package websrv

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
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
	Prompt(userID, conversation int64, text string, images ...engine.Image) (int64, error)
	Abort(userID int64) bool
	Notify(userID int64, ev engine.WorkoutEvent)
	Models(userID int64) (engine.ModelList, error)
	SetPreferences(userID int64, model, thinking string) error
	Conversations(userID int64) ([]engine.Conversation, error)
	Conversation(userID, conversation int64) (engine.Conversation, []engine.ConversationMessage, error)
	OpenConversation(userID, conversation int64) error
	RenameConversation(userID, conversation int64, title string) error
	DeleteConversation(userID, conversation int64) error
	DeleteConversations(userID int64) error
}

const (
	maxCoachPrompt = 4000
	// maxCoachBody fits the photos of one turn in base64 plus the text.
	maxCoachBody    = engine.MaxImagesBytes/3*4 + 64<<10
	coachUploadTime = 60 * time.Second
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
	engine.CodeBadRequest:        http.StatusBadRequest,
	engine.CodeNotFound:          http.StatusNotFound,
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
	s.handle("GET "+b+"/coach/models", s.coachUser(s.coachModels))
	s.handle("POST "+b+"/coach/settings", s.coachWrite(s.coachSettings))
	s.connectorRoutes()
	s.handle("POST "+b+"/coach/open", s.coachWrite(s.coachOpen))
	s.handle("GET "+b+"/coach/conversations", s.coachUser(s.coachConversations))
	s.handle("GET "+b+"/coach/conversations/{id}", s.coachUser(s.coachConversation))
	s.handle("POST "+b+"/coach/conversations/{id}/rename", s.coachWrite(s.coachRename))
	s.handle("POST "+b+"/coach/conversations/{id}/delete", s.coachWrite(s.coachDelete))
	s.handle("POST "+b+"/coach/conversations/delete-all", s.coachWrite(s.coachDeleteAll))
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

// coachModels lists the models the person's ChatGPT login can use, as their
// own engine reports them, with the current choice.
func (s *Server) coachModels(w http.ResponseWriter, _ *http.Request, userID int64) {
	list, err := s.opts.Coach.Models(userID)
	if err != nil {
		coachError(w, err)
		return
	}
	writeCoachJSON(w, http.StatusOK, list)
}

// coachSettings keeps the person's model and thinking level. The coach refuses
// a model their engine did not list.
func (s *Server) coachSettings(w http.ResponseWriter, r *http.Request, userID int64) {
	var body struct {
		Model    string `json:"model"`
		Thinking string `json:"thinking"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		writeCoachJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_request"})
		return
	}
	if err := s.opts.Coach.SetPreferences(userID, body.Model, body.Thinking); err != nil {
		coachError(w, err)
		return
	}
	writeCoachJSON(w, http.StatusOK, map[string]string{"model": body.Model, "thinking": body.Thinking})
}

func (s *Server) coachAbort(w http.ResponseWriter, _ *http.Request, userID int64) {
	writeCoachJSON(w, http.StatusOK, map[string]bool{"aborted": s.opts.Coach.Abort(userID)})
}

func (s *Server) coachPrompt(w http.ResponseWriter, r *http.Request, userID int64) {
	// A turn with photos is a few megabytes; a slow sender gets a bounded time
	// to finish it. The recorder in tests has no deadline to set.
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(coachUploadTime))
	var body struct {
		Text         string `json:"text"`
		Conversation int64  `json:"conversation"`
		Images       []struct {
			Type string `json:"type"`
			Data []byte `json:"data"` // base64 in the request
		} `json:"images"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxCoachBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeCoachJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "images_too_large"})
			return
		}
		writeCoachJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_request"})
		return
	}
	text := strings.TrimSpace(body.Text)
	if !utf8.ValidString(text) || utf8.RuneCountInString(text) > maxCoachPrompt || (text == "" && len(body.Images) == 0) {
		writeCoachJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_request"})
		return
	}
	images := make([]engine.Image, len(body.Images))
	for i, im := range body.Images {
		images[i] = engine.Image{MIME: im.Type, Data: im.Data}
	}
	if len(images) > engine.MaxImages {
		writeCoachJSON(w, http.StatusBadRequest, map[string]string{"error": "too_many_images"})
		return
	}
	if !engine.ValidImages(images) {
		// Wrong type, bytes that are not that type, empty, or over a size limit.
		writeCoachJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_image"})
		return
	}
	if body.Conversation < 0 {
		writeCoachJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_request"})
		return
	}
	conv, err := s.opts.Coach.Prompt(userID, body.Conversation, text, images...)
	if err != nil {
		coachError(w, err)
		return
	}
	writeCoachJSON(w, http.StatusAccepted, map[string]any{"started": true, "conversation": conv})
}

// --- conversations ------------------------------------------------------------

// conversationID is the {id} of a conversation route: a positive number, or the
// route answers 404 as for a conversation that is not the person's.
func conversationID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeCoachJSON(w, http.StatusNotFound, map[string]string{"error": string(engine.CodeNotFound)})
		return 0, false
	}
	return id, true
}

// decodeCoachBody reads a small JSON body with no unknown fields.
func decodeCoachBody(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeCoachJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_request"})
		return false
	}
	return true
}

func (s *Server) coachConversations(w http.ResponseWriter, _ *http.Request, userID int64) {
	list, err := s.opts.Coach.Conversations(userID)
	if err != nil {
		coachError(w, err)
		return
	}
	writeCoachJSON(w, http.StatusOK, map[string]any{"conversations": list})
}

// coachConversation returns a conversation's messages as JSON text; the page
// renders them as text, never as HTML.
func (s *Server) coachConversation(w http.ResponseWriter, r *http.Request, userID int64) {
	id, ok := conversationID(w, r)
	if !ok {
		return
	}
	c, msgs, err := s.opts.Coach.Conversation(userID, id)
	if err != nil {
		coachError(w, err)
		return
	}
	writeCoachJSON(w, http.StatusOK, map[string]any{"id": c.ID, "title": c.Title, "updated_at": c.UpdatedAt, "messages": msgs})
}

// coachOpen makes a conversation the open one (0: a new chat), where the
// reactions to logged sets go.
func (s *Server) coachOpen(w http.ResponseWriter, r *http.Request, userID int64) {
	var body struct {
		Conversation int64 `json:"conversation"`
	}
	if !decodeCoachBody(w, r, &body) {
		return
	}
	if body.Conversation < 0 {
		writeCoachJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_request"})
		return
	}
	if err := s.opts.Coach.OpenConversation(userID, body.Conversation); err != nil {
		coachError(w, err)
		return
	}
	writeCoachJSON(w, http.StatusOK, map[string]int64{"conversation": body.Conversation})
}

func (s *Server) coachRename(w http.ResponseWriter, r *http.Request, userID int64) {
	id, ok := conversationID(w, r)
	if !ok {
		return
	}
	var body struct {
		Title string `json:"title"`
	}
	if !decodeCoachBody(w, r, &body) {
		return
	}
	if err := s.opts.Coach.RenameConversation(userID, id, body.Title); err != nil {
		coachError(w, err)
		return
	}
	writeCoachJSON(w, http.StatusOK, map[string]string{"title": strings.TrimSpace(body.Title)})
}

func (s *Server) coachDelete(w http.ResponseWriter, r *http.Request, userID int64) {
	id, ok := conversationID(w, r)
	if !ok {
		return
	}
	if err := s.opts.Coach.DeleteConversation(userID, id); err != nil {
		coachError(w, err)
		return
	}
	writeCoachJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

func (s *Server) coachDeleteAll(w http.ResponseWriter, _ *http.Request, userID int64) {
	if err := s.opts.Coach.DeleteConversations(userID); err != nil {
		coachError(w, err)
		return
	}
	writeCoachJSON(w, http.StatusOK, map[string]bool{"deleted": true})
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
	s.opts.Coach.Notify(userID, engine.SetLogged(s.clock(), set.Exercise, set.WeightKG, set.Reps, set.RPE, set.Technique, set.Category))
}
