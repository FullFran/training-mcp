package websrv

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/fullfran/training-mcp/internal/training"
)

// Planificar is the coach's full-screen chat. Routines the coach proposes wait
// there as drafts, and a small card says what the coach can see.

// DraftView is a routine the coach proposed and the person has not saved yet.
type DraftView struct {
	Name    string
	Meta    string // "4 ejercicios · 16 series"
	Changes []DraftChange
}

// DraftChange is one line of "Ver cambios": an exercise and its prescription.
type DraftChange struct{ Exercise, Rx string }

// routineDraft reads a create_plan proposal as a draft; other proposals,
// including edits to an existing routine, keep the plain review card.
func routineDraft(p training.Proposal) *DraftView {
	if p.ActionType != "plan.create" || p.Tool != "create_plan" {
		return nil
	}
	in, ok := decode[struct {
		Name  string         `json:"name"`
		Items []prescription `json:"items"`
	}](p.Payload)
	if !ok || in.Name == "" || len(in.Items) == 0 {
		return nil
	}
	d := &DraftView{Name: in.Name}
	sets := 0
	for _, it := range in.Items {
		sets += it.TargetSets
		d.Changes = append(d.Changes, DraftChange{Exercise: it.Exercise, Rx: it.rxText()})
	}
	d.Meta = fmt.Sprintf("%d %s · %d %s", len(in.Items), plural(len(in.Items), "ejercicio", "ejercicios"), sets, plural(sets, "serie", "series"))
	return d
}

// CoachSees is the context card at the top of Planificar.
type CoachSees struct{ Line string }

const (
	coachSeesDays   = 28
	coachSeesLimit  = 100
	coachSeesMemory = 100
)

// coachSees counts what the coach reads before answering: routines, recent
// workouts and active memories. A failed count leaves the card out rather
// than the page.
func (s *Server) coachSees(ctx context.Context, plans []training.Plan) *CoachSees {
	svc := s.service(ctx)
	today := s.clock()
	from := today.AddDate(0, 0, -(coachSeesDays - 1)).Format(time.DateOnly)
	sessions, err := svc.ListSessions(ctx, training.ListFilter{From: from, To: today.Format(time.DateOnly), Limit: coachSeesLimit})
	if err != nil {
		return nil
	}
	worked := 0
	for _, sess := range sessions {
		if sess.SetCount > 0 {
			worked++
		}
	}
	memories, err := svc.SearchMemory(ctx, training.MemoryQuery{Limit: coachSeesMemory})
	if err != nil {
		return nil
	}
	parts := []string{
		fmt.Sprintf("%d %s", len(plans), plural(len(plans), "rutina", "rutinas")),
		fmt.Sprintf("%d %s en 4 semanas", worked, plural(worked, "entreno", "entrenos")),
		fmt.Sprintf("%d %s", len(memories), plural(len(memories), "recuerdo", "recuerdos")),
	}
	return &CoachSees{Line: strings.Join(parts, " · ")}
}
