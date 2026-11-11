package websrv

import "testing"

// Every write tool's proposal reads as one sentence built from its payload.
func TestProposalSummaryPerTool(t *testing.T) {
	cases := []struct{ tool, payload, want string }{
		{"create_plan", `{"name":"Pierna coach","items":[{"exercise":"sentadilla","target_sets":3},{"exercise":"prensa","target_sets":4,"rep_min":8,"rep_max":12,"target_rpe":8.5}]}`,
			"Crear rutina «Pierna coach»: sentadilla 3 series, prensa 4 × 8-12 reps @8.5"},
		{"save_session_as_plan", `{"session_id":7,"name":"Torso"}`, "Crear rutina «Torso» a partir de la sesión 7"},
		{"update_plan", `{"plan_id":3,"name":"Torso B","notes":"más volumen"}`, "Editar la rutina 3: nombre «Torso B», notas «más volumen»"},
		{"set_plan_exercise", `{"plan_id":3,"exercise":"banca","target_sets":3,"rep_min":8,"rep_max":8}`, "Poner banca en la rutina 3: 3 × 8 reps"},
		{"remove_plan_exercise", `{"plan_id":3,"exercise":"banca"}`, "Quitar banca de la rutina 3"},
		{"move_plan_exercise", `{"plan_id":3,"exercise":"banca","direction":"up"}`, "Subir banca en la rutina 3"},
		{"move_plan_exercise", `{"plan_id":3,"exercise":"banca","direction":"down"}`, "Bajar banca en la rutina 3"},
		{"delete_plan", `{"plan_id":3}`, "Borrar la rutina 3"},
		{"start_session", `{}`, "Empezar la sesión de hoy"},
		{"start_session", `{"date":"2026-11-12","plan_id":3}`, "Empezar la sesión del 2026-11-12 con la rutina 3"},
		{"add_set", `{"session_id":5,"exercise":"banca","weight_kg":82.5,"reps":8,"rpe":8}`, "Registrar banca: 82.5 kg × 8 @8 en la sesión 5"},
		{"log_set", `{"exercise":"banca","weight_kg":80,"reps":8,"rpe":9,"count":3}`, "Registrar banca: 3 series de 80 kg × 8 @9"},
		{"log_set", `{"exercise":"banca","weight_kg":80,"reps":8,"rpe":0}`, "Registrar banca: 80 kg × 8"},
		{"log_sets", `{"idempotency_key":"k","exercise":"remo","sets":[{"weight_kg":60,"reps":12},{"weight_kg":60,"reps":10,"rpe":9}]}`, "Registrar remo: 60 kg × 12, 60 kg × 10 @9"},
		{"update_set", `{"set_id":9,"weight_kg":85,"reps":6}`, "Corregir la serie 9: 85 kg, 6 reps"},
		{"delete_set", `{"set_id":9}`, "Borrar la serie 9"},
		{"delete_session", `{"session_id":5}`, "Borrar la sesión 5 y todas sus series"},
		{"add_session_exercise", `{"session_id":5,"exercise":"fondos","target_sets":2}`, "Añadir fondos a la sesión: 2 series"},
		{"adjust_session_exercise", `{"session_id":5,"exercise":"fondos","target_sets":3,"skipped":true}`, "Ajustar fondos: 3 series, saltado"},
		{"swap_session_exercise", `{"session_id":5,"exercise":"banca","replacement":"press inclinado"}`, "Cambiar banca por press inclinado"},
		{"remove_session_exercise", `{"session_id":5,"exercise":"fondos"}`, "Quitar fondos de la sesión"},
		{"set_exercise_note", `{"exercise":"banca","note":"asiento 4"}`, "Nota de banca: «asiento 4»"},
		{"set_exercise_note", `{"exercise":"banca","note":""}`, "Borrar la nota de banca"},
		{"set_exercise_group", `{"exercise":"banca","muscle_group":"pecho"}`, "Contar banca en el grupo pecho"},
		{"record_feedback", `{"session_id":5,"muscle_group":"pecho","fatigue":2,"pump":1,"recovery":3}`, "Valorar pecho: fatiga 2, bombeo 1, recuperación 3"},
		{"set_volume_landmarks", `{"muscle_group":"pecho","mev":8,"mrv":20}`, "Fijar los límites de pecho: MEV 8, MRV 20 series por semana"},
		{"set_rest_target", `{"rest_seconds":150}`, "Descanso objetivo: 2:30"},
		{"memory_save", `{"type":"limitation","title":"Hombro","body":"Duele en press militar"}`, "Recordar (limitación) «Hombro»: Duele en press militar"},
		{"memory_save", `{"title":"Agarre","body":"Prefiere agarre neutro"}`, "Recordar (nota) «Agarre»: Prefiere agarre neutro"},
		{"memory_forget", `{"memory_id":4}`, "Olvidar el recuerdo 4"},
	}
	for _, c := range cases {
		if got := proposalSummary(c.tool, []byte(c.payload)); got != c.want {
			t.Errorf("%s %s\n got %q\nwant %q", c.tool, c.payload, got, c.want)
		}
	}
}

// Anything not understood falls back to the key/value view: no summary.
func TestProposalSummaryFallsBack(t *testing.T) {
	for _, c := range []struct{ tool, payload string }{
		{"a_future_tool", `{"x":1}`},
		{"create_plan", `not json`},
		{"create_plan", `{"items":[]}`},
		{"add_set", `{"weight_kg":"heavy"}`},
	} {
		if got := proposalSummary(c.tool, []byte(c.payload)); got != "" {
			t.Errorf("%s %s summarised as %q", c.tool, c.payload, got)
		}
	}
}
