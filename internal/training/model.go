package training

import (
	"errors"
	"math"
	"time"
)

var (
	ErrValidation = errors.New("validation error")
	ErrNotFound   = errors.New("not found")
)

type Session struct {
	ID      int64   `json:"id"`
	Date    string  `json:"date"`
	Sets    []Set   `json:"sets,omitempty"`
	TotalSI float64 `json:"total_si"`
	// PlanID and PlanName record the plan the session followed. The name is a
	// snapshot so renaming or deleting a plan never rewrites past sessions.
	PlanID   int64  `json:"plan_id,omitempty"`
	PlanName string `json:"plan_name,omitempty"`
}
type Set struct {
	ID        int64   `json:"id"`
	SessionID int64   `json:"session_id"`
	Position  int     `json:"position"`
	Exercise  string  `json:"exercise"`
	WeightKG  float64 `json:"weight_kg"`
	Reps      int     `json:"reps"`
	RPE       float64 `json:"rpe"`
	SI        float64 `json:"si"`
	// Technique is an optional intensity method applied to this set. Empty
	// means a normal straight set.
	Technique string `json:"technique,omitempty"`
}

// Techniques are the intensity methods offered as suggestions. The field is not
// restricted to them — reality invents more — but suggesting a known list is
// what keeps the vocabulary from fragmenting the way exercise names did.
var Techniques = []string{
	"drop set", "rest-pause", "myo-reps", "cluster", "parciales",
	"negativas", "isométrico", "sin parar", "superserie", "asistida",
}

const maxTechniqueLen = 40

type SessionSummary struct {
	ID       int64   `json:"id"`
	Date     string  `json:"date"`
	SetCount int     `json:"set_count"`
	TotalSI  float64 `json:"total_si"`
	PlanName string  `json:"plan_name,omitempty"`
}
type AddSetInput struct {
	SessionID int64
	Exercise  string
	WeightKG  float64
	Reps      int
	RPE       float64
	Technique string
}
type SetPatch struct {
	Exercise  *string
	WeightKG  *float64
	Reps      *int
	RPE       *float64
	Technique *string
}
type ListFilter struct {
	Limit    int
	From, To string
}

// MuscleGroups is the closed taxonomy sets are aggregated by. It mirrors the
// groups the user already tracked by hand, so the per-group SI view the
// spreadsheet produced can be reproduced exactly.
var MuscleGroups = []string{
	"pecho", "espalda", "hombro anterior", "hombro lateral", "hombro posterior",
	"biceps", "triceps", "antebrazo", "trapecio",
	"cuadriceps", "isquios", "gluteo", "aductor", "gemelo", "abdomen",
}

func ValidMuscleGroup(v string) bool {
	for _, g := range MuscleGroups {
		if g == v {
			return true
		}
	}
	return false
}

// ExerciseGroup assigns one exercise to one muscle group. One group per
// exercise keeps per-group SI a true partition of session SI: every set counts
// once, so the group totals always add up to the session total.
type ExerciseGroup struct {
	Exercise    string `json:"exercise"`
	MuscleGroup string `json:"muscle_group"`
}

// GroupVolume is accumulated SI for one muscle group over a date range.
// Unmapped exercises are reported under an empty group rather than dropped, so
// a gap in the catalogue is visible instead of silently shrinking the totals.
type GroupVolume struct {
	MuscleGroup string  `json:"muscle_group"`
	TotalSI     float64 `json:"total_si"`
	Sets        int     `json:"sets"`
}

// PlanItem prescribes one exercise: how many sets, in what rep range, at what
// RPE. Load is deliberately absent — the prescription is effort and reps, and
// the weight that meets it is discovered at the gym.
type PlanItem struct {
	Position   int     `json:"position"`
	Exercise   string  `json:"exercise"`
	TargetSets int     `json:"target_sets"`
	RepMin     int     `json:"rep_min,omitempty"`
	RepMax     int     `json:"rep_max,omitempty"`
	TargetRPE  float64 `json:"target_rpe,omitempty"`
}

// Plan is a reusable workout template.
type Plan struct {
	ID    int64      `json:"id"`
	Name  string     `json:"name"`
	Notes string     `json:"notes,omitempty"`
	Items []PlanItem `json:"items,omitempty"`
	// TotalSets is the planned set count, the number the volume-landmark
	// feedback loop actually adjusts week to week.
	TotalSets int `json:"total_sets"`
}

// PlanProgress compares what a session planned against what it has logged, per
// exercise. Exercises done but not planned appear with TargetSets 0, so going
// off-plan is visible instead of hidden.
type PlanProgress struct {
	Exercise    string  `json:"exercise"`
	MuscleGroup string  `json:"muscle_group,omitempty"`
	TargetSets  int     `json:"target_sets"`
	DoneSets    int     `json:"done_sets"`
	RepMin      int     `json:"rep_min,omitempty"`
	RepMax      int     `json:"rep_max,omitempty"`
	TargetRPE   float64 `json:"target_rpe,omitempty"`
	// Skipped marks an exercise consciously dropped today. It stays listed so
	// the session records what was decided, not just what was done.
	Skipped bool `json:"skipped,omitempty"`
}

// Done reports whether the prescribed number of sets has been completed.
func (p PlanProgress) Done() bool { return p.TargetSets > 0 && p.DoneSets >= p.TargetSets }

// SessionItemPatch adjusts one exercise of today's session. Nil fields are left
// unchanged, so a caller can bump only the set count without restating the rest.
type SessionItemPatch struct {
	TargetSets *int
	RepMin     *int
	RepMax     *int
	TargetRPE  *float64
	Skipped    *bool
}

func (p SessionItemPatch) Empty() bool {
	return p.TargetSets == nil && p.RepMin == nil && p.RepMax == nil && p.TargetRPE == nil && p.Skipped == nil
}

// Feedback is the post-session response of one muscle group, each dimension
// rated 0-3 exactly as the source spreadsheet asks: how much fatigue it took,
// how much pump it gave, and how recovered it felt afterwards.
type Feedback struct {
	MuscleGroup string `json:"muscle_group"`
	Fatigue     int    `json:"fatigue"`
	Pump        int    `json:"pump"`
	Recovery    int    `json:"recovery"`
}

// Magnitude is the 0-9 sum the recommendation is derived from.
func (f Feedback) Magnitude() int { return f.Fatigue + f.Pump + f.Recovery }

func validRating(v int) bool { return v >= 0 && v <= 3 }

// Valid reports whether every dimension is inside the 0-3 scale.
func (f Feedback) Valid() bool {
	return ValidMuscleGroup(f.MuscleGroup) && validRating(f.Fatigue) && validRating(f.Pump) && validRating(f.Recovery)
}

// SetChange is how many sets to add or remove next week for a muscle group.
type SetChange struct {
	MuscleGroup string `json:"muscle_group"`
	Magnitude   int    `json:"magnitude"`
	SetsDelta   int    `json:"sets_delta"`
	Advice      string `json:"advice"`
	// LastWeekSets is the volume the magnitude was a response to, so the
	// recommendation can be read as "from N to N+delta".
	LastWeekSets int `json:"last_week_sets"`
}

// RecommendSets maps a 0-9 feedback magnitude to a change in weekly sets.
//
// This mirrors the source spreadsheet, whose two visible anchors are magnitude
// 0 -> "Sube 3 series" and magnitude 7 -> "Mantén o reduce 1 serie". The
// thresholds between them are an interpolation, not a value read off the sheet:
// low magnitude means the stimulus barely registered so volume can climb toward
// MAV, while high magnitude means fatigue is outpacing recovery near MRV.
func RecommendSets(magnitude int) (int, string) {
	switch {
	case magnitude <= 1:
		return 3, "Estímulo muy bajo: sube 3 series"
	case magnitude <= 3:
		return 2, "Estímulo bajo: sube 2 series"
	case magnitude <= 5:
		return 1, "Estímulo correcto: sube 1 serie"
	case magnitude <= 7:
		return 0, "Cerca del máximo recuperable: mantén el volumen"
	default:
		return -1, "Fatiga por encima de la recuperación: baja 1 serie"
	}
}

// Epley1RM estimates a one-rep max from a set. It is descriptive only and is
// never stored: SI remains the single recorded intensity metric. Reps of 1
// return the weight unchanged.
func Epley1RM(weightKG float64, reps int) float64 {
	if weightKG <= 0 || reps <= 0 {
		return 0
	}
	return math.Round(weightKG*(1+float64(reps)/30)*10) / 10
}

// ExerciseSet is one recorded set carrying the date it was performed, so an
// exercise's progression can be read without walking every session.
type ExerciseSet struct {
	Technique string  `json:"technique,omitempty"`
	SetID     int64   `json:"set_id"`
	SessionID int64   `json:"session_id"`
	Date      string  `json:"date"`
	WeightKG  float64 `json:"weight_kg"`
	Reps      int     `json:"reps"`
	RPE       float64 `json:"rpe"`
	SI        float64 `json:"si"`
	Est1RM    float64 `json:"est_1rm"`
}

// ExerciseHistory is one exercise's recorded sets, newest first, plus its best
// set by estimated 1RM — the personal record.
type ExerciseHistory struct {
	Exercise    string        `json:"exercise"`
	MuscleGroup string        `json:"muscle_group,omitempty"`
	Sets        []ExerciseSet `json:"sets"`
	Best        *ExerciseSet  `json:"best,omitempty"`
}

// WeeklyVolume is SI for one muscle group in one training week. WeekStart is
// the Monday of that week, so weeks sort and compare as plain dates.
type WeeklyVolume struct {
	WeekStart   string  `json:"week_start"`
	MuscleGroup string  `json:"muscle_group"`
	TotalSI     float64 `json:"total_si"`
	Sets        int     `json:"sets"`
}

// ExerciseMemory is the last recorded set for one exercise. It powers the web
// UI's quick-pick chips, so a repeated exercise can be logged without retyping
// weight, reps, or RPE.
type ExerciseMemory struct {
	Exercise string  `json:"exercise"`
	WeightKG float64 `json:"weight_kg"`
	Reps     int     `json:"reps"`
	RPE      float64 `json:"rpe"`
}
type Clock func() time.Time

func (p SetPatch) Empty() bool {
	return p.Exercise == nil && p.WeightKG == nil && p.Reps == nil && p.RPE == nil && p.Technique == nil
}
