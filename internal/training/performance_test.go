package training

import (
	"math"
	"strings"
	"testing"
)

const lb = 0.45359237

func set(id int64, weightKG float64, reps int, rpe float64) Set {
	return Set{ID: id, Position: int(id), Exercise: "remo", WeightKG: weightKG, Reps: reps, RPE: rpe}
}

func imputed(s Set) Set {
	s.RPE, s.RPEImputed = ImputedRPE, true
	return s
}

func estimateOf(t *testing.T, s DropOffSet, model string) SetEstimate {
	t.Helper()
	for _, e := range s.Estimates {
		if e.Model == model {
			return e
		}
	}
	t.Fatalf("set %d has no %s estimate: %#v", s.SetID, model, s.Estimates)
	return SetEstimate{}
}

func modelOf(t *testing.T, a DropOffAnalysis, model string) ModelSummary {
	t.Helper()
	for _, m := range a.Models {
		if m.Model == model {
			return m
		}
	}
	t.Fatalf("analysis has no %s model: %#v", model, a.Models)
	return ModelSummary{}
}

func dropOf(t *testing.T, s DropOffSet, model string) float64 {
	t.Helper()
	e := estimateOf(t, s, model)
	if e.DropOffPct == nil {
		t.Fatalf("set %d has no %s drop-off: %#v", s.SetID, model, e)
	}
	return *e.DropOffPct
}

func TestEstimatorsFormulasAndBounds(t *testing.T) {
	for _, tt := range []struct {
		name        string
		weight      float64
		reps        int
		rpe         float64
		observed    bool
		model       string
		want        float64
		unavailable string
		confidence  string
	}{
		{"epley single is the lifted weight", 100, 1, 10, true, EstimatorEpley, 100, "", ConfidenceModerate},
		{"epley ten reps", 100, 10, 8, true, EstimatorEpley, 133.3, "", ConfidenceModerate},
		{"epley beyond twelve reps is low confidence", 100, 15, 8, true, EstimatorEpley, 150, "", ConfidenceLow},
		{"rir epley adds reps in reserve", 100, 8, 8, true, EstimatorRIREpley, 133.3, "", ConfidenceModerate},
		{"rir epley at failure equals epley", 100, 6, 10, true, EstimatorRIREpley, 120, "", ConfidenceModerate},
		{"rir epley refuses an imputed rpe", 100, 8, ImputedRPE, false, EstimatorRIREpley, 0, UnavailableRPENotObserved, ConfidenceNone},
		{"rir epley refuses an rpe far from failure", 100, 8, 5, true, EstimatorRIREpley, 0, UnavailableRPEOutOfRange, ConfidenceNone},
		{"rir epley flags a far reach as low confidence", 100, 10, 6, true, EstimatorRIREpley, 146.7, "", ConfidenceLow},
		{"brzycki six reps", 100, 6, 8, true, EstimatorBrzycki, 116.1, "", ConfidenceModerate},
		{"brzycki single is the lifted weight", 100, 1, 10, true, EstimatorBrzycki, 100, "", ConfidenceModerate},
		{"brzycki refuses beyond twelve reps", 100, 13, 8, true, EstimatorBrzycki, 0, UnavailableRepsOutOfRange, ConfidenceNone},
		{"no load estimates nothing", 0, 8, 8, true, EstimatorEpley, 0, UnavailableNoLoadOrReps, ConfidenceNone},
		{"no reps estimates nothing", 100, 0, 8, true, EstimatorEpley, 0, UnavailableNoLoadOrReps, ConfidenceNone},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var got SetEstimate
			for _, e := range EstimatePerformance(tt.weight, tt.reps, tt.rpe, tt.observed) {
				if e.Model == tt.model {
					got = e
				}
			}
			if got.Model == "" {
				t.Fatalf("no %s estimate", tt.model)
			}
			if got.ValueKG != tt.want || got.Unavailable != tt.unavailable || got.Confidence != tt.confidence {
				t.Fatalf("got %#v, want value %v unavailable %q confidence %q", got, tt.want, tt.unavailable, tt.confidence)
			}
		})
	}
}

func TestEstimatorsAreIdentifiedWithFormulaAndUnits(t *testing.T) {
	for _, e := range Estimators() {
		if e.ID == "" || e.Formula == "" || e.Unit != "kg" || e.RepRange == "" {
			t.Fatalf("estimator %#v lacks an id, formula, unit or range", e)
		}
	}
}

// The 2026-11-06 row: 130 lb x10/10/9 @8. Equal load and effort with one rep
// fewer reads as a small apparent drop; it is a proxy, not a measurement.
func TestDropOffDecliningRepsAtEqualLoadAndEffort(t *testing.T) {
	w := 130 * lb
	a := AnalyzeDropOff("remo", []Set{set(1, w, 10, 8), set(2, w, 10, 8), set(3, w, 9, 8)})
	if !a.Comparable || a.ReferenceSetID != 1 || a.Unit != "kg" {
		t.Fatalf("analysis = %#v", a)
	}
	if d := dropOf(t, a.Sets[1], EstimatorEpley); d != 0 {
		t.Fatalf("equal sets drop = %v, want 0", d)
	}
	if d := dropOf(t, a.Sets[2], EstimatorEpley); d != 2.5 {
		t.Fatalf("epley drop = %v, want 2.5", d)
	}
	if d := dropOf(t, a.Sets[2], EstimatorRIREpley); d != 2.4 {
		t.Fatalf("rir epley drop = %v, want 2.4", d)
	}
	// One RIR either way on the compared set moves the estimate by w/30.
	r := estimateOf(t, a.Sets[2], EstimatorRIREpley).DropOffRangePct
	if r == nil || r[0] != 0 || r[1] != 4.8 {
		t.Fatalf("rir sensitivity = %v, want [0 4.8]", r)
	}
	if m := modelOf(t, a, EstimatorEpley); m.Confidence != ConfidenceModerate || m.ComparedSets != 3 || m.MaxDropOffPct == nil || *m.MaxDropOffPct != 2.5 {
		t.Fatalf("epley summary = %#v", m)
	}
	if !strings.Contains(a.Explanation, "no mide") {
		t.Fatalf("explanation does not disclaim measurement: %q", a.Explanation)
	}
}

// The 2026-11-06 chest fly: 220 lb x6 @8, then 190 lb x6 @8, then a third set
// whose RPE nobody stated. Varying load is what the estimators normalize; the
// unstated RPE keeps the RIR model off that set but not off the others.
func TestDropOffVaryingLoadAndAMissingRPE(t *testing.T) {
	fly := []Set{set(1, 220*lb, 6, 8), set(2, 190*lb, 6, 8), imputed(set(3, 190*lb, 6, 0))}
	a := AnalyzeDropOff("aperturas", fly)
	if d := dropOf(t, a.Sets[1], EstimatorEpley); d != 13.6 {
		t.Fatalf("epley drop = %v, want 13.6", d)
	}
	third := estimateOf(t, a.Sets[2], EstimatorRIREpley)
	if third.Unavailable != UnavailableRPENotObserved || third.DropOffPct != nil {
		t.Fatalf("imputed set got an RIR estimate: %#v", third)
	}
	if !a.Sets[2].Comparable || a.Sets[2].RPEObserved {
		t.Fatalf("third set = %#v", a.Sets[2])
	}
	if m := modelOf(t, a, EstimatorRIREpley); m.ComparedSets != 2 {
		t.Fatalf("rir epley compared %d sets, want 2", m.ComparedSets)
	}
}

func TestDropOffSameLoadWithMoreRepsIsANegativeDrop(t *testing.T) {
	a := AnalyzeDropOff("remo", []Set{set(1, 60, 8, 9), set(2, 60, 9, 9)})
	if d := dropOf(t, a.Sets[1], EstimatorEpley); d >= 0 {
		t.Fatalf("drop = %v, want negative", d)
	}
}

func TestDropOffExcludesNonComparableSets(t *testing.T) {
	drop := set(4, 40, 10, 10)
	drop.Technique = "drop set"
	assisted := set(5, 60, 8, 9)
	assisted.Technique = "asistida"
	a := AnalyzeDropOff("remo", []Set{set(1, 20, 12, 5), set(2, 60, 8, 8), set(3, 60, 7, 8), drop, assisted, set(6, 0, 10, 8)})
	want := map[int64]string{1: ExcludedProbableWarmup, 4: ExcludedTechnique, 5: ExcludedTechnique, 6: ExcludedNoLoadOrReps}
	for _, s := range a.Sets {
		if s.Comparable == (want[s.SetID] != "") || s.ExcludedReason != want[s.SetID] {
			t.Fatalf("set %d = %#v, want excluded %q", s.SetID, s, want[s.SetID])
		}
		if !s.Comparable && len(s.Estimates) != 0 {
			t.Fatalf("excluded set %d carries estimates", s.SetID)
		}
	}
	if a.ReferenceSetID != 2 {
		t.Fatalf("reference = %d, want the first working set", a.ReferenceSetID)
	}
}

func TestDropOffAbstainsWithFewerThanTwoComparableSets(t *testing.T) {
	drop := set(2, 40, 10, 10)
	drop.Technique = "drop set"
	for name, sets := range map[string][]Set{
		"none":       nil,
		"one":        {set(1, 60, 8, 8)},
		"one usable": {set(1, 60, 8, 8), drop},
	} {
		t.Run(name, func(t *testing.T) {
			a := AnalyzeDropOff("remo", sets)
			if a.Comparable {
				t.Fatalf("analysis = %#v", a)
			}
			for _, m := range a.Models {
				if m.Confidence != ConfidenceNone || m.MaxDropOffPct != nil {
					t.Fatalf("model %#v did not abstain", m)
				}
			}
			for _, s := range a.Sets {
				for _, e := range s.Estimates {
					if e.DropOffPct != nil {
						t.Fatalf("abstained analysis reports a drop: %#v", e)
					}
				}
			}
			if !strings.Contains(a.Explanation, "comparables") {
				t.Fatalf("explanation = %q", a.Explanation)
			}
		})
	}
}

// A reference whose RPE was never stated leaves the RIR model with no baseline:
// it abstains rather than borrowing a later set as the reference.
func TestDropOffRIRModelAbstainsWhenTheReferenceRPEIsImputed(t *testing.T) {
	a := AnalyzeDropOff("remo", []Set{imputed(set(1, 60, 8, 0)), set(2, 60, 7, 8), set(3, 60, 6, 8)})
	m := modelOf(t, a, EstimatorRIREpley)
	if m.Confidence != ConfidenceNone || m.MaxDropOffPct != nil || m.Note == "" {
		t.Fatalf("rir summary = %#v", m)
	}
	if modelOf(t, a, EstimatorEpley).Confidence != ConfidenceModerate {
		t.Fatal("the imputed RPE disabled the Epley baseline")
	}
}

func TestDropOffHighRepsLowerTheModelConfidence(t *testing.T) {
	a := AnalyzeDropOff("remo", []Set{set(1, 40, 15, 8), set(2, 40, 14, 8)})
	if m := modelOf(t, a, EstimatorEpley); m.Confidence != ConfidenceLow {
		t.Fatalf("epley summary = %#v", m)
	}
	if m := modelOf(t, a, EstimatorBrzycki); m.Confidence != ConfidenceNone {
		t.Fatalf("brzycki summary = %#v", m)
	}
}

func TestDropOffKeepsTheGivenOrderByPosition(t *testing.T) {
	a := AnalyzeDropOff("remo", []Set{set(2, 60, 7, 8), set(1, 60, 8, 8)})
	if a.ReferenceSetID != 1 || a.Sets[0].SetID != 1 {
		t.Fatalf("sets not ordered by position: %#v", a)
	}
}

func TestDropOffNeverMentionsVelocityOrForce(t *testing.T) {
	a := AnalyzeDropOff("remo", []Set{set(1, 60, 8, 8), set(2, 60, 7, 8)})
	text := strings.ToLower(a.Explanation + strings.Join(a.Assumptions, " "))
	for _, banned := range []string{"m/s", "velocidad medida", "fuerza absoluta"} {
		if strings.Contains(text, banned) && !strings.Contains(text, "no mide") {
			t.Fatalf("output claims %q: %s", banned, text)
		}
	}
	if math.IsNaN(*modelOf(t, a, EstimatorEpley).MaxDropOffPct) {
		t.Fatal("NaN drop")
	}
}
