package httpsrv

import (
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
)

const (
	// keyRetryInterval limits key fetches caused by tokens naming an unknown key,
	// so anyone can send those without making this server hammer the provider.
	keyRetryInterval = time.Minute
	minRSAKeyBits    = 2048
	maxLoggedClaim   = 120
)

// OAuthConfig describes the provider this server trusts and the one account it
// serves. This is a single-user service: a valid token for anyone else, even from
// the same provider, is refused.
type OAuthConfig struct {
	Issuer   string
	Resource string
	Subject  string
	// JWKSURL defaults to the provider's standard location under Issuer.
	JWKSURL string
	Logger  *log.Logger
}

// OAuth verifies access tokens issued by an external authorization server. It
// only ever reads them: login, consent and token issuance all belong to the
// provider, so there is no credential here to leak.
type OAuth struct {
	cfg         OAuthConfig
	parser      *jwt.Parser
	client      *http.Client
	metadataURL string

	mu       sync.Mutex
	keys     map[string]*rsa.PublicKey
	lastMiss time.Time
}

func NewOAuth(cfg OAuthConfig) *OAuth {
	if cfg.JWKSURL == "" {
		cfg.JWKSURL = strings.TrimRight(cfg.Issuer, "/") + "/.well-known/jwks.json"
	}
	if cfg.Logger == nil {
		cfg.Logger = log.Default()
	}
	return &OAuth{
		cfg: cfg,
		parser: jwt.NewParser(
			jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Alg()}),
			jwt.WithIssuer(cfg.Issuer),
			jwt.WithAudience(cfg.Resource),
			jwt.WithExpirationRequired(),
			jwt.WithLeeway(30*time.Second),
		),
		client: &http.Client{
			Timeout: 5 * time.Second,
			// Trust in these keys comes from where they were fetched, so they are
			// never accepted from wherever a redirect happens to point.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		metadataURL: metadataURLFor(cfg.Resource),
	}
}

// metadataURLFor builds the RFC 9728 location: the well-known prefix goes between
// the host and the resource's path.
func metadataURLFor(resource string) string {
	u, err := url.Parse(resource)
	if err != nil {
		return ""
	}
	u.Path = "/.well-known/oauth-protected-resource" + strings.TrimRight(u.Path, "/")
	u.RawQuery, u.Fragment = "", ""
	return u.String()
}

func (o *OAuth) verify(raw string) error {
	var claims jwt.RegisteredClaims
	if _, err := o.parser.ParseWithClaims(raw, &claims, o.keyFor); err != nil {
		return fmt.Errorf("%w (aud=%q iss=%q)", err, clip(strings.Join(claims.Audience, ",")), clip(claims.Issuer))
	}
	if claims.Subject != o.cfg.Subject {
		return fmt.Errorf("subject %q is not the owner", clip(claims.Subject))
	}
	return nil
}

// clip bounds a claim before logging: until the signature checks out, these
// values are whatever the sender chose.
func clip(s string) string {
	if len(s) > maxLoggedClaim {
		return s[:maxLoggedClaim]
	}
	return s
}

func (o *OAuth) keyFor(token *jwt.Token) (any, error) {
	kid, _ := token.Header["kid"].(string)
	o.mu.Lock()
	defer o.mu.Unlock()
	if key, ok := o.keys[kid]; ok {
		return key, nil
	}
	// An unknown key may mean the provider rotated, so look once. A miss is
	// remembered, so a flood of invented key ids costs at most one fetch a minute.
	if time.Since(o.lastMiss) < keyRetryInterval {
		return nil, errors.New("unknown signing key")
	}
	keys, err := o.fetchKeys()
	if err != nil {
		o.lastMiss = time.Now()
		return nil, fmt.Errorf("fetching signing keys: %w", err)
	}
	// An empty answer says nothing about the keys already trusted, so keep them.
	if len(keys) > 0 {
		o.keys = keys
	}
	key, ok := keys[kid]
	if !ok {
		o.lastMiss = time.Now()
		return nil, errors.New("unknown signing key")
	}
	return key, nil
}

func (o *OAuth) fetchKeys() (map[string]*rsa.PublicKey, error) {
	resp, err := o.client.Get(o.cfg.JWKSURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	var doc struct {
		Keys []struct {
			Kty string `json:"kty"`
			Use string `json:"use"`
			Kid string `json:"kid"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 1<<20)).Decode(&doc); err != nil {
		return nil, err
	}
	keys := make(map[string]*rsa.PublicKey, len(doc.Keys))
	for _, k := range doc.Keys {
		if k.Kty != "RSA" || k.Kid == "" || (k.Use != "" && k.Use != "sig") {
			continue
		}
		n, errN := base64.RawURLEncoding.DecodeString(k.N)
		e, errE := base64.RawURLEncoding.DecodeString(k.E)
		if errN != nil || errE != nil {
			continue
		}
		exponent := new(big.Int).SetBytes(e)
		modulus := new(big.Int).SetBytes(n)
		if modulus.BitLen() < minRSAKeyBits || exponent.BitLen() > 31 || exponent.Int64() < 3 {
			continue
		}
		keys[k.Kid] = &rsa.PublicKey{N: modulus, E: int(exponent.Int64())}
	}
	return keys, nil
}

// guard admits the static bearer token or a valid access token. Failures point
// the client at the metadata document, which is how it discovers where to log in.
func (o *OAuth) guard(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if staticTokenMatches(r, token) {
			next.ServeHTTP(w, r)
			return
		}
		challenge := fmt.Sprintf(`Bearer resource_metadata=%q`, o.metadataURL)
		if raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
			err := o.verify(strings.TrimSpace(raw))
			if err == nil {
				next.ServeHTTP(w, r)
				return
			}
			o.cfg.Logger.Printf("oauth token rejected: %v", err)
			challenge += `, error="invalid_token"`
		}
		w.Header().Set("WWW-Authenticate", challenge)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	})
}

func (o *OAuth) metadataHandler() http.Handler {
	return auth.ProtectedResourceMetadataHandler(&oauthex.ProtectedResourceMetadata{
		Resource:               o.cfg.Resource,
		AuthorizationServers:   []string{o.cfg.Issuer},
		BearerMethodsSupported: []string{"header"},
	})
}
