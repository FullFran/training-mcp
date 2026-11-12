package httpsrv

import (
	"crypto/subtle"
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"github.com/fullfran/training-mcp/internal/engine"
	"github.com/fullfran/training-mcp/internal/training"
)

func Bearer(token string, disabled bool, next http.Handler) http.Handler {
	return BearerWithPolicy(token, disabled, func(*http.Request) bool { return true }, next)
}

// BearerWithPolicy separates authentication from authorization. The static
// token identifies only the owner, so production allows all; callers can
// inject a scoped policy.
func BearerWithPolicy(token string, disabled bool, authorize func(*http.Request) bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if disabled {
			next.ServeHTTP(w, r)
			return
		}
		if !staticTokenMatches(r, token) {
			unauthorized(w)
			return
		}
		if authorize != nil && !authorize(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// staticTokenMatches compares in constant time. An empty configured token never
// matches, so an empty header can never open anything.
func staticTokenMatches(r *http.Request, token string) bool {
	auth := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if token == "" || !strings.HasPrefix(auth, prefix) {
		return false
	}
	provided := []byte(strings.TrimSpace(strings.TrimPrefix(auth, prefix)))
	expected := []byte(token)
	return len(provided) == len(expected) && subtle.ConstantTimeCompare(provided, expected) == 1
}

// asOwner marks a request as the owner's. The static token and the auth bypass
// identify nobody else, so /mcp behind them acts as the owner.
func asOwner(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(training.WithUser(r.Context(), training.OwnerUserID)))
	})
}
func unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", "Bearer")
	http.Error(w, "unauthorized", http.StatusUnauthorized)
}
func MaxBody(next http.Handler) http.Handler { return http.MaxBytesHandler(next, 1<<20) }
func WarningIfDisabled(disabled bool, logger *log.Logger) {
	if disabled {
		logger.Printf("WARNING: MCP_AUTH_DISABLED=1 is unsafe and only supported for tunnel-only development")
	}
}

// Routes serves /mcp behind the static token and /health. No other path exists.
func Routes(mcp http.Handler, authDisabled bool, token string) http.Handler {
	return RoutesWithOAuth(mcp, authDisabled, token, nil)
}

// RoutesWithOAuth also accepts access tokens on /mcp when oauth is set. These
// tokens are issued for the MCP resource and open nothing else.
func RoutesWithOAuth(mcp http.Handler, authDisabled bool, token string, oauth *OAuth) http.Handler {
	return RoutesWithLogin(mcp, authDisabled, token, oauth, nil)
}

// RoutesWithLogin also serves the app at the root when root is set, to whoever
// signs in with the provider, as their own user. It is the web app's only
// entrance: bearers never open it, and a browser session never opens /mcp.
func RoutesWithLogin(mcp http.Handler, authDisabled bool, token string, oauth *OAuth, root *RootWeb) http.Handler {
	return RoutesWithEngine(mcp, authDisabled, token, oauth, root, nil)
}

// EngineTokens resolves the bearers the in-app coach gives its own engine
// processes. Nil leaves /mcp exactly as it is without the coach.
type EngineTokens interface {
	Resolve(token string) (userID int64, ok bool)
}

// RoutesWithEngine also lets the coach's engine processes call /mcp, each as its
// own person.
func RoutesWithEngine(mcp http.Handler, authDisabled bool, token string, oauth *OAuth, root *RootWeb, engineTokens EngineTokens) http.Handler {
	mux := http.NewServeMux()
	var door http.Handler
	if oauth != nil && !authDisabled {
		door = oauth.guard(token, mcp)
		metadata := oauth.metadataHandler()
		mux.Handle("/.well-known/oauth-protected-resource", metadata)
		mux.Handle("/.well-known/oauth-protected-resource/mcp", metadata)
	} else {
		door = Bearer(token, authDisabled, asOwner(mcp))
	}
	mux.Handle("/mcp", MaxBody(engineGuard(engineTokens, mcp, door)))
	if root != nil && root.Handler != nil && root.Login != nil {
		mux.Handle("/", MaxBody(root.Login.Wrap(root.Handler)))
		// Discovery probes and paths under /mcp keep answering 404, as they did
		// before the root served anything.
		mux.Handle("/.well-known/", http.NotFoundHandler())
		mux.Handle("/mcp/", http.NotFoundHandler())
	}
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
	return mux
}

// engineGuard admits an engine token as the person it was minted for, and only
// on a direct loopback connection. It runs before every other door, so not even
// the auth bypass turns an engine into the owner. Any other bearer goes on to
// the usual door unchanged.
func engineGuard(tokens EngineTokens, mcp, next http.Handler) http.Handler {
	if tokens == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		raw = strings.TrimSpace(raw)
		if !ok || !engine.LooksLikeToken(raw) {
			next.ServeHTTP(w, r)
			return
		}
		userID, live := tokens.Resolve(raw)
		if !live || !engine.DirectLoopback(r) {
			unauthorized(w)
			return
		}
		mcp.ServeHTTP(w, r.WithContext(training.WithUser(r.Context(), userID)))
	})
}
