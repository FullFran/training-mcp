package httpsrv

import (
	"log"
	"net/http"
	"strings"
)

// LogProtocol writes one line per /mcp request with the MCP revision and method
// the client declared, whether it sent a session id, and its User-Agent. It exists
// to learn which revision each client (ChatGPT in particular) actually speaks.
// Revision 2026-07-28 sends MCP-Protocol-Version and Mcp-Method on every POST;
// older revisions send no Mcp-Method and carry a session id after initialize.
//
// Only these headers are logged: never the body, arguments, results or the
// Authorization header. Every value is client-controlled, so it is reduced to a
// small safe alphabet and clipped before it reaches the log.
func LogProtocol(logger *log.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/mcp" {
			session := "no"
			if r.Header.Get("Mcp-Session-Id") != "" {
				session = "yes"
			}
			logger.Printf("mcp request: version=%s method=%s session=%s client=%s",
				safeHeader(r.Header.Get("MCP-Protocol-Version")),
				safeHeader(r.Header.Get("Mcp-Method")),
				session,
				safeHeader(r.Header.Get("User-Agent")))
		}
		next.ServeHTTP(w, r)
	})
}

const maxLoggedHeader = 64

// safeHeader keeps letters, digits and . _ / : - , replaces anything else with _,
// clips to maxLoggedHeader characters and reports an absent value as "none".
func safeHeader(v string) string {
	if v == "" {
		return "none"
	}
	var b strings.Builder
	for _, c := range v {
		if b.Len() >= maxLoggedHeader {
			break
		}
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '.', c == '_', c == '/', c == ':', c == '-':
			b.WriteRune(c)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}
