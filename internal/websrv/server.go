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
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	texttemplate "text/template"
	"time"

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
	service  training.ServiceFor
	snapshot func(context.Context, string) error
	clock    training.Clock
	base     string
	opts     Options
	tpl      *template.Template
	// txt renders the manifest and the service worker. They are JSON and
	// JavaScript documents, so html/template's contextual escaping would be
	// wrong for them.
	txt *texttemplate.Template
	mux *http.ServeMux
	// patterns lists what is registered on mux, which cannot list itself.
	patterns []string
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
	// Note is the persistent setup reminder: seat height, grip, pin.
	Note string `json:"note,omitempty"`
	// Suggest is the advised next working weight, with its reasoning.
	Suggest *training.LoadSuggestion `json:"suggest,omitempty"`
}

type pageData struct {
	Base      string
	Today     string
	Session   training.Session
	Recent    []training.ExerciseMemory
	History   []training.SessionSummary
	Volume    []training.GroupVolume
	Blocks    []ExerciseBlock
	Plans     []training.Plan
	Progress  []training.PlanProgress
	PlanNotes string
	// Catalogue is every exercise ever logged, offered as autocomplete. Picking
	// an existing name is what stops the catalogue fragmenting into near
	// duplicates like "remo maquina" beside "remo máquina".
	Catalogue []string
	// TrainedGroups are the muscle groups worked today, the only ones worth
	// rating. Feedback stays a few taps by never asking about the rest.
	TrainedGroups []string
	Feedback      map[string]training.Feedback
	Recommend     []training.SetChange
	// InfoJSON is a name -> ExerciseInfo map the client uses to show "last
	// time" and the record without another round trip.
	InfoJSON template.JS
	// ReadOnly renders a past session without the editing controls.
	ReadOnly bool
	Error    string
	// CSRF is the session's token, empty where the mount has no sessions.
	CSRF string
	// CanExport shows the backup link where /export.db is served.
	CanExport bool
	// RestTarget is the person's rest between sets in seconds, shared with the
	// agent. Rest and Next are the live state the agent also reads, so the
	// phone's timer and the coach never disagree.
	RestTarget int
	Rest       training.Rest
	Next       *training.PlanProgress
	LastSetID  int64
	// Timed says today's session has a stored start; Elapsed is the seconds
	// since then by the server's clock, so every device shows one duration.
	Timed   bool
	Elapsed int
	// Review is the review page: proposals waiting and answered, and the standing answers.
	Review ReviewData
	// Memory is the memory page: what the coach remembers about the person.
	Memory MemoryData
	// Exercises is the catalogue page: every exercise with its group and note.
	Exercises CatalogueData
	// Landmarks is every muscle group with its recorded MEV and MRV, 0 if unknown.
	Landmarks []training.Landmarks
	// Import is the import page: earlier imports and a file's preview.
	Import ImportData
	// CoachSees is Planificar's context card: what the coach reads.
	CoachSees *CoachSees
	// PendingCount is how many agent proposals wait for an answer.
	PendingCount int
	// CoachOn shows the coach chat.
	CoachOn bool
	// ConnectorsOn links the connectors panel and shows connector approvals.
	ConnectorsOn bool
	// Mode is the screen the page opens on, from ?modo=: "home" (Entrenar's
	// tab), "train" (the active workout layer), "plan" (Planificar),
	// "progress" (Progreso) or "you" (Tú). The page holds them all and
	// switches between them in place.
	Mode string
	// Home is Entrenar's tab: the next routine, the others and the last session.
	Home HomeData
	// Stats is Progreso's tab, filled when the page opens on it or when the
	// tab loads it from /progress; StatsReady says which.
	Stats      StatsData
	StatsReady bool
	// Exercise is one exercise's pushed screen from Progreso.
	Exercise ExerciseDetail
	// Cards are today's exercises on the training screen, each with its set
	// table pre-filled.
	Cards []TrainCard
	// Records are today's sets that match their exercise's best estimated 1RM.
	Records map[int64]bool
}

// LastSet returns the session's final working set, used to power "repeat last
// set": repeating a warm-up would log its light weight as a working set.
func (p pageData) LastSet() *training.Set {
	for i := len(p.Session.Sets) - 1; i >= 0; i-- {
		if p.Session.Sets[i].Category != training.SetWarmup {
			return &p.Session.Sets[i]
		}
	}
	return nil
}

// WorkingSetCount is how many sets count today; warm-ups do not.
func (p pageData) WorkingSetCount() int {
	n := 0
	for _, set := range p.Session.Sets {
		if set.Category != training.SetWarmup {
			n++
		}
	}
	return n
}

// IsRecord reports whether a set matches the exercise's all-time best, so it can
// be badged. Compared with a small tolerance because both sides are rounded.
func (b ExerciseBlock) IsRecord(s training.Set) bool {
	return b.BestE1RM > 0 && math.Abs(training.Epley1RM(s.WeightKG, s.Reps)-b.BestE1RM) < 0.05
}

// WorkingCount is how many of the block's sets count; warm-ups do not.
func (b ExerciseBlock) WorkingCount() int {
	n := 0
	for _, set := range b.Sets {
		if set.Category != training.SetWarmup {
			n++
		}
	}
	return n
}

// Top is the block's best working set by estimated 1RM, the line the finish
// summary shows for each exercise. Nil for a block with only warm-ups.
func (b ExerciseBlock) Top() *training.Set {
	var top *training.Set
	for i := range b.Sets {
		if b.Sets[i].Category == training.SetWarmup {
			continue
		}
		if top == nil || training.Epley1RM(b.Sets[i].WeightKG, b.Sets[i].Reps) > training.Epley1RM(top.WeightKG, top.Reps) {
			top = &b.Sets[i]
		}
	}
	return top
}

// Options are what differs between the ways the app can be served.
type Options struct {
	// Export serves /export.db, a copy of the whole database with every user's
	// data. The root never sets it; no production mount does since the secret
	// mount was retired, and backups snapshot the data volume instead.
	Export bool
	// CSRFToken returns the token a request's session must send back with every
	// write. Nil means the mount has no sessions and the pages carry no token.
	CSRFToken func(*http.Request) string
	// Apply applies an accepted proposal through its MCP tool's code. Nil means
	// proposals can be listed and rejected here but not accepted.
	Apply training.Applier
	// Coach is the in-app coach. Nil leaves the app exactly as it is without it:
	// no chat, no coach routes.
	Coach Coach
	// Connectors are the person's own remote MCP servers. Used only with a
	// Coach; nil serves no connector routes.
	Connectors Connectors
	// Logger records failed memory hooks. Nil: the standard logger.
	Logger *log.Logger
}

// New serves the app under basePath with the database export on.
func New(service training.ServiceFor, snapshot func(context.Context, string) error, clock training.Clock, basePath string) (*Server, error) {
	return NewWithOptions(service, snapshot, clock, basePath, Options{Export: true})
}

func NewWithOptions(service training.ServiceFor, snapshot func(context.Context, string) error, clock training.Clock, basePath string, opts Options) (*Server, error) {
	funcs := map[string]any{
		"num":         formatNumber,
		"clock":       clockText,
		"kilos":       kilos,
		"restClock":   restClock,
		"shortDate":   shortDate,
		"rpeScale":    rpeScale,
		"techniques":  func() []string { return training.Techniques },
		"ratingScale": func() []int { return []int{0, 1, 2, 3} },
		"ratingDims": func() [][2]string {
			return [][2]string{
				{"fatigue", "fatiga"},
				{"pump", "bombeo"},
				{"recovery", "agujetas"},
			}
		},
		"fbValue": func(f training.Feedback, dim string) int {
			switch dim {
			case "fatigue":
				return f.Fatigue
			case "pump":
				return f.Pump
			default:
				return f.Recovery
			}
		},
		"add": func(a, b int) int { return a + b },
		// rowData hands one row still to log to the set-row template.
		"rowData": func(base, exercise string, row PendingRow) setRowData {
			return setRowData{Base: base, Exercise: exercise, Row: row}
		},
		"countOf": countOf,
		"doneCount": func(items []training.PlanProgress) int {
			n := 0
			for _, it := range items {
				if it.Done() {
					n++
				}
			}
			return n
		},
		// todayWeight is the weight of today's latest set of an exercise, which
		// the one-tap log offers next: "last time" skips today on purpose, so
		// without it the form would offer an older session's weight after heavy
		// sets. Empty when the exercise has no set today.
		"todayWeight": func(session training.Session, exercise string) string {
			var last *training.Set
			for i := range session.Sets {
				set := &session.Sets[i]
				if set.Exercise == exercise && set.Category != training.SetWarmup && (last == nil || set.ID > last.ID) {
					last = set
				}
			}
			if last == nil {
				return ""
			}
			return formatNumber(last.WeightKG)
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
		// rx is a prescription in one short line: "8-10 reps @8".
		"rx": func(p training.PlanProgress) string {
			var b strings.Builder
			if p.RepMin > 0 {
				b.WriteString(strconv.Itoa(p.RepMin))
				if p.RepMax > p.RepMin {
					b.WriteString("-" + strconv.Itoa(p.RepMax))
				}
				b.WriteString(" reps")
			}
			if p.TargetRPE > 0 {
				if b.Len() > 0 {
					b.WriteString(" ")
				}
				b.WriteString("@" + formatNumber(p.TargetRPE))
			}
			return b.String()
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
		service: service, snapshot: snapshot, clock: clock, base: strings.TrimSuffix(basePath, "/"), opts: opts,
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

// Routes lists every registered pattern, so a test can require a case for each.
func (s *Server) Routes() []string { return slices.Clone(s.patterns) }

func (s *Server) routes() {
	b := s.base
	s.handle("GET "+b+"/{$}", s.today)
	s.handle("POST "+b+"/sets", s.addSet)
	s.handle("POST "+b+"/sets/{id}/delete", s.deleteSet)
	s.handle("POST "+b+"/sets/{id}/update", s.updateSet)
	s.handle("GET "+b+"/history", s.historyMoved)
	s.handle("GET "+b+"/progress", s.progress)
	s.handle("GET "+b+"/progress/exercise", s.exerciseDetail)
	s.handle("GET "+b+"/exercises", s.exercises)
	s.handle("GET "+b+"/catalogue", s.cataloguePage)
	s.handle("POST "+b+"/catalogue", s.saveCatalogueEntry)
	if s.opts.Export {
		s.handle("GET "+b+"/export.db", s.export)
	}
	s.handle("GET "+b+"/plans", s.plansPage)
	s.handle("POST "+b+"/plans/create", s.planAction)
	s.handle("POST "+b+"/plans/update", s.planAction)
	s.handle("POST "+b+"/plans/delete", s.planAction)
	s.handle("POST "+b+"/plans/item", s.planAction)
	s.handle("POST "+b+"/plans/item/remove", s.planAction)
	s.handle("POST "+b+"/plans/move", s.planAction)
	s.handle("POST "+b+"/plan", s.choosePlan)
	s.handle("POST "+b+"/plan/item", s.adjustItem)
	s.handle("POST "+b+"/feedback", s.recordFeedback)
	s.handle("GET "+b+"/landmarks", s.landmarksPage)
	s.handle("POST "+b+"/landmarks", s.setLandmarks)
	s.handle("POST "+b+"/note", s.saveNote)
	s.handle("POST "+b+"/rest", s.setRest)
	s.handle("POST "+b+"/finish", s.finishWorkout)
	s.handle("GET "+b+"/exercise-info", s.exerciseInfo)
	s.handle("GET "+b+"/s/{id}", s.session)
	s.handle("POST "+b+"/s/{id}/plan", s.saveSessionAsPlan)
	s.handle("POST "+b+"/s/{id}/delete", s.deleteSession)
	s.handle("GET "+b+"/review", s.reviewPage)
	s.handle("POST "+b+"/review/{id}/accept", s.acceptProposal)
	s.handle("POST "+b+"/review/{id}/reject", s.rejectProposal)
	s.handle("POST "+b+"/review/accept-all", s.setAcceptAll)
	s.handle("POST "+b+"/review/forget", s.forgetRule)
	s.handle("POST "+b+"/review/{id}/undo", s.undoProposal)
	s.handle("GET "+b+"/review/undoable", s.undoableProposals)
	s.handle("GET "+b+"/memory", s.memoryPage)
	s.handle("POST "+b+"/memory/{id}/update", s.updateMemory)
	s.handle("POST "+b+"/memory/{id}/delete", s.deleteMemory)
	s.handle("POST "+b+"/memory/{id}/confirm", s.confirmMemory)
	s.handle("POST "+b+"/memory/conflicts/{id}/resolve", s.resolveConflict)
	s.handle("POST "+b+"/memory/delete-all", s.deleteAllMemory)
	s.handle("GET "+b+"/import", s.importPage)
	s.handle("POST "+b+"/import/preview", s.previewImport)
	s.handle("POST "+b+"/import/apply", s.applyImport)
	s.handle("GET "+b+"/manifest.webmanifest", s.manifest)
	s.handle("GET "+b+"/sw.js", s.serviceWorker)

	s.coachRoutes()

	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err)
	}
	s.handle("GET "+b+"/static/", http.StripPrefix(b+"/static/", cacheStatic(http.FileServerFS(sub))).ServeHTTP)

	// Bare prefix with no trailing slash: send it to the app root so a typed or
	// shared URL still lands somewhere useful.
	if b != "" {
		s.handle("GET "+b, func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, b+"/", http.StatusMovedPermanently)
		})
	}
}

func (s *Server) handle(pattern string, handler http.HandlerFunc) {
	s.patterns = append(s.patterns, pattern)
	s.mux.HandleFunc(pattern, handler)
}

func (s *Server) today(w http.ResponseWriter, r *http.Request) {
	s.proposeIdleSummaries(r.Context())
	data, err := s.todayData(r.Context(), "")
	if err != nil {
		s.fail(w, err)
		return
	}
	data.Mode = modeFromQuery(r.URL.Query().Get("modo"))
	if data.Mode == "progress" {
		if data.Stats, err = s.statsData(r.Context()); err != nil {
			s.fail(w, err)
			return
		}
		data.StatsReady = true
	}
	if data.RestTarget == 0 {
		if data.RestTarget, err = s.service(r.Context()).RestTarget(r.Context()); err != nil {
			s.fail(w, err)
			return
		}
	}
	if !data.Active() {
		if data.Home, err = s.homeData(r.Context(), data.Plans); err != nil {
			s.fail(w, err)
			return
		}
	}
	data.Review.Pending = s.pendingProposals(r)
	data.Review.Answers = answerLabels
	data.PendingCount = len(data.Review.Pending)
	if s.opts.Coach != nil {
		data.CoachSees = s.coachSees(r.Context(), data.Plans)
	}
	s.render(w, r, "index.html", data)
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
	technique := r.PostFormValue("technique")
	category := lastFormValue(r, "category")

	message := ""
	if errWeight != nil || errReps != nil || errRPE != nil {
		message = "Revisa peso, reps y RPE."
	} else {
		session, err := s.ensureToday(r.Context())
		if err != nil {
			s.fail(w, err)
			return
		}
		set, _, err := s.service(r.Context()).AddSet(r.Context(), training.AddSetInput{
			SessionID: session.ID, Exercise: exercise, WeightKG: weight,
			Reps: reps, RPE: rpe, Technique: technique, Category: category,
		})
		switch {
		case errors.Is(err, training.ErrValidation):
			message = "Datos inválidos: revisa ejercicio, peso, reps y RPE (1-10)."
		case err != nil:
			s.fail(w, err)
			return
		default:
			s.notifySetLogged(r, set)
		}
	}
	s.renderPanel(w, r, message)
}

// lastFormValue is the last value sent for a field: a checkbox after a hidden
// input of the same name overrides it only when ticked.
func lastFormValue(r *http.Request, name string) string {
	values := r.PostForm[name]
	if len(values) == 0 {
		return ""
	}
	return values[len(values)-1]
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
	// Correcting the exercise moves the set to the right movement.
	if v := strings.TrimSpace(r.PostFormValue("exercise")); v != "" {
		patch.Exercise = &v
	}
	// Present but empty clears the technique back to a normal set.
	if r.Form.Has("technique") {
		v := r.PostFormValue("technique")
		patch.Technique = &v
	}
	// The edit form sends a hidden "working" before the warm-up checkbox, so
	// the last value is the toggle's state; a form without it leaves it alone.
	if r.Form.Has("category") {
		v := lastFormValue(r, "category")
		patch.Category = &v
	}
	if message == "" {
		switch _, _, err := s.service(r.Context()).UpdateSet(r.Context(), id, patch); {
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
	switch _, _, _, err := s.service(r.Context()).DeleteSet(r.Context(), id); {
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
		http.Redirect(w, r, s.trainURL(), http.StatusSeeOther)
		return
	}
	s.render(w, r, "panel.html", data)
}

// plansPage lists every routine with its exercises, editable in place. Plan
// building is a deliberate activity, so it lives on its own screen instead of
// crowding the logging form.
func (s *Server) plansPage(w http.ResponseWriter, r *http.Request) {
	data, err := s.plansData(r.Context(), "")
	if err != nil {
		s.fail(w, err)
		return
	}
	s.render(w, r, "plans.html", data)
}

func (s *Server) plansData(ctx context.Context, message string) (pageData, error) {
	data := pageData{Base: s.base, Today: s.todayDate(), Error: message}
	summaries, err := s.service(ctx).ListPlans(ctx)
	if err != nil {
		return data, err
	}
	// ListPlans returns totals only; the page needs each plan's exercises.
	for _, summary := range summaries {
		plan, err := s.service(ctx).GetPlan(ctx, summary.ID)
		if err != nil {
			return data, err
		}
		data.Plans = append(data.Plans, plan)
	}
	groups, err := s.service(ctx).ExerciseGroups(ctx)
	if err != nil {
		return data, err
	}
	for _, g := range groups {
		data.Catalogue = append(data.Catalogue, g.Exercise)
	}
	sort.Strings(data.Catalogue)
	return data, nil
}

// planAction applies one edit to the routines and re-renders the whole page.
func (s *Server) planAction(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.fail(w, err)
		return
	}
	planID, _ := strconv.ParseInt(r.PostFormValue("plan_id"), 10, 64)
	exercise := r.PostFormValue("exercise")
	atoi := func(name string) int {
		v, _ := strconv.Atoi(strings.TrimSpace(r.PostFormValue(name)))
		return v
	}

	var err error
	switch strings.TrimSuffix(r.URL.Path, "/") {
	case s.base + "/plans/create":
		// A routine starts empty; exercises are added from the page itself.
		_, err = s.service(r.Context()).CreatePlan(r.Context(), training.Plan{
			Name:  r.PostFormValue("name"),
			Items: []training.PlanItem{{Exercise: "por definir", TargetSets: 1}},
		})
	case s.base + "/plans/update":
		name, notes := r.PostFormValue("name"), r.PostFormValue("notes")
		err = s.service(r.Context()).UpdatePlan(r.Context(), planID, training.PlanPatch{Name: &name, Notes: &notes})
	case s.base + "/plans/delete":
		err = s.service(r.Context()).DeletePlan(r.Context(), planID)
	case s.base + "/plans/item":
		rpe, _ := strconv.ParseFloat(strings.TrimSpace(r.PostFormValue("target_rpe")), 64)
		err = s.service(r.Context()).SetPlanItem(r.Context(), planID, training.PlanItem{
			Exercise: exercise, TargetSets: atoi("target_sets"),
			RepMin: atoi("rep_min"), RepMax: atoi("rep_max"), TargetRPE: rpe,
			Superset: r.PostFormValue("superset"), Notes: r.PostFormValue("notes"),
		})
	case s.base + "/plans/item/remove":
		err = s.service(r.Context()).RemovePlanItem(r.Context(), planID, exercise)
	case s.base + "/plans/move":
		delta := 1
		if r.PostFormValue("direction") == "up" {
			delta = -1
		}
		err = s.service(r.Context()).MovePlanItem(r.Context(), planID, exercise, delta)
	}

	message := ""
	switch {
	case errors.Is(err, training.ErrNotFound):
		message = "Esa rutina o ejercicio ya no existe."
	case errors.Is(err, training.ErrValidation):
		message = "Datos inválidos: revisa nombre, series (1-20), rango de reps y RPE (1-10)."
	case err != nil:
		s.fail(w, err)
		return
	}
	if r.Header.Get("HX-Request") == "" {
		http.Redirect(w, r, s.base+"/plans", http.StatusSeeOther)
		return
	}
	data, dataErr := s.plansData(r.Context(), message)
	if dataErr != nil {
		s.fail(w, dataErr)
		return
	}
	s.render(w, r, "plans-main.html", data)
}

// export streams a consistent copy of the whole database. It is the backup
// path: the response is a plain SQLite file, so restoring is copying it back
// into the data volume. Nothing else in this deployment keeps a second copy.
func (s *Server) export(w http.ResponseWriter, r *http.Request) {
	dir, err := os.MkdirTemp("", "training-export")
	if err != nil {
		s.fail(w, err)
		return
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "training.db")
	if err := s.snapshot(r.Context(), path); err != nil {
		s.fail(w, err)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		s.fail(w, err)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/vnd.sqlite3")
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	w.Header().Set("Content-Disposition",
		`attachment; filename="training-`+s.todayDate()+`.db"`)
	http.ServeContent(w, r, "training.db", info.ModTime(), f)
}

func (s *Server) session(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	s.sessionPage(w, r, id, http.StatusOK, "")
}

// saveSessionAsPlan turns a past session into a routine, the same call the
// save_session_as_plan tool makes, and opens Rutinas to show it.
func (s *Server) saveSessionAsPlan(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.fail(w, err)
		return
	}
	name := strings.TrimSpace(r.PostFormValue("name"))
	if _, err := s.service(r.Context()).SessionProgress(r.Context(), id); errors.Is(err, training.ErrNotFound) || errors.Is(err, training.ErrValidation) {
		http.NotFound(w, r)
		return
	}
	if name == "" {
		s.sessionPage(w, r, id, http.StatusBadRequest, "Ponle un nombre a la rutina.")
		return
	}
	switch _, err := s.service(r.Context()).SaveSessionAsPlan(r.Context(), id, name); {
	case errors.Is(err, training.ErrNotFound):
		http.NotFound(w, r)
		return
	case errors.Is(err, training.ErrValidation):
		s.sessionPage(w, r, id, http.StatusBadRequest, "No se pudo guardar: la sesión no tiene ejercicios que copiar.")
		return
	case err != nil:
		s.fail(w, err)
		return
	}
	to := s.base + "/plans"
	if r.Header.Get("HX-Request") != "" {
		w.Header().Set("HX-Redirect", to)
		return
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// sessionPage renders one session read-only, with message shown above it.
func (s *Server) sessionPage(w http.ResponseWriter, r *http.Request, id int64, status int, message string) {
	session, err := s.service(r.Context()).GetSession(r.Context(), id)
	if errors.Is(err, training.ErrNotFound) || errors.Is(err, training.ErrValidation) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	data := pageData{Base: s.base, Today: session.Date, Session: session, ReadOnly: true, Error: message}
	if data.Progress, err = s.service(r.Context()).SessionProgress(r.Context(), session.ID); err != nil {
		s.fail(w, err)
		return
	}
	if err := s.decorate(r.Context(), &data); err != nil {
		s.fail(w, err)
		return
	}
	data.Today = s.todayDate()
	if status != http.StatusOK {
		// render sets the content type; the status must follow it.
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
	}
	s.render(w, r, "session.html", data)
}

// deleteSession removes a session and every set in it, the same call the
// delete_session tool makes. Another user's session gets what a missing one
// gets. The page it came from no longer exists, so it lands on Progreso.
func (s *Server) deleteSession(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "Esa sesión ya no existe.", http.StatusNotFound)
		return
	}
	switch _, err := s.service(r.Context()).DeleteSession(r.Context(), id); {
	case errors.Is(err, training.ErrNotFound), errors.Is(err, training.ErrValidation):
		http.Error(w, "Esa sesión ya no existe.", http.StatusNotFound)
		return
	case err != nil:
		s.fail(w, err)
		return
	}
	to := s.base + "/?modo=progreso"
	if r.Header.Get("HX-Request") != "" {
		w.Header().Set("HX-Redirect", to)
		return
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
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

	// The same reading the agent gets from live_workout.
	live, err := s.service(ctx).LiveWorkout(ctx)
	if err != nil {
		return data, err
	}
	if live.Session != nil {
		data.Session = *live.Session
		if at, err := time.Parse(time.RFC3339, live.Session.StartedAt); err == nil {
			data.Timed, data.Elapsed = true, max(0, int(s.clock().Sub(at)/time.Second))
		}
	}
	data.Progress, data.Next, data.Rest, data.RestTarget = live.Progress, live.Next, live.Rest, live.Rest.TargetSeconds
	if live.LastSet != nil {
		data.LastSetID = live.LastSet.Set.ID
	}
	if data.Recent, err = s.service(ctx).RecentExercises(ctx, recentExerciseLimit); err != nil {
		return data, err
	}
	if data.Plans, err = s.service(ctx).ListPlans(ctx); err != nil {
		return data, err
	}
	if data.Session.PlanID > 0 {
		// The routine's own notes, shown above its exercises.
		if plan, err := s.service(ctx).GetPlan(ctx, data.Session.PlanID); err == nil {
			data.PlanNotes = plan.Notes
		}
	}
	if err = s.decorate(ctx, &data); err != nil {
		return data, err
	}
	if data.Cards, err = s.trainCards(ctx, data); err != nil {
		return data, err
	}
	data.Records = map[int64]bool{}
	for _, b := range data.Blocks {
		for _, set := range b.Sets {
			if b.IsRecord(set) {
				data.Records[set.ID] = true
			}
		}
	}
	if data.Session.ID > 0 {
		seen := map[string]bool{}
		for _, b := range data.Blocks {
			if b.MuscleGroup != "" && !seen[b.MuscleGroup] {
				seen[b.MuscleGroup] = true
				data.TrainedGroups = append(data.TrainedGroups, b.MuscleGroup)
			}
		}
		given, err := s.service(ctx).SessionFeedback(ctx, data.Session.ID)
		if err != nil {
			return data, err
		}
		data.Feedback = map[string]training.Feedback{}
		for _, f := range given {
			data.Feedback[f.MuscleGroup] = f
		}
	}
	return data, nil
}

// exerciseInfo answers for one exercise on demand. The page preloads only the
// handful in play; picking anything else from the catalogue would otherwise
// show no previous performance and no setup note.
func (s *Server) exerciseInfo(w http.ResponseWriter, r *http.Request) {
	name := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("name")))
	w.Header().Set("Content-Type", "application/json")
	if name == "" {
		w.Write([]byte("{}"))
		return
	}
	entry, err := s.infoFor(r.Context(), name, s.todayDate())
	if err != nil {
		s.fail(w, err)
		return
	}
	json.NewEncoder(w).Encode(entry)
}

// saveNote stores a setup reminder for an exercise. Written once, shown every
// time that exercise comes up again.
func (s *Server) saveNote(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.fail(w, err)
		return
	}
	message := ""
	switch err := s.service(r.Context()).SetExerciseNote(r.Context(), r.PostFormValue("exercise"), r.PostFormValue("note")); {
	case errors.Is(err, training.ErrValidation):
		message = "Nota inválida: elige un ejercicio y usa 280 caracteres como mucho."
	case err != nil:
		s.fail(w, err)
		return
	}
	s.renderPanel(w, r, message)
}

// recordFeedback rates one muscle group's response to today's session.
func (s *Server) recordFeedback(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.fail(w, err)
		return
	}
	session, err := s.ensureToday(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	rating := func(name string) int {
		v, _ := strconv.Atoi(r.PostFormValue(name))
		return v
	}
	message := ""
	err = s.service(r.Context()).RecordFeedback(r.Context(), session.ID, training.Feedback{
		MuscleGroup: r.PostFormValue("muscle_group"),
		Fatigue:     rating("fatigue"), Pump: rating("pump"), Recovery: rating("recovery"),
	})
	switch {
	case errors.Is(err, training.ErrValidation):
		message = "Valoración inválida: usa 0 a 3."
	case err != nil:
		s.fail(w, err)
		return
	}
	s.renderPanel(w, r, message)
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
		http.Redirect(w, r, s.trainURL(), http.StatusSeeOther)
		return
	}
	// Before anything else: a plan that cannot be started must not cost the day's
	// empty session.
	plan, err := s.service(r.Context()).GetPlan(r.Context(), planID)
	if err != nil {
		s.fail(w, err)
		return
	}
	today := s.todayDate()
	sessions, err := s.service(r.Context()).ListSessions(r.Context(), training.ListFilter{From: today, To: today, Limit: 1})
	if err != nil {
		s.fail(w, err)
		return
	}
	if len(sessions) > 0 {
		// SetCount leaves warm-ups out, so ask the session itself whether
		// anything at all was logged before replacing it.
		current, err := s.service(r.Context()).GetSession(r.Context(), sessions[0].ID)
		if err != nil {
			s.fail(w, err)
			return
		}
		if len(current.Sets) == 0 {
			// Nothing logged yet: replace the empty session outright.
			if _, err := s.service(r.Context()).DeleteSession(r.Context(), sessions[0].ID); err != nil {
				s.fail(w, err)
				return
			}
		} else {
			// Work already logged. The plan is a per-session snapshot and sets
			// are never touched, so adopting another plan mid-session is safe:
			// its exercises are added and anything already done that the new
			// plan does not include simply shows as off-plan.
			for _, it := range plan.Items {
				if err := s.service(r.Context()).SetSessionItem(r.Context(), sessions[0].ID, it); err != nil {
					s.fail(w, err)
					return
				}
			}
			http.Redirect(w, r, s.trainURL(), http.StatusSeeOther)
			return
		}
	}
	if _, err := s.service(r.Context()).StartPlannedSession(r.Context(), today, planID); err != nil {
		s.fail(w, err)
		return
	}
	http.Redirect(w, r, s.trainURL(), http.StatusSeeOther)
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
		err = s.service(r.Context()).AdjustSessionItem(r.Context(), session.ID, exercise, training.SessionItemPatch{TargetSets: &next})
	case "skip", "unskip":
		skipped := action == "skip"
		err = s.service(r.Context()).AdjustSessionItem(r.Context(), session.ID, exercise, training.SessionItemPatch{Skipped: &skipped})
	case "swap":
		err = s.service(r.Context()).SwapSessionItem(r.Context(), session.ID, exercise, r.PostFormValue("replacement"))
	case "adopt":
		// An exercise done off-plan becomes part of today's plan at the volume
		// already performed.
		done, _ := strconv.Atoi(r.PostFormValue("done"))
		if done < 1 {
			done = 1
		}
		err = s.service(r.Context()).SetSessionItem(r.Context(), session.ID, training.PlanItem{Exercise: exercise, TargetSets: done})
	case "remove":
		err = s.service(r.Context()).RemoveSessionItem(r.Context(), session.ID, exercise)
	case "add":
		// A new exercise for today only, with its own prescription.
		it := training.PlanItem{Exercise: exercise}
		var bad bool
		it.TargetSets, bad = formInt(r, "target_sets", bad)
		it.RepMin, bad = formInt(r, "rep_min", bad)
		it.RepMax, bad = formInt(r, "rep_max", bad)
		it.TargetRPE, bad = formFloat(r, "target_rpe", bad)
		if bad {
			message = "Ajuste inválido."
			break
		}
		err = s.service(r.Context()).SetSessionItem(r.Context(), session.ID, it)
	case "rx":
		// Rep range and RPE; the set count has its own buttons. Blank fields stay.
		patch := training.SessionItemPatch{}
		var bad bool
		if r.PostFormValue("rep_min") != "" {
			v, b := formInt(r, "rep_min", false)
			patch.RepMin, bad = &v, bad || b
		}
		if r.PostFormValue("rep_max") != "" {
			v, b := formInt(r, "rep_max", false)
			patch.RepMax, bad = &v, bad || b
		}
		if r.PostFormValue("target_rpe") != "" {
			v, b := formFloat(r, "target_rpe", false)
			patch.TargetRPE, bad = &v, bad || b
		}
		if bad {
			message = "Ajuste inválido."
			break
		}
		err = s.service(r.Context()).AdjustSessionItem(r.Context(), session.ID, exercise, patch)
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

// formInt reads an optional whole number; blank is zero. bad accumulates.
func formInt(r *http.Request, name string, bad bool) (int, bool) {
	v := strings.TrimSpace(r.PostFormValue(name))
	if v == "" {
		return 0, bad
	}
	n, err := strconv.Atoi(v)
	return n, bad || err != nil
}

// formFloat reads an optional number; blank is zero. bad accumulates.
func formFloat(r *http.Request, name string, bad bool) (float64, bool) {
	v := strings.TrimSpace(r.PostFormValue(name))
	if v == "" {
		return 0, bad
	}
	f, err := strconv.ParseFloat(v, 64)
	return f, bad || err != nil
}

// setRest stores the rest target chosen with the timer's buttons. It answers in
// JSON because the timer calls it in the background, never swapping the page.
func (s *Server) setRest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	seconds, err := strconv.Atoi(strings.TrimSpace(r.FormValue("seconds")))
	if err == nil {
		err = s.service(r.Context()).SetRestTarget(r.Context(), seconds)
	}
	switch {
	case err == nil:
	case errors.Is(err, training.ErrValidation), errors.Is(err, strconv.ErrSyntax), errors.Is(err, strconv.ErrRange):
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("rest must be %d to %d seconds", training.MinRestSeconds, training.MaxRestSeconds)})
		return
	default:
		s.fail(w, err)
		return
	}
	v, err := s.service(r.Context()).RestTarget(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	json.NewEncoder(w).Encode(map[string]int{"rest_seconds": v})
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

	groups, err := s.service(ctx).ExerciseGroups(ctx)
	if err != nil {
		return err
	}
	catalogue := map[string]bool{}
	for _, g := range groups {
		catalogue[g.Exercise] = true
	}
	for _, n := range names {
		catalogue[n] = true
	}
	notes, err := s.service(ctx).ExerciseNotes(ctx)
	if err != nil {
		return err
	}
	noteByExercise := map[string]string{}
	for _, n := range notes {
		noteByExercise[n.Exercise] = n.Note
		catalogue[n.Exercise] = true
	}

	for n := range catalogue {
		data.Catalogue = append(data.Catalogue, n)
	}
	sort.Strings(data.Catalogue)

	info := map[string]ExerciseInfo{}
	for _, name := range names {
		entry, err := s.infoFor(ctx, name, data.Today)
		if err != nil {
			return err
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
	sessions, err := s.service(ctx).ListSessions(ctx, training.ListFilter{From: today, To: today, Limit: 1})
	if err != nil {
		return training.Session{}, err
	}
	if len(sessions) > 0 {
		return training.Session{ID: sessions[0].ID, Date: sessions[0].Date}, nil
	}
	return s.service(ctx).StartSession(ctx, today)
}

// infoFor gathers what the entry form shows about one exercise: last time's
// set, the standing record, its muscle group and its setup note.
func (s *Server) infoFor(ctx context.Context, name, today string) (ExerciseInfo, error) {
	h, err := s.service(ctx).ExerciseHistory(ctx, name, 200)
	if err != nil {
		return ExerciseInfo{}, err
	}
	entry := ExerciseInfo{Group: h.MuscleGroup}
	if h.Best != nil {
		entry.BestE1RM = h.Best.Est1RM
	}
	// "Last time" must skip today, or the form would echo the set just logged.
	for i := range h.Sets {
		if h.Sets[i].Date != today && h.Sets[i].Category != training.SetWarmup {
			entry.Last = &h.Sets[i]
			break
		}
	}
	notes, err := s.service(ctx).ExerciseNotes(ctx)
	if err != nil {
		return entry, err
	}
	for _, n := range notes {
		if n.Exercise == name {
			entry.Note = n.Note
			break
		}
	}
	suggestion, err := s.service(ctx).SuggestLoad(ctx, name)
	if err != nil {
		return entry, err
	}
	// Only offer advice that actually moves the bar; "hold" is already implied
	// by the previous set being shown.
	if suggestion.DeltaKG != 0 {
		entry.Suggest = &suggestion
	}
	return entry, nil
}

func (s *Server) todayDate() string { return s.clock().Format("2006-01-02") }

func (s *Server) render(w http.ResponseWriter, r *http.Request, name string, data pageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if s.opts.CSRFToken != nil {
		data.CSRF = s.opts.CSRFToken(r)
	}
	data.CanExport = s.opts.Export
	data.CoachOn = s.opts.Coach != nil
	data.ConnectorsOn = s.opts.Coach != nil && s.opts.Connectors != nil
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
// formatNumber shows at most 2 decimals with no trailing zeros, so a weight
// stored as 65.77089365 reads and pre-fills as 65.77.
func formatNumber(v float64) string {
	v = math.Round(v*100) / 100
	if v == 0 {
		v = 0 // no "-0"
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// modeFromQuery maps ?modo= to the screen the home opens on.
func modeFromQuery(v string) string {
	switch v {
	case "entrenar":
		return "train"
	case "planificar":
		return "plan"
	case "progreso":
		return "progress"
	case "tu":
		return "you"
	default:
		return "home"
	}
}

// trainURL is the training screen, where a write without JavaScript lands.
func (s *Server) trainURL() string { return s.base + "/?modo=entrenar" }

// clockText is a workout duration as the app's clock shows it: 7:05, 1:02:09.
func clockText(seconds int) string {
	h, m, sec := seconds/3600, seconds%3600/60, seconds%60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, sec)
	}
	return fmt.Sprintf("%d:%02d", m, sec)
}
