package websrv

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/fullfran/training-mcp/internal/training"
)

// homeSessionLimit bounds how many past sessions Entrenar's home reads to
// find when each routine was last done.
const homeSessionLimit = 60

// HomeData is Entrenar's home: the routine to do next, the others, and the
// last session. It is empty while a workout is live, which the home continues.
type HomeData struct {
	Next   *RoutineCard
	Others []RoutineCard
	Last   *LastSession
}

// RoutineCard is one routine the home offers to start.
type RoutineCard struct {
	Plan training.Plan
	// Exercises are the routine's exercise names, joined for one line; only
	// the next routine's are read.
	Exercises string
	// Ago says when it was last done ("hace 3 días"), empty if never.
	Ago string
}

// LastSession is the latest finished session before today.
type LastSession struct {
	training.SessionSummary
	Ago string
}

// homeData picks the next routine: the one after the last one done, in the
// plans' order, wrapping around; the first one when none was ever done.
func (s *Server) homeData(ctx context.Context, plans []training.Plan) (HomeData, error) {
	var home HomeData
	today := s.todayDate()
	sessions, err := s.service(ctx).ListSessions(ctx, training.ListFilter{Limit: homeSessionLimit})
	if err != nil {
		return home, err
	}
	lastDone := map[string]string{} // plan name -> latest date, newest first
	lastName := ""
	for _, sess := range sessions {
		if sess.Date >= today || sess.SetCount == 0 {
			continue
		}
		if home.Last == nil {
			home.Last = &LastSession{SessionSummary: sess, Ago: daysAgo(sess.Date, today)}
		}
		if sess.PlanName == "" {
			continue
		}
		if lastName == "" {
			lastName = sess.PlanName
		}
		if _, ok := lastDone[sess.PlanName]; !ok {
			lastDone[sess.PlanName] = sess.Date
		}
	}
	if len(plans) == 0 {
		return home, nil
	}
	next := 0
	for i, p := range plans {
		if p.Name == lastName {
			next = (i + 1) % len(plans)
			break
		}
	}
	for i, p := range plans {
		card := RoutineCard{Plan: p}
		if date, ok := lastDone[p.Name]; ok {
			card.Ago = daysAgo(date, today)
		}
		if i == next {
			// The list carries no exercises; the card shown first names them.
			full, err := s.service(ctx).GetPlan(ctx, p.ID)
			if err != nil {
				return home, err
			}
			card.Plan, card.Exercises = full, exerciseNames(full)
			home.Next = &card
			continue
		}
		home.Others = append(home.Others, card)
	}
	return home, nil
}

func exerciseNames(p training.Plan) string {
	names := make([]string, 0, len(p.Items))
	for _, item := range p.Items {
		names = append(names, item.Exercise)
	}
	return strings.Join(names, " · ")
}

// daysAgo says how long ago a date was, in the home's words.
func daysAgo(date, today string) string {
	d, err1 := time.Parse("2006-01-02", date)
	t, err2 := time.Parse("2006-01-02", today)
	if err1 != nil || err2 != nil {
		return date
	}
	switch n := int(t.Sub(d).Hours() / 24); {
	case n <= 0:
		return "hoy"
	case n == 1:
		return "ayer"
	default:
		return "hace " + strconv.Itoa(n) + " días"
	}
}

// PlannedSets is how many working sets today's plan asks for in total.
func (p pageData) PlannedSets() int {
	n := 0
	for _, item := range p.Progress {
		n += max(item.TargetSets, item.DoneSets)
	}
	return n
}

// Active reports a workout in progress: a plan chosen or a set logged today.
func (p pageData) Active() bool { return len(p.Progress) > 0 || len(p.Session.Sets) > 0 }

var (
	weekdays = [...]string{"dom", "lun", "mar", "mié", "jue", "vie", "sáb"}
	months   = [...]string{"ene", "feb", "mar", "abr", "may", "jun", "jul", "ago", "sep", "oct", "nov", "dic"}
)

// shortDate writes a date as the home's chip does: "jue 12 nov".
func shortDate(date string) string {
	d, err := time.Parse("2006-01-02", date)
	if err != nil {
		return date
	}
	return weekdays[d.Weekday()] + " " + strconv.Itoa(d.Day()) + " " + months[d.Month()-1]
}
