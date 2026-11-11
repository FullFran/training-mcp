package websrv

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/fullfran/training-mcp/internal/engine"
	"github.com/fullfran/training-mcp/internal/engine/connector"
)

// Connectors are a person's own remote MCP servers (docs/coach.md). Every call
// names the person the request was authenticated as.
type Connectors interface {
	List(ctx context.Context, userID int64) ([]connector.View, error)
	Add(ctx context.Context, userID int64, rawURL, label, token string) (connector.View, error)
	Connect(ctx context.Context, userID, id int64) (connector.ConnectResult, error)
	Callback(ctx context.Context, userID int64, state, code string) (int64, error)
	Tools(ctx context.Context, userID, id int64) ([]connector.Tool, error)
	Disconnect(ctx context.Context, userID, id int64) error
	Remove(ctx context.Context, userID, id int64) error
	Descriptor(ctx context.Context, userID, id int64) (connector.ServerJSON, error)
}

// Approvals is the part of the coach that answers connector tool permission
// requests and holds the session's always answers.
type Approvals interface {
	Answer(userID int64, id, answer string) error
	Grants(userID int64) []engine.Grant
	Revoke(userID int64, server string)
}

const maxConnectorBody = 16 << 10

var connectorHTTPStatus = map[string]int{
	"not_found":           http.StatusNotFound,
	"exists":              http.StatusConflict,
	"too_many":            http.StatusConflict,
	"bad_url":             http.StatusBadRequest,
	"bad_label":           http.StatusBadRequest,
	"bad_token":           http.StatusBadRequest,
	"blocked_address":     http.StatusBadRequest,
	"state_invalid":       http.StatusBadRequest,
	"oauth_unavailable":   http.StatusConflict,
	"discovery_failed":    http.StatusBadGateway,
	"registration_failed": http.StatusBadGateway,
	"token_failed":        http.StatusBadGateway,
	"unauthorized":        http.StatusBadGateway,
	"unreachable":         http.StatusBadGateway,
	"vault":               http.StatusServiceUnavailable,
}

func (s *Server) connectorRoutes() {
	if s.opts.Connectors == nil {
		return
	}
	b := s.base
	s.handle("GET "+b+"/coach/connectors", s.coachUser(s.connectorsPage))
	s.handle("GET "+b+"/coach/connectors/list", s.coachUser(s.connectorsList))
	s.handle("POST "+b+"/coach/connectors", s.coachWrite(s.connectorAdd))
	s.handle("GET "+b+"/coach/connectors/callback", s.coachUser(s.connectorCallback))
	s.handle("POST "+b+"/coach/connectors/{id}/connect", s.coachWrite(s.connectorAction))
	s.handle("GET "+b+"/coach/connectors/{id}/tools", s.coachUser(s.connectorAction))
	s.handle("GET "+b+"/coach/connectors/{id}/server.json", s.coachUser(s.connectorAction))
	s.handle("POST "+b+"/coach/connectors/{id}/disconnect", s.coachWrite(s.connectorAction))
	s.handle("POST "+b+"/coach/connectors/{id}/remove", s.coachWrite(s.connectorAction))
	s.handle("POST "+b+"/coach/connectors/{id}/revoke", s.coachWrite(s.connectorAction))
	if _, ok := s.opts.Coach.(Approvals); ok {
		s.handle("POST "+b+"/coach/approval", s.coachWrite(s.approvalAnswer))
	}
}

// connectorError answers with the fixed code only.
func connectorError(w http.ResponseWriter, err error) {
	code := connector.CodeOf(err)
	status, ok := connectorHTTPStatus[code]
	if !ok {
		status = http.StatusBadGateway
	}
	writeCoachJSON(w, status, map[string]string{"error": code})
}

func (s *Server) connectorsPage(w http.ResponseWriter, r *http.Request, _ int64) {
	s.render(w, r, "connectors.html", pageData{Base: s.base})
}

func (s *Server) connectorsList(w http.ResponseWriter, r *http.Request, userID int64) {
	list, err := s.opts.Connectors.List(r.Context(), userID)
	if err != nil {
		connectorError(w, err)
		return
	}
	grants := []engine.Grant{}
	if a, ok := s.opts.Coach.(Approvals); ok {
		if g := a.Grants(userID); g != nil {
			grants = g
		}
	}
	writeCoachJSON(w, http.StatusOK, map[string]any{"connectors": list, "grants": grants})
}

func (s *Server) connectorAdd(w http.ResponseWriter, r *http.Request, userID int64) {
	var body struct {
		URL   string `json:"url"`
		Label string `json:"label"`
		Token string `json:"token"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxConnectorBody))
	dec.DisallowUnknownFields()
	if dec.Decode(&body) != nil {
		writeCoachJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_request"})
		return
	}
	v, err := s.opts.Connectors.Add(r.Context(), userID, body.URL, body.Label, body.Token)
	if err != nil {
		connectorError(w, err)
		return
	}
	writeCoachJSON(w, http.StatusCreated, v)
}

func (s *Server) connectorAction(w http.ResponseWriter, r *http.Request, userID int64) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeCoachJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		return
	}
	ctx := r.Context()
	c := s.opts.Connectors
	switch lastSegment(r.URL.Path) {
	case "connect":
		res, err := c.Connect(ctx, userID, id)
		if err != nil {
			connectorError(w, err)
			return
		}
		writeCoachJSON(w, http.StatusOK, res)
	case "tools":
		tools, err := c.Tools(ctx, userID, id)
		if err != nil {
			connectorError(w, err)
			return
		}
		writeCoachJSON(w, http.StatusOK, map[string]any{"tools": tools})
	case "server.json":
		d, err := c.Descriptor(ctx, userID, id)
		if err != nil {
			connectorError(w, err)
			return
		}
		writeCoachJSON(w, http.StatusOK, d)
	case "disconnect":
		if err := c.Disconnect(ctx, userID, id); err != nil {
			connectorError(w, err)
			return
		}
		writeCoachJSON(w, http.StatusOK, map[string]string{"state": "disconnected"})
	case "remove":
		if err := c.Remove(ctx, userID, id); err != nil {
			connectorError(w, err)
			return
		}
		writeCoachJSON(w, http.StatusOK, map[string]bool{"removed": true})
	case "revoke":
		a, ok := s.opts.Coach.(Approvals)
		if !ok {
			writeCoachJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
			return
		}
		list, err := c.List(ctx, userID)
		if err != nil {
			connectorError(w, err)
			return
		}
		for _, v := range list {
			if v.ID == id {
				a.Revoke(userID, v.Name)
				writeCoachJSON(w, http.StatusOK, map[string]bool{"revoked": true})
				return
			}
		}
		writeCoachJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
	default:
		http.NotFound(w, r)
	}
}

func lastSegment(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[i+1:]
		}
	}
	return p
}

// connectorCallback is where an authorization server sends the person back.
// The browser lands on the panel with a fixed code; nothing the server said
// is shown.
func (s *Server) connectorCallback(w http.ResponseWriter, r *http.Request, userID int64) {
	q := r.URL.Query()
	back := s.base + "/coach/connectors"
	if q.Get("error") != "" {
		http.Redirect(w, r, back+"?error=denied", http.StatusSeeOther)
		return
	}
	id, err := s.opts.Connectors.Callback(r.Context(), userID, q.Get("state"), q.Get("code"))
	if err != nil {
		http.Redirect(w, r, back+"?error="+url.QueryEscape(connector.CodeOf(err)), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, back+"?connected="+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

func (s *Server) approvalAnswer(w http.ResponseWriter, r *http.Request, userID int64) {
	var body struct {
		ID     string `json:"id"`
		Answer string `json:"answer"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxConnectorBody))
	dec.DisallowUnknownFields()
	if dec.Decode(&body) != nil {
		writeCoachJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_request"})
		return
	}
	err := s.opts.Coach.(Approvals).Answer(userID, body.ID, body.Answer)
	switch {
	case errors.Is(err, engine.ErrBadAnswer):
		writeCoachJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_answer"})
	case errors.Is(err, engine.ErrNoApproval):
		writeCoachJSON(w, http.StatusNotFound, map[string]string{"error": "no_approval"})
	case err != nil:
		writeCoachJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "engine_unavailable"})
	default:
		writeCoachJSON(w, http.StatusOK, map[string]bool{"answered": true})
	}
}
