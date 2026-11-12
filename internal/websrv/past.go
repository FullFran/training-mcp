package websrv

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/fullfran/training-mcp/internal/training"
)

// pastSessionForm offers a session on an earlier date, for a workout that was
// never logged on the day, optionally following a routine.
func (s *Server) pastSessionForm(w http.ResponseWriter, r *http.Request) {
	s.renderPastForm(w, r, http.StatusOK, "", "")
}

func (s *Server) renderPastForm(w http.ResponseWriter, r *http.Request, status int, message, date string) {
	plans, err := s.service(r.Context()).ListPlans(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	today, err := time.Parse("2006-01-02", s.todayDate())
	if err != nil {
		s.fail(w, err)
		return
	}
	yesterday := today.AddDate(0, 0, -1).Format("2006-01-02")
	if date == "" {
		date = yesterday
	}
	if status != http.StatusOK {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
	}
	s.render(w, r, "past.html", pageData{Base: s.base, Today: yesterday, Session: training.Session{Date: date}, Plans: plans, Error: message})
}

// startPastSession creates a session on an earlier date with the same call
// start_session makes, then opens it for its sets. Today is refused, because
// today's session is the live workout; so is any later date.
func (s *Server) startPastSession(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.fail(w, err)
		return
	}
	date := strings.TrimSpace(r.PostFormValue("date"))
	today := s.todayDate()
	if _, err := time.Parse("2006-01-02", date); err != nil {
		s.renderPastForm(w, r, http.StatusBadRequest, "Elige una fecha válida.", "")
		return
	}
	// ISO dates compare as strings.
	switch {
	case date > today:
		s.renderPastForm(w, r, http.StatusBadRequest, "No puedes registrar una sesión en una fecha futura.", "")
		return
	case date == today:
		s.renderPastForm(w, r, http.StatusBadRequest, "Para hoy, registra el entreno desde Entrenar.", "")
		return
	}
	var planID int64
	if raw := r.PostFormValue("plan_id"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			s.renderPastForm(w, r, http.StatusBadRequest, "Esa rutina ya no existe.", date)
			return
		}
		planID = id
	}
	session, err := s.service(r.Context()).StartPlannedSession(r.Context(), date, planID)
	switch {
	case errors.Is(err, training.ErrNotFound):
		s.renderPastForm(w, r, http.StatusBadRequest, "Esa rutina ya no existe.", date)
		return
	case errors.Is(err, training.ErrValidation):
		s.renderPastForm(w, r, http.StatusBadRequest, "Elige una fecha válida.", "")
		return
	case err != nil:
		s.fail(w, err)
		return
	}
	s.backToEditor(w, r, session.ID)
}

// backToEditor reopens a session's set editor after a write.
func (s *Server) backToEditor(w http.ResponseWriter, r *http.Request, id int64) {
	to := s.base + "/s/" + strconv.FormatInt(id, 10) + "?editar=1"
	if r.Header.Get("HX-Request") != "" {
		w.Header().Set("HX-Redirect", to)
		return
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// ownedSession reads the session in the path. Another user's session gets
// what a missing one gets.
func (s *Server) ownedSession(w http.ResponseWriter, r *http.Request) (training.Session, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return training.Session{}, false
	}
	session, err := s.service(r.Context()).GetSession(r.Context(), id)
	if errors.Is(err, training.ErrNotFound) || errors.Is(err, training.ErrValidation) {
		http.NotFound(w, r)
		return session, false
	}
	if err != nil {
		s.fail(w, err)
		return session, false
	}
	if err := r.ParseForm(); err != nil {
		s.fail(w, err)
		return session, false
	}
	return session, true
}

// ownedSet is the set in the path, only when it belongs to the session in
// the path, so one session's URL never edits another's sets.
func (s *Server) ownedSet(w http.ResponseWriter, r *http.Request) (training.Session, int64, bool) {
	session, ok := s.ownedSession(w, r)
	if !ok {
		return session, 0, false
	}
	id, err := strconv.ParseInt(r.PathValue("set"), 10, 64)
	if err == nil {
		for _, set := range session.Sets {
			if set.ID == id {
				return session, id, true
			}
		}
	}
	http.NotFound(w, r)
	return session, 0, false
}

const badSetMessage = "Datos inválidos: revisa ejercicio, peso, reps y RPE (1-10)."

// addPastSet logs a set into the session in the path, on that session's date,
// with the same call add_set makes.
func (s *Server) addPastSet(w http.ResponseWriter, r *http.Request) {
	session, ok := s.ownedSession(w, r)
	if !ok {
		return
	}
	weight, errWeight := formWeightKG(r)
	reps, errReps := strconv.Atoi(strings.TrimSpace(r.PostFormValue("reps")))
	rpe, errRPE := strconv.ParseFloat(strings.TrimSpace(r.PostFormValue("rpe")), 64)
	if errWeight != nil || errReps != nil || errRPE != nil {
		s.renderSession(w, r, session.ID, http.StatusBadRequest, badSetMessage, true)
		return
	}
	_, _, err := s.service(r.Context()).AddSet(r.Context(), training.AddSetInput{
		SessionID: session.ID, Exercise: r.PostFormValue("exercise"), WeightKG: weight,
		Reps: reps, RPE: rpe, Category: lastFormValue(r, "category"),
	})
	switch {
	case errors.Is(err, training.ErrValidation):
		s.renderSession(w, r, session.ID, http.StatusBadRequest, badSetMessage, true)
		return
	case err != nil:
		s.fail(w, err)
		return
	}
	s.backToEditor(w, r, session.ID)
}

// updatePastSet corrects a set of the session in the path; blank fields stay.
func (s *Server) updatePastSet(w http.ResponseWriter, r *http.Request) {
	session, id, ok := s.ownedSet(w, r)
	if !ok {
		return
	}
	patch := training.SetPatch{}
	bad := false
	if v := strings.TrimSpace(r.PostFormValue("weight_kg")); v != "" && !weightAsShown(r) {
		f, err := formWeightKG(r)
		patch.WeightKG, bad = &f, bad || err != nil
	}
	if v := strings.TrimSpace(r.PostFormValue("reps")); v != "" {
		n, err := strconv.Atoi(v)
		patch.Reps, bad = &n, bad || err != nil
	}
	if v := strings.TrimSpace(r.PostFormValue("rpe")); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		patch.RPE, bad = &f, bad || err != nil
	}
	// A hidden "working" precedes the warm-up checkbox, as in the live editor.
	if r.Form.Has("category") {
		v := lastFormValue(r, "category")
		patch.Category = &v
	}
	if !bad {
		_, _, err := s.service(r.Context()).UpdateSet(r.Context(), id, patch)
		switch {
		case errors.Is(err, training.ErrValidation):
			bad = true
		case err != nil:
			s.fail(w, err)
			return
		}
	}
	if bad {
		s.renderSession(w, r, session.ID, http.StatusBadRequest, badSetMessage, true)
		return
	}
	s.backToEditor(w, r, session.ID)
}

// deletePastSet removes a set of the session in the path.
func (s *Server) deletePastSet(w http.ResponseWriter, r *http.Request) {
	session, id, ok := s.ownedSet(w, r)
	if !ok {
		return
	}
	if _, _, _, err := s.service(r.Context()).DeleteSet(r.Context(), id); err != nil && !errors.Is(err, training.ErrNotFound) {
		s.fail(w, err)
		return
	}
	s.backToEditor(w, r, session.ID)
}
