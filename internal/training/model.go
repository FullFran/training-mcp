package training

import (
	"errors"
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
