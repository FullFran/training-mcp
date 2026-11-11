package websrv

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// proposalSummary says in one sentence what a proposal of a write tool would
// do, built from its payload only, so it is the same every time and costs no
// model. The payload shapes mirror the MCP tools' inputs. An unknown tool or a
// payload that does not read as expected gives "", and the page falls back to
// listing the payload's fields.
func proposalSummary(tool string, payload json.RawMessage) string {
	summarise, ok := summaries[tool]
	if !ok {
		return ""
	}
	return summarise(payload)
}

type prescription struct {
	Exercise   string  `json:"exercise"`
	TargetSets int     `json:"target_sets"`
	RepMin     int     `json:"rep_min"`
	RepMax     int     `json:"rep_max"`
	TargetRPE  float64 `json:"target_rpe"`
}

// rxText is "3 series", or "3 × 8-12 reps @8.5" with a rep range.
func (p prescription) rxText() string {
	var b strings.Builder
	if p.RepMin > 0 || p.RepMax > 0 {
		fmt.Fprintf(&b, "%d × %s reps", p.TargetSets, repRange(p.RepMin, p.RepMax))
	} else {
		fmt.Fprintf(&b, "%d %s", p.TargetSets, plural(p.TargetSets, "serie", "series"))
	}
	if p.TargetRPE > 0 {
		b.WriteString(" @" + kg(p.TargetRPE))
	}
	return b.String()
}

func repRange(low, high int) string {
	switch {
	case low > 0 && high > 0 && low != high:
		return fmt.Sprintf("%d-%d", low, high)
	case low > 0:
		return strconv.Itoa(low)
	default:
		return strconv.Itoa(high)
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// kg prints a number the way the app does: 80, 82.5, 8.5.
func kg(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// setText is "80 kg × 8 @9", without the RPE when there is none.
func setText(weight float64, reps int, rpe float64) string {
	s := kg(weight) + " kg × " + strconv.Itoa(reps)
	if rpe > 0 {
		s += " @" + kg(rpe)
	}
	return s
}

// decode reads the payload into T; a payload that does not fit gives ok false.
func decode[T any](payload json.RawMessage) (T, bool) {
	var v T
	err := json.Unmarshal(payload, &v)
	return v, err == nil
}

// summary builds a summariser for one payload shape. say returns "" when the
// payload lacks what the sentence needs.
func summary[T any](say func(T) string) func(json.RawMessage) string {
	return func(payload json.RawMessage) string {
		v, ok := decode[T](payload)
		if !ok {
			return ""
		}
		return say(v)
	}
}

var summaries = map[string]func(json.RawMessage) string{
	"create_plan": summary(func(in struct {
		Name  string         `json:"name"`
		Items []prescription `json:"items"`
	}) string {
		if in.Name == "" || len(in.Items) == 0 {
			return ""
		}
		items := make([]string, len(in.Items))
		for i, it := range in.Items {
			items[i] = it.Exercise + " " + it.rxText()
		}
		return fmt.Sprintf("Crear rutina «%s»: %s", in.Name, strings.Join(items, ", "))
	}),
	"save_session_as_plan": summary(func(in struct {
		SessionID int64  `json:"session_id"`
		Name      string `json:"name"`
	}) string {
		if in.Name == "" {
			return ""
		}
		return fmt.Sprintf("Crear rutina «%s» a partir de la sesión %d", in.Name, in.SessionID)
	}),
	"update_plan": summary(func(in struct {
		PlanID int64   `json:"plan_id"`
		Name   *string `json:"name"`
		Notes  *string `json:"notes"`
	}) string {
		var changes []string
		if in.Name != nil {
			changes = append(changes, "nombre «"+*in.Name+"»")
		}
		if in.Notes != nil {
			changes = append(changes, "notas «"+*in.Notes+"»")
		}
		if len(changes) == 0 {
			return ""
		}
		return fmt.Sprintf("Editar la rutina %d: %s", in.PlanID, strings.Join(changes, ", "))
	}),
	"set_plan_exercise": summary(func(in struct {
		PlanID int64 `json:"plan_id"`
		prescription
	}) string {
		if in.Exercise == "" {
			return ""
		}
		return fmt.Sprintf("Poner %s en la rutina %d: %s", in.Exercise, in.PlanID, in.rxText())
	}),
	"remove_plan_exercise": summary(func(in struct {
		PlanID   int64  `json:"plan_id"`
		Exercise string `json:"exercise"`
	}) string {
		if in.Exercise == "" {
			return ""
		}
		return fmt.Sprintf("Quitar %s de la rutina %d", in.Exercise, in.PlanID)
	}),
	"move_plan_exercise": summary(func(in struct {
		PlanID    int64  `json:"plan_id"`
		Exercise  string `json:"exercise"`
		Direction string `json:"direction"`
	}) string {
		verb := map[string]string{"up": "Subir", "down": "Bajar"}[in.Direction]
		if verb == "" || in.Exercise == "" {
			return ""
		}
		return fmt.Sprintf("%s %s en la rutina %d", verb, in.Exercise, in.PlanID)
	}),
	"delete_plan": summary(func(in struct {
		PlanID int64 `json:"plan_id"`
	}) string {
		return fmt.Sprintf("Borrar la rutina %d", in.PlanID)
	}),
	"start_session": summary(func(in struct {
		Date   string `json:"date"`
		PlanID int64  `json:"plan_id"`
	}) string {
		s := "Empezar la sesión de hoy"
		if in.Date != "" {
			s = "Empezar la sesión del " + in.Date
		}
		if in.PlanID > 0 {
			s += fmt.Sprintf(" con la rutina %d", in.PlanID)
		}
		return s
	}),
	"add_set": summary(func(in struct {
		SessionID int64   `json:"session_id"`
		Exercise  string  `json:"exercise"`
		WeightKG  float64 `json:"weight_kg"`
		Reps      int     `json:"reps"`
		RPE       float64 `json:"rpe"`
	}) string {
		if in.Exercise == "" {
			return ""
		}
		return fmt.Sprintf("Registrar %s: %s en la sesión %d", in.Exercise, setText(in.WeightKG, in.Reps, in.RPE), in.SessionID)
	}),
	"log_set": summary(func(in struct {
		Exercise string  `json:"exercise"`
		WeightKG float64 `json:"weight_kg"`
		Reps     int     `json:"reps"`
		RPE      float64 `json:"rpe"`
		Count    int     `json:"count"`
	}) string {
		if in.Exercise == "" {
			return ""
		}
		set := setText(in.WeightKG, in.Reps, in.RPE)
		if in.Count > 1 {
			set = fmt.Sprintf("%d series de %s", in.Count, set)
		}
		return fmt.Sprintf("Registrar %s: %s", in.Exercise, set)
	}),
	"log_sets": summary(func(in struct {
		Exercise string `json:"exercise"`
		Sets     []struct {
			WeightKG float64 `json:"weight_kg"`
			Reps     int     `json:"reps"`
			RPE      float64 `json:"rpe"`
		} `json:"sets"`
	}) string {
		if in.Exercise == "" || len(in.Sets) == 0 {
			return ""
		}
		sets := make([]string, len(in.Sets))
		for i, s := range in.Sets {
			sets[i] = setText(s.WeightKG, s.Reps, s.RPE)
		}
		return fmt.Sprintf("Registrar %s: %s", in.Exercise, strings.Join(sets, ", "))
	}),
	"update_set": summary(func(in struct {
		SetID     int64    `json:"set_id"`
		Exercise  *string  `json:"exercise"`
		WeightKG  *float64 `json:"weight_kg"`
		Reps      *int     `json:"reps"`
		RPE       *float64 `json:"rpe"`
		Technique *string  `json:"technique"`
	}) string {
		var changes []string
		if in.Exercise != nil {
			changes = append(changes, *in.Exercise)
		}
		if in.WeightKG != nil {
			changes = append(changes, kg(*in.WeightKG)+" kg")
		}
		if in.Reps != nil {
			changes = append(changes, strconv.Itoa(*in.Reps)+" reps")
		}
		if in.RPE != nil {
			changes = append(changes, "RPE "+kg(*in.RPE))
		}
		if in.Technique != nil {
			changes = append(changes, "técnica «"+*in.Technique+"»")
		}
		if len(changes) == 0 {
			return ""
		}
		return fmt.Sprintf("Corregir la serie %d: %s", in.SetID, strings.Join(changes, ", "))
	}),
	"delete_set": summary(func(in struct {
		SetID int64 `json:"set_id"`
	}) string {
		return fmt.Sprintf("Borrar la serie %d", in.SetID)
	}),
	"delete_session": summary(func(in struct {
		SessionID int64 `json:"session_id"`
	}) string {
		return fmt.Sprintf("Borrar la sesión %d y todas sus series", in.SessionID)
	}),
	"add_session_exercise": summary(func(in prescription) string {
		if in.Exercise == "" {
			return ""
		}
		return fmt.Sprintf("Añadir %s a la sesión: %s", in.Exercise, in.rxText())
	}),
	"adjust_session_exercise": summary(func(in struct {
		Exercise   string   `json:"exercise"`
		TargetSets *int     `json:"target_sets"`
		RepMin     *int     `json:"rep_min"`
		RepMax     *int     `json:"rep_max"`
		TargetRPE  *float64 `json:"target_rpe"`
		Skipped    *bool    `json:"skipped"`
	}) string {
		var changes []string
		if in.TargetSets != nil {
			changes = append(changes, fmt.Sprintf("%d %s", *in.TargetSets, plural(*in.TargetSets, "serie", "series")))
		}
		if in.RepMin != nil || in.RepMax != nil {
			low, high := 0, 0
			if in.RepMin != nil {
				low = *in.RepMin
			}
			if in.RepMax != nil {
				high = *in.RepMax
			}
			changes = append(changes, repRange(low, high)+" reps")
		}
		if in.TargetRPE != nil {
			changes = append(changes, "@"+kg(*in.TargetRPE))
		}
		if in.Skipped != nil {
			changes = append(changes, map[bool]string{true: "saltado", false: "retomado"}[*in.Skipped])
		}
		if in.Exercise == "" || len(changes) == 0 {
			return ""
		}
		return fmt.Sprintf("Ajustar %s: %s", in.Exercise, strings.Join(changes, ", "))
	}),
	"swap_session_exercise": summary(func(in struct {
		Exercise    string `json:"exercise"`
		Replacement string `json:"replacement"`
	}) string {
		if in.Exercise == "" || in.Replacement == "" {
			return ""
		}
		return fmt.Sprintf("Cambiar %s por %s", in.Exercise, in.Replacement)
	}),
	"remove_session_exercise": summary(func(in struct {
		Exercise string `json:"exercise"`
	}) string {
		if in.Exercise == "" {
			return ""
		}
		return fmt.Sprintf("Quitar %s de la sesión", in.Exercise)
	}),
	"set_exercise_note": summary(func(in struct {
		Exercise string `json:"exercise"`
		Note     string `json:"note"`
	}) string {
		switch {
		case in.Exercise == "":
			return ""
		case strings.TrimSpace(in.Note) == "":
			return "Borrar la nota de " + in.Exercise
		default:
			return fmt.Sprintf("Nota de %s: «%s»", in.Exercise, in.Note)
		}
	}),
	"set_exercise_group": summary(func(in struct {
		Exercise    string `json:"exercise"`
		MuscleGroup string `json:"muscle_group"`
	}) string {
		if in.Exercise == "" || in.MuscleGroup == "" {
			return ""
		}
		return fmt.Sprintf("Contar %s en el grupo %s", in.Exercise, in.MuscleGroup)
	}),
	"record_feedback": summary(func(in struct {
		MuscleGroup string `json:"muscle_group"`
		Fatigue     int    `json:"fatigue"`
		Pump        int    `json:"pump"`
		Recovery    int    `json:"recovery"`
	}) string {
		if in.MuscleGroup == "" {
			return ""
		}
		return fmt.Sprintf("Valorar %s: fatiga %d, bombeo %d, recuperación %d", in.MuscleGroup, in.Fatigue, in.Pump, in.Recovery)
	}),
	"set_volume_landmarks": summary(func(in struct {
		MuscleGroup string `json:"muscle_group"`
		MEV         int    `json:"mev"`
		MRV         int    `json:"mrv"`
	}) string {
		if in.MuscleGroup == "" {
			return ""
		}
		return fmt.Sprintf("Fijar los límites de %s: MEV %d, MRV %d series por semana", in.MuscleGroup, in.MEV, in.MRV)
	}),
	"set_rest_target": summary(func(in struct {
		RestSeconds int `json:"rest_seconds"`
	}) string {
		if in.RestSeconds <= 0 {
			return ""
		}
		return fmt.Sprintf("Descanso objetivo: %d:%02d", in.RestSeconds/60, in.RestSeconds%60)
	}),
}
