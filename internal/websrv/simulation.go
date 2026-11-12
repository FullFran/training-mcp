package websrv

import (
	"context"
	"fmt"
	"strings"

	"github.com/fullfran/training-mcp/internal/training"
)

// SimulationView is the week a pending plan proposal would prescribe, shown
// before the person accepts it: sets per muscle group against their MEV and
// MRV, and the totals.
type SimulationView struct {
	Meta     string // "1 vez por semana · 14 series · SI 15"
	Groups   []SimulationGroup
	Unmapped string // "Sin grupo muscular: curl araña"
}

// SimulationGroup is one trained muscle group's line. Tone is low, ok, high
// or none, for styling.
type SimulationGroup struct{ Name, Sets, Status, Range, Tone string }

var simulationStatus = map[string]struct{ label, tone string }{
	training.VolumeBelowMEV:    {"Por debajo de tu MEV", "low"},
	training.VolumeWithin:      {"Dentro de tu rango", "ok"},
	training.VolumeAboveMRV:    {"Por encima de tu MRV", "high"},
	training.VolumeNoLandmarks: {"Sin MEV/MRV", "none"},
}

// planSimulation simulates what a pending plan proposal would leave: the new
// routine, or a saved one with the edit applied. Other proposals, and any
// simulation that fails (a payload the tool would reject, a plan since
// deleted), show none: the card still says what the proposal does.
func (s *Server) planSimulation(ctx context.Context, p training.Proposal) *SimulationView {
	var in training.PlanSimulationInput
	switch p.Tool {
	case "create_plan":
		v, ok := decode[struct {
			Items []planItemPayload `json:"items"`
		}](p.Payload)
		if !ok {
			return nil
		}
		for _, it := range v.Items {
			in.Items = append(in.Items, it.item())
		}
	case "set_plan_exercise":
		v, ok := decode[struct {
			PlanID int64 `json:"plan_id"`
			planItemPayload
		}](p.Payload)
		if !ok {
			return nil
		}
		it := v.item()
		in.PlanID, in.Edit = v.PlanID, &training.PlanEdit{Set: &it}
	case "remove_plan_exercise":
		v, ok := decode[struct {
			PlanID   int64  `json:"plan_id"`
			Exercise string `json:"exercise"`
		}](p.Payload)
		if !ok {
			return nil
		}
		in.PlanID, in.Edit = v.PlanID, &training.PlanEdit{Remove: v.Exercise}
	default:
		return nil
	}
	sim, err := s.service(ctx).SimulatePlan(ctx, in)
	if err != nil {
		return nil
	}
	return simulationView(sim)
}

// planItemPayload is a plan item as create_plan and set_plan_exercise send it.
type planItemPayload struct {
	Exercise   string  `json:"exercise"`
	TargetSets int     `json:"target_sets"`
	RepMin     int     `json:"rep_min"`
	RepMax     int     `json:"rep_max"`
	TargetRPE  float64 `json:"target_rpe"`
}

func (p planItemPayload) item() training.PlanItem {
	return training.PlanItem{Exercise: p.Exercise, TargetSets: p.TargetSets, RepMin: p.RepMin, RepMax: p.RepMax, TargetRPE: p.TargetRPE}
}

func simulationView(sim training.PlanSimulation) *SimulationView {
	v := &SimulationView{Meta: fmt.Sprintf("%d %s por semana · %d %s · SI %s",
		sim.SessionsPerWeek, plural(sim.SessionsPerWeek, "vez", "veces"),
		sim.WeeklySets, plural(sim.WeeklySets, "serie", "series"), kg(sim.WeeklySI))}
	for _, g := range sim.Groups {
		st := simulationStatus[g.Status]
		var bounds []string
		if g.MEV > 0 {
			bounds = append(bounds, fmt.Sprintf("MEV %d", g.MEV))
		}
		if g.MRV > 0 {
			bounds = append(bounds, fmt.Sprintf("MRV %d", g.MRV))
		}
		v.Groups = append(v.Groups, SimulationGroup{
			Name: g.MuscleGroup, Sets: fmt.Sprintf("%d %s", g.WeeklySets, plural(g.WeeklySets, "serie", "series")),
			Status: st.label, Range: strings.Join(bounds, " · "), Tone: st.tone,
		})
	}
	if len(sim.Unmapped) > 0 {
		v.Unmapped = "Sin grupo muscular: " + strings.Join(sim.Unmapped, ", ")
	}
	return v
}
