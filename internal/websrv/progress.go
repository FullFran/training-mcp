package websrv

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"math"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/fullfran/training-mcp/internal/training"
)

// Progreso's tab reads a year of sets once and derives everything from them,
// rather than asking for each exercise's history in turn.
const (
	progressDays     = 365
	progressSessions = 60
	// trendPoints is how many sessions an exercise's sparkline spans.
	trendPoints = 12
)

// StatsData is Progreso's tab: Historial (this week, then every session by
// month), Ejercicios (e1RM and best set per exercise, volume per muscle) and
// Cuerpo, which has nothing recorded yet.
type StatsData struct {
	Week         []WeekDay
	WeekSessions int
	WeekKG       float64
	Months       []MonthSessions
	Exercises    []ExerciseStat
	Volume       []training.GroupVolume
	Recommend    []training.SetChange
}

// WeekDay is one dot of the week strip, Monday first.
type WeekDay struct {
	Letter string
	Done   bool
	Today  bool
}

// MonthSessions is one month header of the history and its sessions.
type MonthSessions struct {
	Name     string
	Sessions []HistoryRow
}

// HistoryRow is one past session: its summary, the day as the chips write it,
// and the working kilos moved. KG is zero when the day holds two sessions,
// because the sets carry the date and not the session.
type HistoryRow struct {
	training.SessionSummary
	Day string
	KG  float64
}

// ExerciseStat is one exercise in Ejercicios.
type ExerciseStat struct {
	Name       string
	Group      string
	Best1RM    float64
	BestWeight float64
	BestReps   int
	// Delta is the change in best e1RM across the trend, first to last session.
	Delta float64
	Trend string
}

// ExerciseDetail is the pushed screen of one exercise: its e1RM per session,
// the best sets, every session's sets and the fatigue between sets last time.
type ExerciseDetail struct {
	training.ExerciseHistory
	Trend   string
	Delta   float64
	Top     []training.ExerciseSet
	Days    []ExerciseDay
	DropOff *training.DropOffAnalysis
}

// ExerciseDay is one session's working sets of the exercise.
type ExerciseDay struct {
	Date string
	Day  string
	Best float64
	Sets []training.ExerciseSet
}

var (
	weekLetters = [...]string{"L", "M", "X", "J", "V", "S", "D"}
	monthNames  = [...]string{"Enero", "Febrero", "Marzo", "Abril", "Mayo", "Junio", "Julio", "Agosto", "Septiembre", "Octubre", "Noviembre", "Diciembre"}
)

// progress serves Progreso's body; the tab loads it when first shown.
func (s *Server) progress(w http.ResponseWriter, r *http.Request) {
	stats, err := s.statsData(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	s.render(w, r, "progress-body", pageData{Base: s.base, Today: s.todayDate(), Stats: stats})
}

// historyMoved sends the old history page to Progreso, its new home.
func (s *Server) historyMoved(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, s.base+"/?modo=progreso", http.StatusFound)
}

func (s *Server) statsData(ctx context.Context) (StatsData, error) {
	svc := s.service(ctx)
	var out StatsData
	today, err := time.Parse("2006-01-02", s.todayDate())
	if err != nil {
		return out, err
	}
	sessions, err := svc.ListSessions(ctx, training.ListFilter{Limit: progressSessions})
	if err != nil {
		return out, err
	}
	sets, err := svc.DaySets(ctx, today.AddDate(0, 0, -progressDays).Format("2006-01-02"), today.Format("2006-01-02"))
	if err != nil {
		return out, err
	}
	groups, err := svc.ExerciseGroups(ctx)
	if err != nil {
		return out, err
	}
	if out.Volume, err = svc.VolumeByGroup(ctx, training.ListFilter{}); err != nil {
		return out, err
	}
	if out.Recommend, err = svc.VolumeRecommendation(ctx); err != nil {
		return out, err
	}

	kgByDate := map[string]float64{}
	for _, set := range sets {
		if set.Category != training.SetWarmup {
			kgByDate[set.Date] += set.WeightKG * float64(set.Reps)
		}
	}
	sessionsByDate := map[string]int{}
	for _, sess := range sessions {
		sessionsByDate[sess.Date]++
	}

	// The week runs Monday to Sunday, as the strip reads.
	monday := today.AddDate(0, 0, -((int(today.Weekday()) + 6) % 7))
	for i := range 7 {
		date := monday.AddDate(0, 0, i).Format("2006-01-02")
		done := sessionsByDate[date] > 0
		out.Week = append(out.Week, WeekDay{Letter: weekLetters[i], Done: done, Today: i == (int(today.Weekday())+6)%7})
		if done {
			out.WeekSessions += sessionsByDate[date]
			out.WeekKG += kgByDate[date]
		}
	}

	for _, sess := range sessions {
		row := HistoryRow{SessionSummary: sess, Day: shortDate(sess.Date)}
		if sessionsByDate[sess.Date] == 1 {
			row.KG = kgByDate[sess.Date]
		}
		name := monthName(sess.Date, today.Year())
		if n := len(out.Months); n == 0 || out.Months[n-1].Name != name {
			out.Months = append(out.Months, MonthSessions{Name: name})
		}
		last := &out.Months[len(out.Months)-1]
		last.Sessions = append(last.Sessions, row)
	}

	groupOf := map[string]string{}
	for _, g := range groups {
		groupOf[g.Exercise] = g.MuscleGroup
	}
	out.Exercises = exerciseStats(sets, groupOf)
	return out, nil
}

// exerciseStats ranks exercises by when they were last done, each with its best
// set by e1RM and the best e1RM of each of its latest sessions.
func exerciseStats(sets []training.DaySet, groupOf map[string]string) []ExerciseStat {
	byExercise := map[string]map[string]float64{}
	best := map[string]ExerciseStat{}
	for _, set := range sets {
		if set.Category == training.SetWarmup {
			continue
		}
		e1rm := training.Epley1RM(set.WeightKG, set.Reps)
		if e1rm <= 0 {
			continue
		}
		if byExercise[set.Exercise] == nil {
			byExercise[set.Exercise] = map[string]float64{}
		}
		byExercise[set.Exercise][set.Date] = max(byExercise[set.Exercise][set.Date], e1rm)
		if b := best[set.Exercise]; e1rm > b.Best1RM {
			best[set.Exercise] = ExerciseStat{Name: set.Exercise, Group: groupOf[set.Exercise], Best1RM: e1rm, BestWeight: set.WeightKG, BestReps: set.Reps}
		}
	}
	out := make([]ExerciseStat, 0, len(best))
	lastDate := map[string]string{}
	for name, byDate := range byExercise {
		dates := slices.Sorted(maps.Keys(byDate))
		if len(dates) > trendPoints {
			dates = dates[len(dates)-trendPoints:]
		}
		values := make([]float64, len(dates))
		for i, d := range dates {
			values[i] = byDate[d]
		}
		stat := best[name]
		stat.Trend = sparkline(values)
		stat.Delta = math.Round((values[len(values)-1]-values[0])*10) / 10
		lastDate[name] = dates[len(dates)-1]
		out = append(out, stat)
	}
	sort.Slice(out, func(i, j int) bool {
		if lastDate[out[i].Name] != lastDate[out[j].Name] {
			return lastDate[out[i].Name] > lastDate[out[j].Name]
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// sparkline draws values as SVG polyline points in a 300x80 box, oldest on the
// left; a single value is a flat line so one session still shows a mark.
func sparkline(values []float64) string {
	if len(values) == 0 {
		return ""
	}
	if len(values) == 1 {
		values = []float64{values[0], values[0]}
	}
	lo, hi := slices.Min(values), slices.Max(values)
	const w, h, pad = 300.0, 80.0, 6.0
	var b strings.Builder
	for i, v := range values {
		x := w * float64(i) / float64(len(values)-1)
		y := h / 2
		if hi > lo {
			y = pad + (h-2*pad)*(hi-v)/(hi-lo)
		}
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(strconv.FormatFloat(math.Round(x*10)/10, 'f', -1, 64) + "," + strconv.FormatFloat(math.Round(y*10)/10, 'f', -1, 64))
	}
	return b.String()
}

func monthName(date string, year int) string {
	d, err := time.Parse("2006-01-02", date)
	if err != nil {
		return date
	}
	name := monthNames[d.Month()-1]
	if d.Year() != year {
		name += " " + strconv.Itoa(d.Year())
	}
	return name
}

// kilos writes a whole number of kilos with a dot every three digits, as
// Spanish does: 24.900 kg.
func kilos(v float64) string {
	s := strconv.FormatInt(int64(math.Round(v)), 10)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "." + s[i:]
	}
	return s
}

// exerciseDetail is one exercise's pushed screen.
func (s *Server) exerciseDetail(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	svc := s.service(ctx)
	name := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("name")))
	hist, err := svc.ExerciseHistory(ctx, name, 500)
	if errors.Is(err, training.ErrValidation) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	detail := ExerciseDetail{ExerciseHistory: hist}
	for _, set := range hist.Sets {
		if set.Category == training.SetWarmup {
			continue
		}
		if n := len(detail.Days); n == 0 || detail.Days[n-1].Date != set.Date {
			detail.Days = append(detail.Days, ExerciseDay{Date: set.Date, Day: shortDate(set.Date)})
		}
		d := &detail.Days[len(detail.Days)-1]
		d.Sets = append(d.Sets, set)
		d.Best = max(d.Best, set.Est1RM)
		detail.Top = append(detail.Top, set)
	}
	if len(detail.Days) == 0 {
		http.NotFound(w, r)
		return
	}
	sort.SliceStable(detail.Top, func(i, j int) bool { return detail.Top[i].Est1RM > detail.Top[j].Est1RM })
	detail.Top = detail.Top[:min(5, len(detail.Top))]
	// Sets come newest first; the chart reads oldest first.
	values := []float64{}
	for i := min(trendPoints, len(detail.Days)) - 1; i >= 0; i-- {
		values = append(values, detail.Days[i].Best)
	}
	detail.Trend = sparkline(values)
	detail.Delta = math.Round((values[len(values)-1]-values[0])*10) / 10
	for _, set := range detail.Days[0].Sets {
		// Each set carries its session; the latest day may hold two sessions.
		analyses, err := svc.PerformanceDropOff(ctx, set.SessionID, name)
		if err != nil && !errors.Is(err, training.ErrValidation) && !errors.Is(err, training.ErrNotFound) {
			s.fail(w, err)
			return
		}
		for i := range analyses {
			if analyses[i].Comparable && analyses[i].Explanation != "" {
				detail.DropOff = &analyses[i]
			}
		}
		break
	}
	s.render(w, r, "exercise.html", pageData{Base: s.base, Today: s.todayDate(), Exercise: detail})
}

// pickerExercise is one row of the exercise picker.
type pickerExercise struct {
	Name  string `json:"name"`
	Group string `json:"group,omitempty"`
	Last  string `json:"last,omitempty"`
}

// exercises feeds the exercise picker: every exercise the person knows, its
// muscle group and last set, the groups present and the latest used.
func (s *Server) exercises(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	svc := s.service(ctx)
	names, err := svc.ExerciseNames(ctx)
	if err != nil {
		s.fail(w, err)
		return
	}
	groups, err := svc.ExerciseGroups(ctx)
	if err != nil {
		s.fail(w, err)
		return
	}
	recent, err := svc.RecentExercises(ctx, 50)
	if err != nil {
		s.fail(w, err)
		return
	}
	known := map[string]*pickerExercise{}
	list := []*pickerExercise{}
	ensure := func(name string) *pickerExercise {
		if e, ok := known[name]; ok {
			return e
		}
		e := &pickerExercise{Name: name}
		known[name] = e
		list = append(list, e)
		return e
	}
	for _, n := range names {
		ensure(n)
	}
	present := map[string]bool{}
	for _, g := range groups {
		ensure(g.Exercise).Group = g.MuscleGroup
		if g.MuscleGroup != "" {
			present[g.MuscleGroup] = true
		}
	}
	out := struct {
		Groups    []string          `json:"groups"`
		Recent    []string          `json:"recent"`
		Exercises []*pickerExercise `json:"exercises"`
	}{Groups: []string{}, Recent: []string{}}
	for _, m := range recent {
		e := ensure(m.Exercise)
		e.Last = formatNumber(m.WeightKG) + "×" + strconv.Itoa(m.Reps)
		out.Recent = append(out.Recent, m.Exercise)
	}
	for _, g := range training.MuscleGroups {
		if present[g] {
			out.Groups = append(out.Groups, g)
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	out.Exercises = list
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

// restClock writes a rest target as the timer shows it: 2:30.
func restClock(seconds int) string {
	return strconv.Itoa(seconds/60) + ":" + strconv.Itoa(seconds%60/10) + strconv.Itoa(seconds%10)
}
