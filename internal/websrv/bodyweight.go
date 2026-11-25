package websrv

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/fullfran/training-mcp/internal/training"
)

// Session context in the app: Progreso › Cuerpo records one bodyweight a day
// and lists the recent ones; Historial puts their weekly mean beside each
// week's volume; a session's page edits its notes. None of it is needed to log
// a set, and none of it asks twice: Cuerpo's form only says whether today has
// a weight yet.

const (
	// bodyRecent is how many entries Cuerpo lists.
	bodyRecent = 14
	// bodyWeeks is how many Monday-to-Sunday weeks Historial compares.
	bodyWeeks = 8
)

// BodyStats is Cuerpo and Historial's bodyweight card.
type BodyStats struct {
	// Today is the form's default date; Logged is today's entry, if any.
	Today  string
	Logged *training.Bodyweight
	// Recent are the latest entries, newest first.
	Recent []training.Bodyweight
	// Trend is the sparkline of the last 90 days' entries.
	Trend   string
	Summary *training.BodyweightSummary
	// Weeks are the last weeks, newest first, each with its working volume and
	// mean bodyweight.
	Weeks []BodyWeek
	// Error says why the last entry was refused.
	Error string
}

// BodyWeek is one Monday-to-Sunday week. Entries is 0 when no bodyweight was
// recorded in it.
type BodyWeek struct {
	Label   string
	KG      float64
	AvgKG   float64
	Entries int
}

// bodyStats reads the last 90 days of bodyweight and lays the weeks beside the
// working volume already summed per date.
func (s *Server) bodyStats(ctx context.Context, svc *training.Service, today time.Time, kgByDate map[string]float64) (BodyStats, error) {
	out := BodyStats{Today: today.Format("2006-01-02")}
	h, err := svc.BodyweightHistory(ctx, "", out.Today)
	if err != nil {
		return out, err
	}
	out.Summary = h.Summary
	values := make([]float64, 0, len(h.Entries))
	for i := len(h.Entries) - 1; i >= 0; i-- {
		e := h.Entries[i]
		if e.Date == out.Today {
			out.Logged = &h.Entries[i]
		}
		if len(out.Recent) < bodyRecent {
			out.Recent = append(out.Recent, e)
		}
	}
	for _, e := range h.Entries {
		values = append(values, e.WeightKG)
	}
	out.Trend = sparkline(values)
	if len(h.Entries) == 0 {
		return out, nil
	}
	monday := today.AddDate(0, 0, -((int(today.Weekday()) + 6) % 7))
	for w := range bodyWeeks {
		start := monday.AddDate(0, 0, -7*w)
		week := BodyWeek{Label: shortDate(start.Format("2006-01-02"))}
		total := 0.0
		for d := range 7 {
			date := start.AddDate(0, 0, d).Format("2006-01-02")
			week.KG += kgByDate[date]
			for _, e := range h.Entries {
				if e.Date == date {
					total += e.WeightKG
					week.Entries++
				}
			}
		}
		if week.Entries > 0 {
			week.AvgKG = training.RoundWeight(total / float64(week.Entries))
		}
		out.Weeks = append(out.Weeks, week)
	}
	return out, nil
}

// logBodyweight records the weight typed in Cuerpo, replacing that day's
// entry. It answers htmx with Progreso again and anything else with a redirect
// there; a refused value comes back with the reason.
func (s *Server) logBodyweight(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.fail(w, err)
		return
	}
	in := training.BodyweightInput{
		Date: strings.TrimSpace(r.PostFormValue("date")),
		Unit: r.PostFormValue("weight_unit"),
		Note: strings.TrimSpace(r.PostFormValue("note")),
	}
	if in.Unit == "" {
		in.Unit = s.unitOf(r.Context())
	}
	weight, err := strconv.ParseFloat(strings.Replace(strings.TrimSpace(r.PostFormValue("weight")), ",", ".", 1), 64)
	switch {
	case err != nil:
		s.bodyRefused(w, r, "Escribe tu peso con un número.")
		return
	case in.Date != "" && !validFormDate(in.Date):
		s.bodyRefused(w, r, "La fecha no es válida.")
		return
	case utf8.RuneCountInString(in.Note) > training.MaxBodyweightNote:
		s.bodyRefused(w, r, fmt.Sprintf("La nota admite hasta %d caracteres.", training.MaxBodyweightNote))
		return
	}
	in.Weight = weight
	switch _, err := s.service(r.Context()).LogBodyweight(r.Context(), in); {
	case errors.Is(err, training.ErrValidation):
		s.bodyRefused(w, r, fmt.Sprintf("El peso tiene que estar entre %d y %d kg (%s y %s lb).", training.MinBodyweightKG, training.MaxBodyweightKG,
			formatNumber(training.FromKG(training.MinBodyweightKG, training.UnitLB)), formatNumber(training.FromKG(training.MaxBodyweightKG, training.UnitLB))))
		return
	case err != nil:
		s.fail(w, err)
		return
	}
	s.bodyDone(w, r, "")
}

// deleteBodyweight removes one day's entry from Cuerpo. A date with no entry,
// or not a date at all, is the same 404.
func (s *Server) deleteBodyweight(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.fail(w, err)
		return
	}
	switch err := s.service(r.Context()).DeleteBodyweight(r.Context(), strings.TrimSpace(r.PostFormValue("date"))); {
	case errors.Is(err, training.ErrNotFound), errors.Is(err, training.ErrValidation):
		http.Error(w, "Ese registro de peso ya no existe.", http.StatusNotFound)
		return
	case err != nil:
		s.fail(w, err)
		return
	}
	s.bodyDone(w, r, "")
}

// bodyRefused answers a refused entry: htmx gets Progreso with the reason in
// Cuerpo, a plain form a 400 with it.
func (s *Server) bodyRefused(w http.ResponseWriter, r *http.Request, message string) {
	if r.Header.Get("HX-Request") == "" {
		http.Error(w, message, http.StatusBadRequest)
		return
	}
	s.bodyDone(w, r, message)
}

func (s *Server) bodyDone(w http.ResponseWriter, r *http.Request, message string) {
	if r.Header.Get("HX-Request") == "" {
		http.Redirect(w, r, s.base+"/?modo=progreso", http.StatusSeeOther)
		return
	}
	stats, err := s.statsData(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	stats.Body.Error = message
	s.render(w, r, "progress-body", pageData{Base: s.base, Today: s.todayDate(), Stats: stats})
}

// saveSessionNotes stores the notes typed on a session's page and shows the
// page again. Another person's session is the same 404 as a missing one.
func (s *Server) saveSessionNotes(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.fail(w, err)
		return
	}
	switch _, err := s.service(r.Context()).SetSessionNotes(r.Context(), id, r.PostFormValue("notes")); {
	case errors.Is(err, training.ErrNotFound):
		http.NotFound(w, r)
		return
	case errors.Is(err, training.ErrValidation):
		s.sessionPage(w, r, id, http.StatusBadRequest, fmt.Sprintf("Las notas admiten hasta %d caracteres.", training.MaxSessionNotes))
		return
	case err != nil:
		s.fail(w, err)
		return
	}
	s.redirect(w, r, s.base+"/s/"+strconv.FormatInt(id, 10))
}

func validFormDate(v string) bool { _, err := time.Parse("2006-01-02", v); return err == nil }
