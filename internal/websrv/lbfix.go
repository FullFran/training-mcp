package websrv

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/fullfran/training-mcp/internal/training"
)

// LbFixData is the correction of past sets typed in lb but stored as kg: the
// scope chosen, its preview, and what was just corrected.
type LbFixData struct {
	Scope     training.LbScope
	Preview   *training.LbPreview
	Done      int64 // the proposal that recorded the correction just made
	DoneCount int
	Exercises []string
}

// lbFixPage previews a correction: ?session_id=, or ?exercise=&from=&to=.
// Without a scope it shows the form to choose one; ?hecho= confirms one made.
func (s *Server) lbFixPage(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	fix := LbFixData{}
	fix.Done, _ = strconv.ParseInt(q.Get("hecho"), 10, 64)
	fix.DoneCount, _ = strconv.Atoi(q.Get("n"))
	if v := q.Get("session_id"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id <= 0 {
			http.NotFound(w, r)
			return
		}
		fix.Scope.SessionID = id
	}
	fix.Scope.Exercise, fix.Scope.From, fix.Scope.To = q.Get("exercise"), q.Get("from"), q.Get("to")
	status, message := http.StatusOK, ""
	if fix.Scope.SessionID != 0 || fix.Scope.Exercise != "" {
		preview, err := s.service(r.Context()).LbCorrectionPreview(r.Context(), fix.Scope)
		switch {
		case errors.Is(err, training.ErrNotFound):
			http.NotFound(w, r)
			return
		case errors.Is(err, training.ErrValidation):
			status, message = http.StatusBadRequest, "Elige un ejercicio y las dos fechas, o abre una sesión."
		case err != nil:
			s.fail(w, err)
			return
		default:
			fix.Preview = &preview
		}
	}
	s.renderLbFix(w, r, status, message, fix)
}

func (s *Server) renderLbFix(w http.ResponseWriter, r *http.Request, status int, message string, fix LbFixData) {
	ctx := r.Context()
	names, err := s.service(ctx).ExerciseNames(ctx)
	if err != nil {
		s.fail(w, err)
		return
	}
	sort.Strings(names)
	fix.Exercises = names
	data := pageData{Base: s.base, Today: s.todayDate(), Error: message, LbFix: fix}
	if status != http.StatusOK {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
	}
	s.render(w, r, "lbfix.html", data)
}

// applyLbFix corrects the ticked sets as the person's own change: recorded as
// a proposal they decided at once, so it is in the history and can be undone.
func (s *Server) applyLbFix(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.fail(w, err)
		return
	}
	// Each ticked set comes with the stored kg its preview showed (old_<id>).
	var ids []int64
	previewed := map[int64]float64{}
	for _, v := range r.PostForm["set_ids"] {
		id, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err != nil || id <= 0 {
			http.NotFound(w, r)
			return
		}
		ids = append(ids, id)
		if old, err := strconv.ParseFloat(strings.TrimSpace(r.PostFormValue("old_"+strconv.FormatInt(id, 10))), 64); err == nil {
			previewed[id] = old
		}
	}
	if len(ids) == 0 {
		s.renderLbFix(w, r, http.StatusBadRequest, "Marca al menos una serie.", LbFixData{})
		return
	}
	ctx := r.Context()
	svc := s.service(ctx)
	// Checked before anything is recorded, so another person's ids and a set
	// already corrected leave no trace; the store checks again as it writes.
	preview, err := svc.LbCorrectionPreview(ctx, training.LbScope{SetIDs: ids})
	if err == nil && preview.Corrected > 0 {
		err = training.ErrLbCorrected
	}
	if err == nil {
		for _, set := range preview.Sets {
			if old, ok := previewed[set.SetID]; !ok || training.RoundWeight(old) != set.OldKG {
				err = training.ErrLbChanged
			}
		}
	}
	if err != nil {
		s.lbFixRefused(w, r, err)
		return
	}
	payload, err := json.Marshal(map[string][]int64{"set_ids": ids})
	if err != nil {
		s.fail(w, err)
		return
	}
	p, err := svc.ApplyAsPerson(ctx, training.ProposalInput{Tool: training.LbCorrectionTool, ActionType: training.LbCorrectionAction, Payload: payload},
		func(ctx context.Context) (json.RawMessage, error) {
			v, err := svc.CorrectLbSetsFrom(ctx, ids, previewed)
			if err != nil {
				return nil, err
			}
			return json.Marshal(v)
		})
	if err != nil {
		s.lbFixRefused(w, r, err)
		return
	}
	http.Redirect(w, r, s.base+"/units/fix?hecho="+strconv.FormatInt(p.ID, 10)+"&n="+strconv.Itoa(len(ids)), http.StatusSeeOther)
}

// lbFixRefused says why a correction was not made; nothing was changed.
func (s *Server) lbFixRefused(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, training.ErrLbCorrected):
		s.renderLbFix(w, r, http.StatusConflict, "Una de esas series ya estaba corregida: no se ha cambiado nada.", LbFixData{})
	case errors.Is(err, training.ErrLbChanged):
		s.renderLbFix(w, r, http.StatusConflict, "Una de esas series cambió desde la vista previa: vuelve a cargarla. No se ha cambiado nada.", LbFixData{})
	case errors.Is(err, training.ErrNotFound):
		http.NotFound(w, r)
	case errors.Is(err, training.ErrValidation):
		s.renderLbFix(w, r, http.StatusBadRequest, "No se pudo corregir: revisa las series marcadas.", LbFixData{})
	default:
		s.fail(w, err)
	}
}
