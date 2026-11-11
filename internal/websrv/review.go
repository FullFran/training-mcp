package websrv

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/fullfran/training-mcp/internal/training"
)

// actionLabels name each action type in the app's language. A type added later
// without a label shows its own name, so it is never hidden.
var actionLabels = map[string]string{
	"session.start":           "Empezar sesión",
	"session.delete":          "Borrar sesión",
	"set.log":                 "Registrar series",
	"set.update":              "Corregir serie",
	"set.delete":              "Borrar serie",
	"plan.create":             "Crear rutina",
	"plan.update":             "Editar rutina",
	"plan.delete":             "Borrar rutina",
	"plan.exercise.set":       "Poner ejercicio en rutina",
	"plan.exercise.remove":    "Quitar ejercicio de rutina",
	"plan.exercise.move":      "Mover ejercicio de rutina",
	"session.exercise.set":    "Añadir ejercicio a la sesión",
	"session.exercise.adjust": "Ajustar ejercicio de la sesión",
	"session.exercise.swap":   "Cambiar ejercicio de la sesión",
	"session.exercise.remove": "Quitar ejercicio de la sesión",
	"exercise.note.set":       "Guardar nota de ejercicio",
	"exercise.note.remove":    "Borrar nota de ejercicio",
	"exercise.group.set":      "Asignar grupo muscular",
	"feedback.record":         "Valorar grupo muscular",
	"landmarks.set":           "Fijar MEV/MRV",
	"rest.set":                "Cambiar descanso objetivo",
	"memory.save":             "Recordar",
	"memory.forget":           "Olvidar recuerdo",
}

func actionLabel(actionType string) string {
	if label, ok := actionLabels[actionType]; ok {
		return label
	}
	return actionType
}

var statusLabels = map[string]string{
	training.ProposalAccepted: "Aceptada",
	training.ProposalRejected: "Rechazada",
	training.ProposalFailed:   "Falló",
	training.ProposalApplying: "Aplicándose",
}

var decidedByLabels = map[string]string{
	training.DecidedByPerson:       "tú",
	training.DecidedBySessionGrant: "aceptado siempre en esa sesión",
	training.DecidedByDenial:       "rechazado siempre en esa sesión",
	training.DecidedByAcceptAll:    "aceptar todo",
	// Proposals decided before answers became session-limited.
	training.DecidedBySession: "recordado 12 h",
	training.DecidedByAlways:  "recordado siempre",
}

// The answers a person can give a pending proposal, in the app's language.
// The always ones only last for the session the proposal was asked in.
var answerLabels = map[string]string{
	training.AnswerAllowOnce:    "Aceptar una vez",
	training.AnswerRejectOnce:   "Rechazar",
	training.AnswerAllowAlways:  "Aceptar siempre en esta sesión",
	training.AnswerRejectAlways: "Rechazar siempre en esta sesión",
}

// sessionLabel names the agent session a stored answer is limited to.
func sessionLabel(session string) string {
	if strings.HasPrefix(session, training.CoachSessionPrefix) {
		return "esta conversación con el coach"
	}
	if session == training.ExternalSession {
		return "clientes MCP externos"
	}
	return session
}

// ProposalField is one value of what a proposal would change.
type ProposalField struct{ Name, Value string }

// ProposalView is a proposal as the review page shows it.
type ProposalView struct {
	ID         int64
	ActionType string
	Label      string
	// Summary says in words what the proposal does; empty for a type without
	// one, which shows Fields instead.
	Summary   string
	Fields    []ProposalField
	Status    string
	DecidedBy string
	When      string
	Error     string
	// InSession says the proposal was asked inside an agent session, so the
	// always answers, which last for that session, can be offered.
	InSession bool
}

// RuleView is a stored always answer as the review page shows it.
type RuleView struct {
	ID       int64
	Label    string
	Remember string
}

// ReviewData is what the review page shows.
type ReviewData struct {
	Pending   []ProposalView
	Answered  []ProposalView
	AcceptAll bool
	Rules     []RuleView
	Answers   map[string]string
}

// proposalFields lists the payload's values, nested ones as compact JSON, so the
// person sees exactly what the agent sent.
func proposalFields(payload json.RawMessage) []ProposalField {
	var values map[string]json.RawMessage
	if err := json.Unmarshal(payload, &values); err != nil {
		return []ProposalField{{Name: "datos", Value: string(payload)}}
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]ProposalField, 0, len(names))
	for _, name := range names {
		raw := values[name]
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			var compact bytes.Buffer
			if json.Compact(&compact, raw) == nil {
				text = compact.String()
			} else {
				text = string(raw)
			}
		}
		out = append(out, ProposalField{Name: strings.ReplaceAll(name, "_", " "), Value: text})
	}
	return out
}

func (s *Server) proposalView(p training.Proposal) ProposalView {
	when := p.CreatedAt
	if !p.DecidedAt.IsZero() {
		when = p.DecidedAt
	}
	return ProposalView{
		ID: p.ID, ActionType: p.ActionType, Label: actionLabel(p.ActionType), Summary: proposalSummary(p.Tool, p.Payload),
		Fields: proposalFields(p.Payload),
		Status: statusLabels[p.Status], DecidedBy: decidedByLabels[p.DecidedBy],
		When: when.In(s.clock().Location()).Format("02/01 15:04"), Error: p.Error, InSession: p.Session != "",
	}
}

func (s *Server) reviewData(r *http.Request) (ReviewData, error) {
	ctx := r.Context()
	svc := s.service(ctx)
	pending, err := svc.ListProposals(ctx, training.ProposalFilter{Pending: true, Limit: 100})
	if err != nil {
		return ReviewData{}, err
	}
	answered, err := svc.ListProposals(ctx, training.ProposalFilter{Limit: 30})
	if err != nil {
		return ReviewData{}, err
	}
	perms, err := svc.Permissions(ctx)
	if err != nil {
		return ReviewData{}, err
	}
	data := ReviewData{AcceptAll: perms.AcceptAll, Answers: answerLabels}
	for _, p := range pending {
		data.Pending = append(data.Pending, s.proposalView(p))
	}
	for _, p := range answered {
		data.Answered = append(data.Answered, s.proposalView(p))
	}
	for _, rule := range perms.Rules {
		verb := "aceptar"
		if rule.Effect == training.EffectDeny {
			verb = "rechazar"
		}
		remember := verb + " en " + sessionLabel(rule.Session) + " hasta las " + rule.ExpiresAt.In(s.clock().Location()).Format("15:04 del 02/01")
		data.Rules = append(data.Rules, RuleView{ID: rule.ID, Label: actionLabel(rule.ActionType), Remember: remember})
	}
	return data, nil
}

func (s *Server) reviewPage(w http.ResponseWriter, r *http.Request) {
	s.renderReview(w, r, http.StatusOK, "")
}

func (s *Server) renderReview(w http.ResponseWriter, r *http.Request, status int, message string) {
	review, err := s.reviewData(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	data := pageData{Base: s.base, Today: s.todayDate(), Review: review, Error: message}
	if status != http.StatusOK {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
	}
	s.render(w, r, "review.html", data)
}

// reviewDone answers a successful change: htmx gets the page back, a plain form
// is sent back to the page so reloading does not post again.
func (s *Server) reviewDone(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("HX-Request") == "true" {
		s.renderReview(w, r, http.StatusOK, "")
		return
	}
	http.Redirect(w, r, s.base+"/review", http.StatusSeeOther)
}

// reviewFailed shows a refused answer. Another user's proposal gets what a
// missing one gets.
func (s *Server) reviewFailed(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, training.ErrNotFound):
		s.renderReview(w, r, http.StatusOK, "Esa propuesta ya no existe.")
	case errors.Is(err, training.ErrNotPending):
		s.renderReview(w, r, http.StatusConflict, "Esa propuesta ya estaba respondida.")
	case errors.Is(err, training.ErrValidation):
		s.renderReview(w, r, http.StatusBadRequest, "No se pudo responder: los datos de la propuesta no son válidos o no tiene una sesión que recordar.")
	case errors.Is(err, training.ErrIdempotencyConflict):
		s.renderReview(w, r, http.StatusConflict, "No se pudo aplicar: esas series ya se registraron con otros datos.")
	default:
		s.fail(w, err)
	}
}

func (s *Server) proposalID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		s.renderReview(w, r, http.StatusBadRequest, "No se pudo identificar la propuesta.")
		return 0, false
	}
	return id, true
}

func (s *Server) acceptProposal(w http.ResponseWriter, r *http.Request) {
	id, ok := s.proposalID(w, r)
	if !ok {
		return
	}
	answer := r.PostFormValue("answer")
	switch answer {
	case "", training.AnswerAllowOnce, training.AnswerAllowAlways:
	default:
		s.renderReview(w, r, http.StatusBadRequest, "Elige aceptar una vez o siempre en esta sesión.")
		return
	}
	if s.opts.Apply == nil {
		s.renderReview(w, r, http.StatusServiceUnavailable, "Aquí no se pueden aplicar propuestas.")
		return
	}
	if _, err := s.service(r.Context()).AcceptProposal(r.Context(), id, answer, s.opts.Apply); err != nil {
		s.reviewFailed(w, r, err)
		return
	}
	s.reviewDone(w, r)
}

func (s *Server) rejectProposal(w http.ResponseWriter, r *http.Request) {
	id, ok := s.proposalID(w, r)
	if !ok {
		return
	}
	answer := r.PostFormValue("answer")
	switch answer {
	case "", training.AnswerRejectOnce, training.AnswerRejectAlways:
	default:
		s.renderReview(w, r, http.StatusBadRequest, "Elige rechazar una vez o siempre en esta sesión.")
		return
	}
	if _, err := s.service(r.Context()).RejectProposal(r.Context(), id, answer); err != nil {
		s.reviewFailed(w, r, err)
		return
	}
	s.reviewDone(w, r)
}

func (s *Server) setAcceptAll(w http.ResponseWriter, r *http.Request) {
	var on bool
	switch r.PostFormValue("on") {
	case "1":
		on = true
	case "0":
	default:
		s.renderReview(w, r, http.StatusBadRequest, "Valor inválido para aceptar todo.")
		return
	}
	if err := s.service(r.Context()).SetAcceptAll(r.Context(), on); err != nil {
		s.fail(w, err)
		return
	}
	s.reviewDone(w, r)
}

func (s *Server) forgetRule(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PostFormValue("rule_id"), 10, 64)
	if err != nil || id <= 0 {
		s.renderReview(w, r, http.StatusBadRequest, "No se pudo identificar la respuesta recordada.")
		return
	}
	if err := s.service(r.Context()).ForgetRule(r.Context(), id); err != nil {
		if errors.Is(err, training.ErrNotFound) {
			s.renderReview(w, r, http.StatusOK, "Esa respuesta recordada ya no existe.")
			return
		}
		s.fail(w, err)
		return
	}
	s.reviewDone(w, r)
}

// pendingProposals are the proposals waiting for an answer, shown as cards in
// the chat and counted on the link to the review page. A failed read shows
// none rather than failing the home: the review page still lists them.
func (s *Server) pendingProposals(r *http.Request) []ProposalView {
	pending, err := s.service(r.Context()).ListProposals(r.Context(), training.ProposalFilter{Pending: true, Limit: 100})
	if err != nil {
		return nil
	}
	views := make([]ProposalView, 0, len(pending))
	for _, p := range pending {
		views = append(views, s.proposalView(p))
	}
	return views
}
