package websrv_test

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

type suggestion struct {
	Exercise string  `json:"exercise"`
	N        int     `json:"n"`
	Weight   float64 `json:"weight"`
	Reps     int     `json:"reps"`
	RPE      float64 `json:"rpe"`
}

// TestCoachSuggestionsReadTheNumbersOfAnAnswer runs static/coach-apply.js under
// node: the Aplicar chips come only from weights the answer spells out.
func TestCoachSuggestionsReadTheNumbersOfAnAnswer(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed; the Aplicar parser is untested here")
	}
	exercises := []string{"press banca", "press militar"}
	cases := []struct {
		text string
		want []suggestion
	}{
		{"Para la 3ª, quédate en **45 kg × 8** y mantén RIR 2.", []suggestion{{N: 3, Weight: 45, Reps: 8, RPE: 8}}},
		{"Sube a 47,5 kg x 6 @8,5 en press militar.", []suggestion{{Exercise: "press militar", Weight: 47.5, Reps: 6, RPE: 8.5}}},
		{"En press banca haz 70 kg a 8 reps, RPE 8.", []suggestion{{Exercise: "press banca", Weight: 70, Reps: 8, RPE: 8}}},
		{"Serie 2: 60kg×10.", []suggestion{{N: 2, Weight: 60, Reps: 10}}},
		{"Baja a 40 kg en la siguiente.", []suggestion{{Weight: 40}}},
		// Totals and history are not instructions.
		{"Hoy llevas 3.200 kg de volumen. La semana pasada hiciste 8 series.", nil},
		{"Descansa 2 minutos y bebe agua.", nil},
		{"Hiciste 47,5 kg × 7, una por debajo del rango.", nil},
		// Two suggestions, and the same one twice is offered once.
		{"Primero 45 kg × 8; si va bien, 47,5 kg × 6. Repito: 45 kg × 8.", []suggestion{{Weight: 45, Reps: 8}, {Weight: 47.5, Reps: 6}}},
	}
	type input struct {
		Text      string   `json:"text"`
		Exercises []string `json:"exercises"`
	}
	in := make([]input, len(cases))
	for i, c := range cases {
		in[i] = input{c.text, exercises}
	}
	args, _ := json.Marshal(in)
	script, _ := filepath.Abs("static/coach-apply.js")
	harness, _ := filepath.Abs("testdata/apply_harness.mjs")
	cmd := exec.Command(node, harness, script, string(args))
	cmd.Dir = t.TempDir()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("harness: %v\n%s", err, out)
	}
	var got [][]suggestion
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	for i, c := range cases {
		if len(got[i]) == 0 && len(c.want) == 0 {
			continue
		}
		if !reflect.DeepEqual(got[i], c.want) {
			t.Errorf("%q\n got %+v\nwant %+v", c.text, got[i], c.want)
		}
	}
}
