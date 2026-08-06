package httpsrv

import (
	"crypto/subtle"
	"encoding/json"
	"log"
	"net/http"
	"strings"
)

func Bearer(token string, disabled bool, next http.Handler) http.Handler {
	return BearerWithPolicy(token, disabled, func(*http.Request) bool { return true }, next)
}

// BearerWithPolicy separates authentication from authorization. The production
// single-user policy is allow-all, while callers can inject a scoped policy.
func BearerWithPolicy(token string, disabled bool, authorize func(*http.Request) bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if disabled {
			next.ServeHTTP(w, r)
			return
		}
		auth := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if !strings.HasPrefix(auth, prefix) {
			unauthorized(w)
			return
		}
		provided := []byte(strings.TrimSpace(strings.TrimPrefix(auth, prefix)))
		expected := []byte(token)
		if len(provided) != len(expected) || subtle.ConstantTimeCompare(provided, expected) != 1 {
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
func Routes(mcp http.Handler, authDisabled bool, token string) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/mcp", MaxBody(Bearer(token, authDisabled, mcp)))
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
