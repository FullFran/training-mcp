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
}
type SessionSummary struct {
	ID       int64   `json:"id"`
	Date     string  `json:"date"`
	SetCount int     `json:"set_count"`
	TotalSI  float64 `json:"total_si"`
}
type AddSetInput struct {
	SessionID int64
	Exercise  string
	WeightKG  float64
	Reps      int
	RPE       float64
}
type SetPatch struct {
	Exercise *string
	WeightKG *float64
	Reps     *int
	RPE      *float64
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
	return p.Exercise == nil && p.WeightKG == nil && p.Reps == nil && p.RPE == nil
}
