package websrv

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/fullfran/training-mcp/internal/training"
)

// memoryTypeLabels name each memory type in the app's language.
var memoryTypeLabels = map[string]string{
	training.MemoryPreference: "Preferencia",
	training.MemoryLimitation: "Limitación",
	training.MemoryGoal:       "Objetivo",
	training.MemoryEquipment:  "Material",
	training.MemoryWorked:     "Lo que funcionó",
	training.MemoryNote:       "Nota",
}

func memoryTypeLabel(t string) string {
	if label, ok := memoryTypeLabels[t]; ok {
		return label
	}
	return t
}

// memoryConflicts is the panel's view of conflicts, beside the store's views.
const memoryConflicts = "conflicts"

// memoryViewLabels name each view of the panel, in the order it shows them.
var memoryViewLabels = []MemoryOption{
	{training.MemoryViewActive, "Vigentes"},
	{training.MemoryViewDue, "Por revisar"},
	{memoryConflicts, "Conflictos"},
	{training.MemoryViewSuperseded, "Sustituidos"},
	{training.MemoryViewForgotten, "Olvidados"},
}

// MemoryOption is one choice of a memory type or view.
type MemoryOption struct{ Value, Label string }

// MemoryView is a remembered fact as the memory page shows it.
type MemoryView struct {
	ID                           int64
	Type, TypeLabel, Title, Body string
	Pinned, Due, Active          bool
	Revision                     int
	When, Status                 string
	History                      []training.MemoryRevision
}

// ConflictView is a conflict the person resolves: the newer fact against the older.
type ConflictView struct {
	ID       int64
	Reason   string
	New, Old MemoryView
}

// MemoryData is the memory page: the view shown, the facts or conflicts in it,
// and the search that found them.
type MemoryData struct {
	Items             []MemoryView
	Conflicts         []ConflictView
	Query, Type, View string
	Types, Views      []MemoryOption
}

const memoryPageLimit = 200

func (s *Server) memoryView(m training.Memory) MemoryView {
	now := s.clock()
	v := MemoryView{
		ID: m.ID, Type: m.Type, TypeLabel: memoryTypeLabel(m.Type), Title: m.Title, Body: m.Body, Pinned: m.Pinned,
		Revision: m.Revision, Due: m.Due(now), Active: m.DeletedAt.IsZero() && m.SupersededBy == 0,
		When: m.UpdatedAt.In(now.Location()).Format("2006-01-02"),
	}
	switch {
	case m.ForgetReason == training.MemoryConsolidated:
		v.Status = "fusionado en el recuerdo " + strconv.FormatInt(m.ConsolidatedInto, 10)
	case !m.DeletedAt.IsZero():
		v.Status = "olvidado el " + m.DeletedAt.In(now.Location()).Format("2006-01-02")
	case m.SupersededBy != 0:
		v.Status = "sustituido por el recuerdo " + strconv.FormatInt(m.SupersededBy, 10)
	case v.Due:
		v.Status = "por revisar"
	}
	return v
}

func (s *Server) memoryPage(w http.ResponseWriter, r *http.Request) {
	s.renderMemory(w, r, http.StatusOK, "")
}

func (s *Server) renderMemory(w http.ResponseWriter, r *http.Request, status int, message string) {
	ctx := r.Context()
	svc := s.service(ctx)
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	kind := strings.TrimSpace(r.URL.Query().Get("type"))
	view := strings.TrimSpace(r.URL.Query().Get("view"))
	if view == "" {
		view = training.MemoryViewActive
	}
	data := pageData{Base: s.base, Today: s.todayDate(), Error: message}
	data.Memory = MemoryData{Query: q, Type: kind, View: view, Views: memoryViewLabels}
	for _, t := range training.MemoryTypes {
		data.Memory.Types = append(data.Memory.Types, MemoryOption{Value: t, Label: memoryTypeLabel(t)})
	}
	var err error
	if view == memoryConflicts {
		var conflicts []training.MemoryConflict
		if conflicts, err = svc.MemoryConflicts(ctx); err == nil {
			for _, c := range conflicts {
				data.Memory.Conflicts = append(data.Memory.Conflicts, ConflictView{
					ID: c.Relation.ID, Reason: c.Relation.Reason, New: s.memoryView(c.New), Old: s.memoryView(c.Old),
				})
			}
		}
	} else {
		var items []training.Memory
		if items, err = svc.SearchMemory(ctx, training.MemoryQuery{Text: q, Type: kind, View: view, Limit: memoryPageLimit}); err == nil {
			for _, m := range items {
				v := s.memoryView(m)
				if m.Revision > 1 {
					if v.History, err = svc.MemoryHistory(ctx, m.ID); err != nil {
						break
					}
				}
				data.Memory.Items = append(data.Memory.Items, v)
			}
		}
	}
	switch {
	case errors.Is(err, training.ErrValidation):
		status, data.Error = http.StatusBadRequest, "Ese tipo o esa vista no existen."
	case err != nil:
		s.fail(w, err)
		return
	}
	if status != http.StatusOK {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
	}
	s.render(w, r, "memory.html", data)
}

// memoryDone answers a successful change: htmx gets the page back, a plain form
// is sent back to the page so reloading does not post again.
func (s *Server) memoryDone(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("HX-Request") == "true" {
		s.renderMemory(w, r, http.StatusOK, "")
		return
	}
	http.Redirect(w, r, s.base+"/memory", http.StatusSeeOther)
}

// memoryFailed shows a refused change. Another user's fact gets what a missing one gets.
func (s *Server) memoryFailed(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, training.ErrNotFound):
		s.renderMemory(w, r, http.StatusOK, "Ese recuerdo ya no existe.")
	case errors.Is(err, training.ErrNotPending):
		s.renderMemory(w, r, http.StatusConflict, "Ese conflicto ya estaba resuelto.")
	case errors.Is(err, training.ErrValidation):
		s.renderMemory(w, r, http.StatusBadRequest, "Revisa el título, el texto y el tipo.")
	default:
		s.fail(w, err)
	}
}

func (s *Server) memoryID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		s.renderMemory(w, r, http.StatusBadRequest, "No se pudo identificar el recuerdo.")
		return 0, false
	}
	return id, true
}

// updateMemory is the person editing a fact. It is their own data, so it is
// applied at once, never proposed; the earlier text is kept as history.
func (s *Server) updateMemory(w http.ResponseWriter, r *http.Request) {
	id, ok := s.memoryID(w, r)
	if !ok {
		return
	}
	title, body, kind := r.PostFormValue("title"), r.PostFormValue("body"), r.PostFormValue("type")
	pinned := r.PostFormValue("pinned") == "1"
	if _, err := s.service(r.Context()).UpdateMemory(r.Context(), id, training.MemoryPatch{Title: &title, Body: &body, Type: &kind, Pinned: &pinned}); err != nil {
		s.memoryFailed(w, r, err)
		return
	}
	s.memoryDone(w, r)
}

// confirmMemory is the person saying a fact still holds.
func (s *Server) confirmMemory(w http.ResponseWriter, r *http.Request) {
	id, ok := s.memoryID(w, r)
	if !ok {
		return
	}
	if _, err := s.service(r.Context()).ConfirmMemory(r.Context(), id); err != nil {
		s.memoryFailed(w, r, err)
		return
	}
	s.memoryDone(w, r)
}

// deleteMemory deletes a fact for good, forgotten or not: only the person can.
func (s *Server) deleteMemory(w http.ResponseWriter, r *http.Request) {
	id, ok := s.memoryID(w, r)
	if !ok {
		return
	}
	if _, err := s.service(r.Context()).DeleteMemory(r.Context(), id); err != nil {
		s.memoryFailed(w, r, err)
		return
	}
	s.memoryDone(w, r)
}

func (s *Server) resolveConflict(w http.ResponseWriter, r *http.Request) {
	id, ok := s.memoryID(w, r)
	if !ok {
		return
	}
	if err := s.service(r.Context()).ResolveMemoryConflict(r.Context(), id, r.PostFormValue("keep")); err != nil {
		s.memoryFailed(w, r, err)
		return
	}
	s.memoryDone(w, r)
}

// deleteAllMemoryWord must be typed to delete everything, so a stray tap cannot.
const deleteAllMemoryWord = "borrar"

func (s *Server) deleteAllMemory(w http.ResponseWriter, r *http.Request) {
	if !strings.EqualFold(strings.TrimSpace(r.PostFormValue("confirm")), deleteAllMemoryWord) {
		s.renderMemory(w, r, http.StatusBadRequest, "Escribe «borrar» para borrar todo lo que el entrenador recuerda.")
		return
	}
	if _, err := s.service(r.Context()).DeleteAllMemory(r.Context()); err != nil {
		s.fail(w, err)
		return
	}
	s.memoryDone(w, r)
}
