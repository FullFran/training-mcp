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

// MemoryTypeOption is one choice of memory type.
type MemoryTypeOption struct{ Value, Label string }

// MemoryView is a remembered fact as the memory page shows it.
type MemoryView struct {
	ID                           int64
	Type, TypeLabel, Title, Body string
	Pinned                       bool
	When                         string
}

// MemoryData is the memory page: the facts found and the search that found them.
type MemoryData struct {
	Items       []MemoryView
	Query, Type string
	Types       []MemoryTypeOption
}

const memoryPageLimit = 200

func (s *Server) memoryPage(w http.ResponseWriter, r *http.Request) {
	s.renderMemory(w, r, http.StatusOK, "")
}

func (s *Server) renderMemory(w http.ResponseWriter, r *http.Request, status int, message string) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	kind := strings.TrimSpace(r.URL.Query().Get("type"))
	data := pageData{Base: s.base, Today: s.todayDate(), Error: message}
	data.Memory = MemoryData{Query: q, Type: kind}
	for _, t := range training.MemoryTypes {
		data.Memory.Types = append(data.Memory.Types, MemoryTypeOption{Value: t, Label: memoryTypeLabel(t)})
	}
	items, err := s.service(r.Context()).SearchMemory(r.Context(), training.MemoryQuery{Text: q, Type: kind, Limit: memoryPageLimit})
	switch {
	case errors.Is(err, training.ErrValidation):
		status, data.Error = http.StatusBadRequest, "Ese tipo no existe."
	case err != nil:
		s.fail(w, err)
		return
	}
	for _, m := range items {
		data.Memory.Items = append(data.Memory.Items, MemoryView{
			ID: m.ID, Type: m.Type, TypeLabel: memoryTypeLabel(m.Type), Title: m.Title, Body: m.Body, Pinned: m.Pinned,
			When: m.UpdatedAt.In(s.clock().Location()).Format("2006-01-02"),
		})
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
// applied at once, never proposed.
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
