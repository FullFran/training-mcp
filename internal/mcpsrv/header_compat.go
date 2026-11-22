package mcpsrv

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
)

// maxCompatBody bounds how much of a request body fillMissingMcpName reads to find
// the resource URI. Larger bodies pass through untouched.
const maxCompatBody = 1 << 20

// fillMissingMcpName sets the Mcp-Name header of a resources/read request from the
// URI in its body when the client left the header out.
//
// Revision 2026-07-28 requires Mcp-Name to equal params.uri on resources/read, and
// the SDK answers 400 when it is absent. ChatGPT sends the header on tools/call but
// not on resources/read, so every app view read failed and no view rendered. The
// header only routes; it grants nothing, so filling it from the body loses no check.
// A header that is present is left alone, so a mismatch is still rejected.
func fillMissingMcpName(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost &&
			r.Header.Get("Mcp-Method") == "resources/read" &&
			r.Header.Get("Mcp-Name") == "" &&
			r.Body != nil {
			body, err := io.ReadAll(io.LimitReader(r.Body, maxCompatBody+1))
			rest := r.Body
			r.Body = struct {
				io.Reader
				io.Closer
			}{io.MultiReader(bytes.NewReader(body), rest), rest}
			if err == nil && len(body) <= maxCompatBody {
				var msg struct {
					Method string `json:"method"`
					Params struct {
						URI string `json:"uri"`
					} `json:"params"`
				}
				if json.Unmarshal(body, &msg) == nil && msg.Method == "resources/read" && msg.Params.URI != "" {
					r.Header.Set("Mcp-Name", msg.Params.URI)
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}
