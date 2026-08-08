// Package websrv is a driving adapter that exposes the training service as an
// installable PWA. It holds no domain logic: validation, SI calculation and
// exercise normalization all stay in training.Service, so the web UI and the
// MCP tools can never disagree.
package websrv

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"html/template"
	"io/fs"
	"math"
	"net/http"
	"strconv"
	"strings"
	texttemplate "text/template"

	"github.com/fullfran/training-mcp/internal/training"
)

//go:embed templates
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

// recentExerciseLimit caps the quick-pick chips. More than this and the chip
// row stops being a shortcut and becomes a list to read.
const recentExerciseLimit = 8

type Server struct {
	service *training.Service
	clock   training.Clock
	base    string
	tpl     *template.Template
	// txt renders the manifest and the service worker. They are JSON and
	// JavaScript documents, so html/template's contextual escaping would be
	// wrong for them.
	txt *texttemplate.Template
	mux *http.ServeMux
}

// ExerciseBlock groups a session's sets by exercise, the way a workout is
// actually performed and read back, instead of one flat chronological list.
type ExerciseBlock struct {
	Exercise    string
	MuscleGroup string
	Sets        []training.Set
	TotalSI     float64
	// BestE1RM is the all-time record for this exercise, used to flag a set
	// performed today that matches it.
	BestE1RM float64
}

// ExerciseInfo is what the entry form shows about the exercise being logged:
// what was done last time and the standing record.
type ExerciseInfo struct {
	Last     *training.ExerciseSet `json:"last,omitempty"`
	BestE1RM float64               `json:"best_e1rm,omitempty"`
	Group    string                `json:"group,omitempty"`
}

type pageData struct {
	Base     string
	Today    string
	Session  training.Session
	Recent   []training.ExerciseMemory
	History  []training.SessionSummary
	Volume   []training.GroupVolume
	Blocks   []ExerciseBlock
	Plans    []training.Plan
	Progress []training.PlanProgress
	// InfoJSON is a name -> ExerciseInfo map the client uses to show "last
	// time" and the record without another round trip.
	InfoJSON template.JS
	// ReadOnly renders a past session without the editing controls.
	ReadOnly bool
	Error    string
}

// LastSet returns the final set of the session, used to power "repeat last set".
func (p pageData) LastSet() *training.Set {
	if len(p.Session.Sets) == 0 {
		return nil
	}
	return &p.Session.Sets[len(p.Session.Sets)-1]
}

// IsRecord reports whether a set matches the exercise's all-time best, so it can
// be badged. Compared with a small tolerance because both sides are rounded.
func (b ExerciseBlock) IsRecord(s training.Set) bool {
	return b.BestE1RM > 0 && math.Abs(training.Epley1RM(s.WeightKG, s.Reps)-b.BestE1RM) < 0.05
}

func New(service *training.Service, clock training.Clock, basePath string) (*Server, error) {
	funcs := map[string]any{
		"num":      formatNumber,
		"rpeScale": rpeScale,
		"add":      func(a, b int) int { return a + b },
		"doneCount": func(items []training.PlanProgress) int {
			n := 0
			for _, it := range items {
				if it.Done() {
					n++
				}
			}
			return n
		},
		// targetReps prefills the middle of the prescribed range, the sensible
		// first attempt when the plan says "10 to 12".
		"targetReps": func(p training.PlanProgress) int {
			switch {
			case p.RepMin > 0 && p.RepMax > 0:
				return (p.RepMin + p.RepMax) / 2
			case p.RepMin > 0:
				return p.RepMin
			default:
				return 0
			}
		},
		"pct": func(v, max float64) string {
			if max <= 0 {
				return "0"
			}
			return strconv.FormatFloat(math.Round(v/max*1000)/10, 'f', -1, 64)
		},
	}
	tpl, err := template.New("").Funcs(funcs).ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	txt, err := texttemplate.New("").Funcs(funcs).ParseFS(templateFS, "templates/*.tmpl")
	if err != nil {
		return nil, err
	}
	s := &Server{
		service: service, clock: clock, base: strings.TrimSuffix(basePath, "/"),
		tpl: tpl, txt: txt, mux: http.NewServeMux(),
	}
	s.routes()
	return s, nil
}

// rpeScale is the half-point RPE range offered as tap targets. Below 6 the SI
// contribution is negligible and the extra buttons only slow entry down.
func rpeScale() []string {
	return []string{"6", "6.5", "7", "7.5", "8", "8.5", "9", "9.5", "10"}
}

func (s *Server) Handler() http.Handler { return s.mux }

// BasePath is the public prefix the PWA is served from.
func (s *Server) BasePath() string { return s.base }

func (s *Server) routes() {
	b := s.base
	s.mux.HandleFunc("GET "+b+"/{$}", s.today)
	s.mux.HandleFunc("POST "+b+"/sets", s.addSet)
	s.mux.HandleFunc("POST "+b+"/sets/{id}/delete", s.deleteSet)
	s.mux.HandleFunc("POST "+b+"/sets/{id}/update", s.updateSet)
	s.mux.HandleFunc("GET "+b+"/history", s.history)
	s.mux.HandleFunc("POST "+b+"/plan", s.choosePlan)
	s.mux.HandleFunc("POST "+b+"/plan/item", s.adjustItem)
	s.mux.HandleFunc("GET "+b+"/s/{id}", s.session)
	s.mux.HandleFunc("GET "+b+"/manifest.webmanifest", s.manifest)
	s.mux.HandleFunc("GET "+b+"/sw.js", s.serviceWorker)

	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err)
	}
	s.mux.Handle("GET "+b+"/static/", http.StripPrefix(b+"/static/", cacheStatic(http.FileServerFS(sub))))

	// Bare prefix with no trailing slash: send it to the app root so a typed or
	// shared URL still lands somewhere useful.
	if b != "" {
		s.mux.HandleFunc("GET "+b, func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, b+"/", http.StatusMovedPermanently)
		})
	}
}

func (s *Server) today(w http.ResponseWriter, r *http.Request) {
	data, err := s.todayData(r.Context(), "")
	if err != nil {
		s.fail(w, err)
		return
	}
	s.render(w, "index.html", data)
}

func (s *Server) addSet(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.fail(w, err)
		return
	}
	weight, errWeight := strconv.ParseFloat(strings.TrimSpace(r.PostFormValue("weight_kg")), 64)
	reps, errReps := strconv.Atoi(strings.TrimSpace(r.PostFormValue("reps")))
	rpe, errRPE := strconv.ParseFloat(strings.TrimSpace(r.PostFormValue("rpe")), 64)
	exercise := r.PostFormValue("exercise")

	message := ""
	if errWeight != nil || errReps != nil || errRPE != nil {
		message = "Revisa peso, reps y RPE."
	} else {
		session, err := s.ensureToday(r.Context())
		if err != nil {
			s.fail(w, err)
			return
		}
		_, _, err = s.service.AddSet(r.Context(), training.AddSetInput{
			SessionID: session.ID, Exercise: exercise, WeightKG: weight, Reps: reps, RPE: rpe,
		})
		switch {
		case errors.Is(err, training.ErrValidation):
			message = "Datos inválidos: revisa ejercicio, peso, reps y RPE (1-10)."
		case err != nil:
			s.fail(w, err)
			return
		}
	}
	s.renderPanel(w, r, message)
}

// updateSet edits an already logged set in place, so a mistyped weight does not
// have to be deleted and re-entered.
func (s *Server) updateSet(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.renderPanel(w, r, "No se pudo identificar la serie.")
		return
	}
	if err := r.ParseForm(); err != nil {
		s.fail(w, err)
		return
	}
	patch := training.SetPatch{}
	message := ""
	if v := strings.TrimSpace(r.PostFormValue("weight_kg")); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			patch.WeightKG = &f
		} else {
			message = "Peso inválido."
		}
	}
	if v := strings.TrimSpace(r.PostFormValue("reps")); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			patch.Reps = &n
		} else {
			message = "Reps inválidas."
		}
	}
	if v := strings.TrimSpace(r.PostFormValue("rpe")); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			patch.RPE = &f
		} else {
			message = "RPE inválido."
		}
	}
	if message == "" {
		switch _, _, err := s.service.UpdateSet(r.Context(), id, patch); {
		case errors.Is(err, training.ErrNotFound):
			message = "Esa serie ya no existe."
		case errors.Is(err, training.ErrValidation):
			message = "Datos inválidos: revisa peso, reps y RPE (1-10)."
		case err != nil:
			s.fail(w, err)
			return
		}
	}
	s.renderPanel(w, r, message)
}

func (s *Server) deleteSet(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.renderPanel(w, r, "No se pudo identificar la serie.")
		return
	}
	message := ""
	switch _, _, _, err := s.service.DeleteSet(r.Context(), id); {
	case errors.Is(err, training.ErrNotFound):
		message = "Esa serie ya no existe."
	case errors.Is(err, training.ErrValidation):
		message = "Identificador de serie inválido."
	case err != nil:
		s.fail(w, err)
		return
	}
	s.renderPanel(w, r, message)
}

// renderPanel returns just the session panel so htmx can swap it without
// touching the entry form, which keeps the previous values ready for the next
// set. Falls back to a full page render for non-htmx clients.
func (s *Server) renderPanel(w http.ResponseWriter, r *http.Request, message string) {
	data, err := s.todayData(r.Context(), message)
	if err != nil {
		s.fail(w, err)
		return
	}
	if r.Header.Get("HX-Request") == "" {
		http.Redirect(w, r, s.base+"/", http.StatusSeeOther)
		return
	}
	s.render(w, "panel.html", data)
}

func (s *Server) history(w http.ResponseWriter, r *http.Request) {
	sessions, err := s.service.ListSessions(r.Context(), training.ListFilter{Limit: 60})
	if err != nil {
		s.fail(w, err)
		return
	}
	volume, err := s.service.VolumeByGroup(r.Context(), training.ListFilter{})
	if err != nil {
		s.fail(w, err)
		return
	}
	s.render(w, "history.html", pageData{Base: s.base, Today: s.todayDate(), History: sessions, Volume: volume})
}

func (s *Server) session(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	session, err := s.service.GetSession(r.Context(), id)
	if errors.Is(err, training.ErrNotFound) || errors.Is(err, training.ErrValidation) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	data := pageData{Base: s.base, Today: session.Date, Session: session, ReadOnly: true}
	if data.Progress, err = s.service.SessionProgress(r.Context(), session.ID); err != nil {
		s.fail(w, err)
		return
	}
	if err := s.decorate(r.Context(), &data); err != nil {
		s.fail(w, err)
		return
	}
	data.Today = s.todayDate()
	s.render(w, "session.html", data)
}

func (s *Server) manifest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/manifest+json")
	s.txt.ExecuteTemplate(w, "manifest.json.tmpl", pageData{Base: s.base})
}

func (s *Server) serviceWorker(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/javascript")
	// The worker must not be cached or clients would keep an outdated shell.
	w.Header().Set("Cache-Control", "no-cache")
	s.txt.ExecuteTemplate(w, "sw.js.tmpl", pageData{Base: s.base})
}

func (s *Server) todayData(ctx context.Context, message string) (pageData, error) {
	today := s.todayDate()
	data := pageData{Base: s.base, Today: today, Error: message}

	sessions, err := s.service.ListSessions(ctx, training.ListFilter{From: today, To: today, Limit: 1})
	if err != nil {
		return data, err
	}
	if len(sessions) > 0 {
		if data.Session, err = s.service.GetSession(ctx, sessions[0].ID); err != nil {
			return data, err
		}
	}
	if data.Recent, err = s.service.RecentExercises(ctx, recentExerciseLimit); err != nil {
		return data, err
	}
	if data.Plans, err = s.service.ListPlans(ctx); err != nil {
		return data, err
	}
	if data.Session.ID > 0 && data.Session.PlanID > 0 {
		if data.Progress, err = s.service.SessionProgress(ctx, data.Session.ID); err != nil {
			return data, err
		}
	}
	if err = s.decorate(ctx, &data); err != nil {
		return data, err
	}
	return data, nil
}

// choosePlan starts today's session from a plan. It refuses once sets exist,
// because the plan is snapshotted at session start and switching afterwards
// would silently reinterpret work already logged.
func (s *Server) choosePlan(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.fail(w, err)
		return
	}
	planID, err := strconv.ParseInt(strings.TrimSpace(r.PostFormValue("plan_id")), 10, 64)
	if err != nil || planID <= 0 {
		http.Redirect(w, r, s.base+"/", http.StatusSeeOther)
		return
	}
	today := s.todayDate()
	sessions, err := s.service.ListSessions(r.Context(), training.ListFilter{From: today, To: today, Limit: 1})
	if err != nil {
		s.fail(w, err)
		return
	}
	if len(sessions) > 0 {
		if sessions[0].SetCount == 0 {
			// Nothing logged yet: replace the empty session outright.
			if _, err := s.service.DeleteSession(r.Context(), sessions[0].ID); err != nil {
				s.fail(w, err)
				return
			}
		} else {
			// Work already logged. The plan is a per-session snapshot and sets
			// are never touched, so adopting another plan mid-session is safe:
			// its exercises are added and anything already done that the new
			// plan does not include simply shows as off-plan.
			plan, err := s.service.GetPlan(r.Context(), planID)
			if err != nil {
				s.fail(w, err)
				return
			}
			for _, it := range plan.Items {
				if err := s.service.SetSessionItem(r.Context(), sessions[0].ID, it); err != nil {
					s.fail(w, err)
					return
				}
			}
			http.Redirect(w, r, s.base+"/", http.StatusSeeOther)
			return
		}
	}
	if _, err := s.service.StartPlannedSession(r.Context(), today, planID); err != nil {
		s.fail(w, err)
		return
	}
	http.Redirect(w, r, s.base+"/", http.StatusSeeOther)
}

// adjustItem applies one in-session change: more or fewer sets, skip, swap for
// another movement, or adopt an off-plan exercise into today's plan.
func (s *Server) adjustItem(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.fail(w, err)
		return
	}
	exercise := r.PostFormValue("exercise")
	action := r.PostFormValue("action")
	session, err := s.ensureToday(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}

	message := ""
	switch action {
	case "sets":
		delta, convErr := strconv.Atoi(r.PostFormValue("delta"))
		current, _ := strconv.Atoi(r.PostFormValue("current"))
		next := current + delta
		if convErr != nil || next < 1 {
			message = "No se puede bajar de una serie."
			break
		}
		err = s.service.AdjustSessionItem(r.Context(), session.ID, exercise, training.SessionItemPatch{TargetSets: &next})
	case "skip", "unskip":
		skipped := action == "skip"
		err = s.service.AdjustSessionItem(r.Context(), session.ID, exercise, training.SessionItemPatch{Skipped: &skipped})
	case "swap":
		err = s.service.SwapSessionItem(r.Context(), session.ID, exercise, r.PostFormValue("replacement"))
	case "adopt":
		// An exercise done off-plan becomes part of today's plan at the volume
		// already performed.
		done, _ := strconv.Atoi(r.PostFormValue("done"))
		if done < 1 {
			done = 1
		}
		err = s.service.SetSessionItem(r.Context(), session.ID, training.PlanItem{Exercise: exercise, TargetSets: done})
	case "remove":
		err = s.service.RemoveSessionItem(r.Context(), session.ID, exercise)
	default:
		message = "Acción desconocida."
	}
	switch {
	case errors.Is(err, training.ErrNotFound):
		message = "Ese ejercicio ya no está en el plan de hoy."
	case errors.Is(err, training.ErrValidation):
		message = "Ajuste inválido."
	case err != nil:
		s.fail(w, err)
		return
	}
	s.renderPanel(w, r, message)
}

// decorate builds the per-exercise blocks and the "last time / record" map. It
// queries history once per distinct exercise in play, which is a handful per
// session, not once per set.
func (s *Server) decorate(ctx context.Context, data *pageData) error {
	names := []string{}
	seen := map[string]bool{}
	for _, set := range data.Session.Sets {
		if !seen[set.Exercise] {
			seen[set.Exercise] = true
			names = append(names, set.Exercise)
		}
	}
	for _, r := range data.Recent {
		if !seen[r.Exercise] {
			seen[r.Exercise] = true
			names = append(names, r.Exercise)
		}
	}

	info := map[string]ExerciseInfo{}
	for _, name := range names {
		h, err := s.service.ExerciseHistory(ctx, name, 200)
		if err != nil {
			return err
		}
		entry := ExerciseInfo{Group: h.MuscleGroup}
		if h.Best != nil {
			entry.BestE1RM = h.Best.Est1RM
		}
		// "Last time" must skip today, or the form would echo the set just logged.
		for i := range h.Sets {
			if h.Sets[i].Date != data.Today {
				entry.Last = &h.Sets[i]
				break
			}
		}
		info[name] = entry
	}

	byName := map[string]int{}
	for _, set := range data.Session.Sets {
		idx, ok := byName[set.Exercise]
		if !ok {
			data.Blocks = append(data.Blocks, ExerciseBlock{
				Exercise:    set.Exercise,
				MuscleGroup: info[set.Exercise].Group,
				BestE1RM:    info[set.Exercise].BestE1RM,
			})
			idx = len(data.Blocks) - 1
			byName[set.Exercise] = idx
		}
		data.Blocks[idx].Sets = append(data.Blocks[idx].Sets, set)
		data.Blocks[idx].TotalSI = training.NormalizeSI(data.Blocks[idx].TotalSI + set.SI)
	}

	encoded, err := json.Marshal(info)
	if err != nil {
		return err
	}
	data.InfoJSON = template.JS(encoded)
	return nil
}

// ensureToday finds today's session or creates it. Creating lazily on the first
// set means opening the app never leaves behind an empty session.
func (s *Server) ensureToday(ctx context.Context) (training.Session, error) {
	today := s.todayDate()
	sessions, err := s.service.ListSessions(ctx, training.ListFilter{From: today, To: today, Limit: 1})
	if err != nil {
		return training.Session{}, err
	}
	if len(sessions) > 0 {
		return training.Session{ID: sessions[0].ID, Date: sessions[0].Date}, nil
	}
	return s.service.StartSession(ctx, today)
}

func (s *Server) todayDate() string { return s.clock().Format("2006-01-02") }

func (s *Server) render(w http.ResponseWriter, name string, data pageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	s.renderTo(w, name, data)
}

func (s *Server) renderTo(w http.ResponseWriter, name string, data pageData) {
	if err := s.tpl.ExecuteTemplate(w, name, data); err != nil {
		// The response is already partially written, so the status cannot be
		// changed. Nothing useful is left to do beyond stopping here.
		return
	}
}

func (s *Server) fail(w http.ResponseWriter, _ error) {
	http.Error(w, "internal error", http.StatusInternalServerError)
}

func cacheStatic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=604800")
		next.ServeHTTP(w, r)
	})
}

// formatNumber trims trailing zeros so 36.0 renders as "36" and 2.5 stays "2.5".
func formatNumber(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}
