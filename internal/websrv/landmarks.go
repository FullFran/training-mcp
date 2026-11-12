package websrv

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/fullfran/training-mcp/internal/training"
)

func (s *Server) landmarksPage(w http.ResponseWriter, r *http.Request) {
	s.renderLandmarks(w, r, http.StatusOK, "")
}

// renderLandmarks shows every muscle group, recorded or not, so a person can
// fill in one they have never set: list_volume_landmarks plus the blanks.
func (s *Server) renderLandmarks(w http.ResponseWriter, r *http.Request, status int, message string) {
	recorded, err := s.service(r.Context()).Landmarks(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	byGroup := map[string]training.Landmarks{}
	for _, l := range recorded {
		byGroup[l.MuscleGroup] = l
	}
	data := pageData{Base: s.base, Today: s.todayDate(), Error: message}
	for _, g := range training.MuscleGroups {
		l := byGroup[g]
		l.MuscleGroup = g
		data.Landmarks = append(data.Landmarks, l)
	}
	if status != http.StatusOK {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
	}
	s.render(w, r, "landmarks.html", data)
}

// setLandmarks records one group's MEV and MRV with the call
// set_volume_landmarks makes. A blank value is unknown and stored as 0.
func (s *Server) setLandmarks(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.fail(w, err)
		return
	}
	value := func(name string) (int, bool) {
		v := strings.TrimSpace(r.PostFormValue(name))
		if v == "" {
			return 0, true
		}
		n, err := strconv.Atoi(v)
		return n, err == nil && n >= 0
	}
	mev, okMEV := value("mev")
	mrv, okMRV := value("mrv")
	if !okMEV || !okMRV {
		s.renderLandmarks(w, r, http.StatusBadRequest, "Usa números enteros de series, o déjalo vacío si no lo sabes.")
		return
	}
	if mev > 0 && mrv > 0 && mev > mrv {
		s.renderLandmarks(w, r, http.StatusBadRequest, "El MEV no puede superar al MRV.")
		return
	}
	switch err := s.service(r.Context()).SetLandmarks(r.Context(), training.Landmarks{MuscleGroup: r.PostFormValue("muscle_group"), MEV: mev, MRV: mrv}); {
	case errors.Is(err, training.ErrValidation):
		s.renderLandmarks(w, r, http.StatusBadRequest, "Grupo muscular inválido.")
		return
	case err != nil:
		s.fail(w, err)
		return
	}
	if r.Header.Get("HX-Request") != "" {
		s.renderLandmarks(w, r, http.StatusOK, "")
		return
	}
	http.Redirect(w, r, s.base+"/landmarks", http.StatusSeeOther)
}
