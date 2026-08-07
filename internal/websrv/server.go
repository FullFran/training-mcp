// Package websrv is a driving adapter that exposes the training service as an
// installable PWA. It holds no domain logic: validation, SI calculation and
// exercise normalization all stay in training.Service, so the web UI and the
// MCP tools can never disagree.
package websrv

import (
	"context"
	"embed"
	"errors"
	"html/template"
	"io/fs"
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

type pageData struct {
	Base    string
	Today   string
	Session training.Session
	Recent  []training.ExerciseMemory
	History []training.SessionSummary
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

func New(service *training.Service, clock training.Clock, basePath string) (*Server, error) {
	funcs := map[string]any{"num": formatNumber, "rpeScale": rpeScale}
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
	s.mux.HandleFunc("GET "+b+"/history", s.history)
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
	s.render(w, "history.html", pageData{Base: s.base, Today: s.todayDate(), History: sessions})
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
	s.render(w, "session.html", pageData{Base: s.base, Today: s.todayDate(), Session: session, ReadOnly: true})
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
	return data, nil
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
