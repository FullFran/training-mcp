package websrv

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/fullfran/training-mcp/internal/training"
)

// CatalogueData is Ejercicios under Tú: what list_exercise_groups and
// list_exercise_notes return, joined per exercise.
type CatalogueData struct {
	Entries []CatalogueEntry
	Groups  []string
}

// CatalogueEntry is one exercise with its muscle group and setup note.
type CatalogueEntry struct {
	Exercise, Group, Note string
}

func (s *Server) cataloguePage(w http.ResponseWriter, r *http.Request) {
	s.renderCatalogue(w, r, http.StatusOK, "")
}

func (s *Server) renderCatalogue(w http.ResponseWriter, r *http.Request, status int, message string) {
	data, err := s.catalogueData(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	if status != http.StatusOK {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
	}
	s.render(w, r, "catalogue.html", pageData{Base: s.base, Today: s.todayDate(), Error: message, Exercises: data})
}

func (s *Server) catalogueData(ctx context.Context) (CatalogueData, error) {
	svc := s.service(ctx)
	out := CatalogueData{Groups: training.MuscleGroups}
	names, err := svc.ExerciseNames(ctx)
	if err != nil {
		return out, err
	}
	groups, err := svc.ExerciseGroups(ctx)
	if err != nil {
		return out, err
	}
	notes, err := svc.ExerciseNotes(ctx)
	if err != nil {
		return out, err
	}
	byName := map[string]*CatalogueEntry{}
	entry := func(name string) *CatalogueEntry {
		if e, ok := byName[name]; ok {
			return e
		}
		e := &CatalogueEntry{Exercise: name}
		byName[name] = e
		return e
	}
	for _, n := range names {
		entry(n)
	}
	for _, g := range groups {
		entry(g.Exercise).Group = g.MuscleGroup
	}
	for _, n := range notes {
		entry(n.Exercise).Note = n.Note
	}
	for _, e := range byName {
		out.Entries = append(out.Entries, *e)
	}
	sort.Slice(out.Entries, func(i, j int) bool { return out.Entries[i].Exercise < out.Entries[j].Exercise })
	return out, nil
}

// saveCatalogueEntry changes one exercise's group and note with the calls
// set_exercise_group and set_exercise_note make. Only what changed is
// written; no group chosen keeps the stored one, and an empty note removes it.
func (s *Server) saveCatalogueEntry(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.fail(w, err)
		return
	}
	ctx := r.Context()
	exercise := strings.ToLower(strings.TrimSpace(r.PostFormValue("exercise")))
	group := strings.ToLower(strings.TrimSpace(r.PostFormValue("muscle_group")))
	note := strings.TrimSpace(r.PostFormValue("note"))
	if exercise == "" {
		s.renderCatalogue(w, r, http.StatusBadRequest, "Elige un ejercicio.")
		return
	}
	if group != "" && !training.ValidMuscleGroup(group) {
		s.renderCatalogue(w, r, http.StatusBadRequest, "Grupo muscular inválido.")
		return
	}
	current, err := s.catalogueData(ctx)
	if err != nil {
		s.fail(w, err)
		return
	}
	was := CatalogueEntry{}
	for _, e := range current.Entries {
		if e.Exercise == exercise {
			was = e
		}
	}
	if group != "" && group != was.Group {
		err = s.service(ctx).SetExerciseGroup(ctx, exercise, group)
	}
	if err == nil && r.Form.Has("note") && note != was.Note {
		err = s.service(ctx).SetExerciseNote(ctx, exercise, note)
	}
	switch {
	case errors.Is(err, training.ErrValidation):
		s.renderCatalogue(w, r, http.StatusBadRequest, "Nota inválida: usa 280 caracteres como mucho.")
		return
	case err != nil:
		s.fail(w, err)
		return
	}
	if r.Header.Get("HX-Request") != "" {
		s.renderCatalogue(w, r, http.StatusOK, "")
		return
	}
	http.Redirect(w, r, s.base+"/catalogue", http.StatusSeeOther)
}
