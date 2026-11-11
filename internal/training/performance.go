package training

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// The performance engine estimates apparent performance from what was logged:
// load, reps and, when stated, RPE. It is a device-free proxy. It never measures
// bar velocity, absolute force or physiological fatigue, never changes a load or
// a plan, and abstains when the data cannot support a comparison. Nothing it
// computes is stored. See docs/performance-engine.md for the contract.

// Estimator identifiers. A formula change gets a new identifier, so a number
// can always be traced to the formula that produced it.
const (
	EstimatorEpley    = "epley_v1"
	EstimatorRIREpley = "rir_epley_v1"
	EstimatorBrzycki  = "brzycki_v1"
)

// Confidence levels. There is no "high": without a measuring device every
// estimate is a proxy.
const (
	ConfidenceNone     = "none"
	ConfidenceLow      = "low"
	ConfidenceModerate = "moderate"
)

// Reasons an estimator gives no value for a set.
const (
	UnavailableNoLoadOrReps   = "no_load_or_reps"
	UnavailableRPENotObserved = "rpe_not_observed"
	UnavailableRPEOutOfRange  = "rpe_out_of_rir_range"
	UnavailableRepsOutOfRange = "reps_out_of_range"
)

// Reasons a set is left out of a drop-off comparison.
const (
	ExcludedTechnique      = "technique"
	ExcludedWarmup         = "warmup"
	ExcludedNoLoadOrReps   = "no_load_or_reps"
	ExcludedProbableWarmup = "probable_warmup"
)

const (
	// maxReliableReps bounds the range the rep-based formulas are trusted in;
	// beyond it Epley is flagged low confidence and Brzycki refuses.
	maxReliableReps = 12
	// minRIRRPE is the lowest RPE read as reps in reserve: RIR 4. Further from
	// failure, people misjudge reserve too much for the RIR model to use it.
	minRIRRPE = 6
	// warmupFraction marks a set lighter than this share of the session's top
	// straight load, logged before it, as a probable warm-up.
	warmupFraction = 0.6
)

// EstimatorInfo documents one estimator: its identifier, formula, units and the
// rep range it is trusted in.
type EstimatorInfo struct {
	ID       string `json:"id"`
	Formula  string `json:"formula"`
	Unit     string `json:"unit"`
	RepRange string `json:"rep_range"`
	Requires string `json:"requires,omitempty"`
}

// Estimators lists the estimators the engine uses, in report order.
func Estimators() []EstimatorInfo {
	return []EstimatorInfo{
		{ID: EstimatorEpley, Formula: "w*(1+reps/30); w when reps=1", Unit: "kg", RepRange: "1-12 (beyond: low confidence)"},
		{ID: EstimatorRIREpley, Formula: "w*(1+(reps+10-RPE)/30)", Unit: "kg", RepRange: "reps+RIR 1-12 (beyond: low confidence)", Requires: "observed RPE 6-10"},
		{ID: EstimatorBrzycki, Formula: "w*36/(37-reps)", Unit: "kg", RepRange: "1-12 (beyond: refused)"},
	}
}

// SetEstimate is one estimator's apparent 1RM for one set, in kg rounded to
// 0.1. Unavailable names why there is no value. DropOffPct is the apparent drop
// against the reference set, rounded to 0.1%: positive is lower apparent
// performance. DropOffRangePct, for the RIR model only, is the drop if the
// stated RPE were one point off either way.
type SetEstimate struct {
	Model           string      `json:"model"`
	ValueKG         float64     `json:"value_kg,omitempty"`
	Confidence      string      `json:"confidence"`
	Unavailable     string      `json:"unavailable,omitempty"`
	DropOffPct      *float64    `json:"dropoff_pct,omitempty"`
	DropOffRangePct *[2]float64 `json:"dropoff_range_pct,omitempty"`

	raw float64
}

// EstimatePerformance runs every estimator on one set. rpeObserved is false
// when the RPE is a placeholder nobody stated; such an RPE never feeds the RIR
// model.
func EstimatePerformance(weightKG float64, reps int, rpe float64, rpeObserved bool) []SetEstimate {
	return []SetEstimate{
		epley(weightKG, reps),
		rirEpley(weightKG, reps, rpe, rpeObserved),
		brzycki(weightKG, reps),
	}
}

func epley(w float64, reps int) SetEstimate {
	e := SetEstimate{Model: EstimatorEpley}
	if w <= 0 || reps <= 0 {
		return unavailable(e, UnavailableNoLoadOrReps)
	}
	e.raw = epleyRaw(w, float64(reps))
	return valued(e, reps)
}

func epleyRaw(w, reps float64) float64 {
	if reps == 1 {
		return w
	}
	return w * (1 + reps/30)
}

func rirEpley(w float64, reps int, rpe float64, observed bool) SetEstimate {
	e := SetEstimate{Model: EstimatorRIREpley}
	switch {
	case w <= 0 || reps <= 0:
		return unavailable(e, UnavailableNoLoadOrReps)
	case !observed || rpe <= 0:
		return unavailable(e, UnavailableRPENotObserved)
	case rpe < minRIRRPE || rpe > 10:
		return unavailable(e, UnavailableRPEOutOfRange)
	}
	effective := float64(reps) + 10 - rpe
	e.raw = w * (1 + effective/30)
	return valued(e, int(math.Ceil(effective)))
}

func brzycki(w float64, reps int) SetEstimate {
	e := SetEstimate{Model: EstimatorBrzycki}
	switch {
	case w <= 0 || reps <= 0:
		return unavailable(e, UnavailableNoLoadOrReps)
	case reps > maxReliableReps:
		return unavailable(e, UnavailableRepsOutOfRange)
	}
	e.raw = w * 36 / (37 - float64(reps))
	return valued(e, reps)
}

func unavailable(e SetEstimate, reason string) SetEstimate {
	e.Unavailable, e.Confidence = reason, ConfidenceNone
	return e
}

func valued(e SetEstimate, reps int) SetEstimate {
	e.ValueKG = round1(e.raw)
	e.Confidence = ConfidenceModerate
	if reps > maxReliableReps {
		e.Confidence = ConfidenceLow
	}
	return e
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }

// DropOffSet is one logged set as the comparison saw it. An excluded set says
// why and carries no estimates.
type DropOffSet struct {
	SetID          int64         `json:"set_id"`
	Position       int           `json:"position"`
	WeightKG       float64       `json:"weight_kg"`
	Reps           int           `json:"reps"`
	RPE            float64       `json:"rpe,omitempty"`
	RPEObserved    bool          `json:"rpe_observed"`
	Technique      string        `json:"technique,omitempty"`
	Comparable     bool          `json:"comparable"`
	ExcludedReason string        `json:"excluded_reason,omitempty"`
	Reference      bool          `json:"reference,omitempty"`
	Estimates      []SetEstimate `json:"estimates,omitempty"`
}

// ModelSummary is one estimator's verdict over the comparison: how many sets it
// could compare (the reference included), its largest apparent drop and how far
// to trust it. A model with fewer than two comparable sets abstains.
type ModelSummary struct {
	Model         string   `json:"model"`
	ComparedSets  int      `json:"compared_sets"`
	MaxDropOffPct *float64 `json:"max_dropoff_pct,omitempty"`
	Confidence    string   `json:"confidence"`
	Note          string   `json:"note,omitempty"`
}

// DropOffAnalysis is the apparent per-set performance drop of one exercise
// within one session.
type DropOffAnalysis struct {
	Exercise       string          `json:"exercise"`
	SessionID      int64           `json:"session_id,omitempty"`
	Date           string          `json:"date,omitempty"`
	Unit           string          `json:"unit"`
	Comparable     bool            `json:"comparable"`
	ReferenceSetID int64           `json:"reference_set_id,omitempty"`
	Sets           []DropOffSet    `json:"sets"`
	Models         []ModelSummary  `json:"models"`
	Estimators     []EstimatorInfo `json:"estimators"`
	Assumptions    []string        `json:"assumptions"`
	Explanation    string          `json:"explanation"`
}

// dropOffAssumptions are stated with every analysis, because each one is a way
// the number can be wrong.
var dropOffAssumptions = []string{
	"Mismo nombre de ejercicio = mismo equipo y configuración: si dos máquinas comparten nombre, regístralas con nombres distintos.",
	"Pesos en kg tal como se guardaron; la auditoría lb/kg del histórico no está cerrada, así que no se calibra con datos antiguos.",
	"La referencia es la primera serie comparable de la sesión; caída positiva = rendimiento aparente menor.",
	"Un RPE no indicado (rpe_imputed) nunca se usa como RIR.",
	"Series lanzadas antes de la carga máxima y por debajo del 60% de ella se tratan como calentamiento probable.",
}

// AnalyzeDropOff compares the sets of one exercise logged in one session. Sets
// are ordered by position. A set is comparable when it is a working straight
// set (not a warm-up, no technique) with load and reps and is not a probable
// warm-up. The first
// comparable set is the reference; each estimator reports every later set's
// apparent drop against it, or abstains. It is pure: no store, no clock.
func AnalyzeDropOff(exercise string, sets []Set) DropOffAnalysis {
	ordered := append([]Set(nil), sets...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Position < ordered[j].Position })

	out := DropOffAnalysis{
		Exercise:    exercise,
		Unit:        "kg",
		Sets:        make([]DropOffSet, 0, len(ordered)),
		Estimators:  Estimators(),
		Assumptions: dropOffAssumptions,
	}
	warmups := probableWarmups(ordered)
	ref := -1
	comparable := 0
	for i, s := range ordered {
		d := DropOffSet{
			SetID: s.ID, Position: s.Position, WeightKG: s.WeightKG, Reps: s.Reps,
			RPEObserved: !s.RPEImputed && s.RPE > 0, Technique: s.Technique,
		}
		if d.RPEObserved {
			d.RPE = s.RPE
		}
		switch {
		case s.Category == SetWarmup:
			d.ExcludedReason = ExcludedWarmup
		case strings.TrimSpace(s.Technique) != "":
			d.ExcludedReason = ExcludedTechnique
		case s.WeightKG <= 0 || s.Reps <= 0:
			d.ExcludedReason = ExcludedNoLoadOrReps
		case warmups[i]:
			d.ExcludedReason = ExcludedProbableWarmup
		default:
			d.Comparable = true
			d.Estimates = EstimatePerformance(s.WeightKG, s.Reps, s.RPE, d.RPEObserved)
			comparable++
			if ref < 0 {
				ref = len(out.Sets)
				d.Reference = true
				out.ReferenceSetID = s.ID
			}
		}
		out.Sets = append(out.Sets, d)
	}
	out.Comparable = comparable >= 2
	for mi, info := range out.Estimators {
		out.Models = append(out.Models, summarize(&out, ref, mi, info.ID))
	}
	out.Explanation = explain(out)
	return out
}

// probableWarmups marks straight sets logged before the first set at the
// session's top load and lighter than warmupFraction of it.
func probableWarmups(sets []Set) map[int]bool {
	top, topAt := 0.0, -1
	for i, s := range sets {
		if s.Technique == "" && s.Category != SetWarmup && s.Reps > 0 && s.WeightKG > top {
			top, topAt = s.WeightKG, i
		}
	}
	out := map[int]bool{}
	for i := 0; i < topAt; i++ {
		if sets[i].WeightKG < top*warmupFraction {
			out[i] = true
		}
	}
	return out
}

// summarize fills one model's drop-offs on the comparable sets and returns its
// summary. Estimates are positional: model mi is Estimates[mi] on every set.
func summarize(a *DropOffAnalysis, ref, mi int, model string) ModelSummary {
	m := ModelSummary{Model: model, Confidence: ConfidenceNone}
	if ref < 0 {
		m.Note = "Sin series comparables."
		return m
	}
	base := a.Sets[ref].Estimates[mi]
	if base.Unavailable != "" {
		m.Note = fmt.Sprintf("La serie de referencia no admite este modelo (%s).", base.Unavailable)
		return m
	}
	low := base.Confidence == ConfidenceLow
	var maxDrop *float64
	m.ComparedSets = 1
	for i := range a.Sets {
		s := &a.Sets[i]
		if !s.Comparable || i == ref {
			continue
		}
		e := &s.Estimates[mi]
		if e.Unavailable != "" {
			continue
		}
		m.ComparedSets++
		low = low || e.Confidence == ConfidenceLow
		if !a.Comparable {
			continue
		}
		d := dropPct(e.raw, base.raw)
		e.DropOffPct = &d
		if model == EstimatorRIREpley {
			e.DropOffRangePct = rirRange(s, base.raw)
		}
		if maxDrop == nil || d > *maxDrop {
			maxDrop = &d
		}
	}
	if m.ComparedSets < 2 || !a.Comparable {
		m.Note = "Menos de dos series comparables para este modelo: no hay caída que calcular."
		return m
	}
	m.MaxDropOffPct = maxDrop
	m.Confidence = ConfidenceModerate
	if low {
		m.Confidence = ConfidenceLow
		m.Note = "Alguna serie queda fuera de 1-12 repeticiones efectivas: el estimador es menos fiable ahí."
	}
	return m
}

func dropPct(value, reference float64) float64 {
	d := round1(100 * (1 - value/reference))
	if d == 0 {
		return 0 // no negative zero in the output
	}
	return d
}

// rirRange is the drop if the set's stated RPE were one point lower or higher,
// with reps in reserve kept within 0-4.
func rirRange(s *DropOffSet, reference float64) *[2]float64 {
	var drops []float64
	for _, delta := range []float64{-1, 1} {
		rpe := math.Min(10, math.Max(minRIRRPE, s.RPE+delta))
		v := s.WeightKG * (1 + (float64(s.Reps)+10-rpe)/30)
		drops = append(drops, dropPct(v, reference))
	}
	sort.Float64s(drops)
	return &[2]float64{drops[0], drops[1]}
}

func explain(a DropOffAnalysis) string {
	const disclaimer = " Es una estimación sin sensor: no mide velocidad, fuerza ni fatiga, y no cambia ninguna carga."
	excluded := 0
	for _, s := range a.Sets {
		if !s.Comparable {
			excluded++
		}
	}
	if !a.Comparable {
		return fmt.Sprintf("%s: menos de dos series comparables (%d registradas, %d excluidas), así que no calculo caída.%s",
			a.Exercise, len(a.Sets), excluded, disclaimer)
	}
	var ref DropOffSet
	for _, s := range a.Sets {
		if s.Reference {
			ref = s
		}
	}
	text := fmt.Sprintf("%s: %d series comparables (%d excluidas); referencia %g kg x%d.",
		a.Exercise, len(a.Sets)-excluded, excluded, round1(ref.WeightKG), ref.Reps)
	for _, m := range a.Models {
		if m.MaxDropOffPct != nil {
			text += fmt.Sprintf(" %s: caída aparente máxima %g%% (confianza %s, %d series).", m.Model, *m.MaxDropOffPct, m.Confidence, m.ComparedSets)
		} else {
			text += fmt.Sprintf(" %s: se abstiene.", m.Model)
		}
	}
	return text + disclaimer
}
