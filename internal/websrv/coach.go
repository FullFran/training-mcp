package websrv

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/fullfran/training-mcp/internal/engine"
	"github.com/fullfran/training-mcp/internal/training"
)

// Coach is the in-app coach as the web app uses it. Every call names the
// person; the app only ever passes the person the request was authenticated as.
type Coach interface {
	Status(userID int64) engine.Status
	StartLogin(userID int64) (engine.LoginInfo, error)
	Disconnect(userID int64) error
	Subscribe(userID int64) (<-chan engine.Event, func())
	Prompt(userID int64, text string) error
	Abort(userID int64) bool
	Notify(userID int64, ev engine.WorkoutEvent)
}

const (
	maxCoachPrompt = 4000
	// coachHeader must accompany every coach write. A cross-site form cannot set
	// it, which matters on the secret mount, where there is no session token.
	coachHeader    = "X-Coach"
	sseHeartbeat   = 20 * time.Second
	coachNoStore   = "no-store"
	coachJSONType  = "application/json"
	coachEventType = "text/event-stream"
)

var coachHTTPStatus = map[engine.ErrorCode]int{
	engine.CodeBusy:              http.StatusTooManyRequests,
	engine.CodeRateLimited:       http.StatusTooManyRequests,
	engine.CodeTurnInProgress:    http.StatusConflict,
	engine.CodeNotConnected:      http.StatusConflict,
	engine.CodeReconnect:         http.StatusConflict,
	engine.CodeEngineUnavailable: http.StatusServiceUnavailable,
	engine.CodeModelBusy:         http.StatusServiceUnavailable,
	engine.CodeProviderError:     http.StatusBadGateway,
	engine.CodeLoginFailed:       http.StatusBadGateway,
	engine.CodeTurnLimit:         http.StatusGatewayTimeout,
}

func (s *Server) coachRoutes() {
	if s.opts.Coach == nil {
		return
	}
	b := s.base
	s.handle("GET "+b+"/coach/status", s.coachUser(s.coachStatus))
	s.handle("POST "+b+"/coach/login", s.coachWrite(s.coachLogin))
	s.handle("POST "+b+"/coach/disconnect", s.coachWrite(s.coachDisconnect))
	s.handle("GET "+b+"/coach/stream", s.coachUser(s.coachStream))
	s.handle("POST "+b+"/coach/prompt", s.coachWrite(s.coachPrompt))
	s.handle("POST "+b+"/coach/abort", s.coachWrite(s.coachAbort))
}

type coachHandler func(w http.ResponseWriter, r *http.Request, userID int64)

// coachUser runs h as the request's person. A request with no identity never
// reaches the coach: it would otherwise share one slot of nobody's.
func (s *Server) coachUser(h coachHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", coachNoStore)
		userID, ok := training.UserFrom(r.Context())
		if !ok {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		h(w, r, userID)
	}
}

func (s *Server) coachWrite(h coachHandler) http.HandlerFunc {
	return s.coachUser(func(w http.ResponseWriter, r *http.Request, userID int64) {
		if r.Header.Get(coachHeader) != "1" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		h(w, r, userID)
	})
}

func writeCoachJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", coachJSONType)
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// coachError answers with the fixed code only; nothing a provider said leaves.
func coachError(w http.ResponseWriter, err error) {
	var e *engine.Error
	code := engine.CodeEngineUnavailable
	if errors.As(err, &e) {
		code = e.Code
	}
	status, ok := coachHTTPStatus[code]
	if !ok {
		status = http.StatusBadGateway
	}
	writeCoachJSON(w, status, map[string]string{"error": string(code)})
}

func (s *Server) coachStatus(w http.ResponseWriter, _ *http.Request, userID int64) {
	writeCoachJSON(w, http.StatusOK, s.opts.Coach.Status(userID))
}

func (s *Server) coachLogin(w http.ResponseWriter, _ *http.Request, userID int64) {
	info, err := s.opts.Coach.StartLogin(userID)
	if err != nil {
		coachError(w, err)
		return
	}
	writeCoachJSON(w, http.StatusOK, info)
}

func (s *Server) coachDisconnect(w http.ResponseWriter, _ *http.Request, userID int64) {
	if err := s.opts.Coach.Disconnect(userID); err != nil {
		coachError(w, err)
		return
	}
	writeCoachJSON(w, http.StatusOK, map[string]string{"state": "disconnected"})
}

func (s *Server) coachAbort(w http.ResponseWriter, _ *http.Request, userID int64) {
	writeCoachJSON(w, http.StatusOK, map[string]bool{"aborted": s.opts.Coach.Abort(userID)})
}

func (s *Server) coachPrompt(w http.ResponseWriter, r *http.Request, userID int64) {
	var body struct {
		Text string `json:"text"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		writeCoachJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_request"})
		return
	}
	text := strings.TrimSpace(body.Text)
	if text == "" || !utf8.ValidString(text) || utf8.RuneCountInString(text) > maxCoachPrompt {
		writeCoachJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_request"})
		return
	}
	if err := s.opts.Coach.Prompt(userID, text); err != nil {
		coachError(w, err)
		return
	}
	writeCoachJSON(w, http.StatusAccepted, map[string]bool{"started": true})
}

// coachStream is the open chat: every turn of this person, theirs and the
// automatic reactions to their sets, as server-sent events.
func (s *Server) coachStream(w http.ResponseWriter, r *http.Request, userID int64) {
	rc := http.NewResponseController(w)
	events, done := s.opts.Coach.Subscribe(userID)
	defer done()
	w.Header().Set("Content-Type", coachEventType)
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, ": open\n\n")
	if rc.Flush() != nil {
		return
	}
	beat := time.NewTicker(sseHeartbeat)
	defer beat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-beat.C:
			fmt.Fprint(w, ": beat\n\n")
		case ev, ok := <-events:
			if !ok {
				return
			}
			data, err := json.Marshal(ev.Data)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Name, data)
		}
		if rc.Flush() != nil {
			return
		}
	}
}

// notifySetLogged tells the person's coach about a set they just logged in the
// app. Without a coach it does nothing.
func (s *Server) notifySetLogged(r *http.Request, set training.Set) {
	if s.opts.Coach == nil {
		return
	}
	userID, ok := training.UserFrom(r.Context())
	if !ok {
		return
	}
	s.opts.Coach.Notify(userID, engine.SetLogged(s.clock(), set.Exercise, set.WeightKG, set.Reps, set.RPE, set.Technique))
}
