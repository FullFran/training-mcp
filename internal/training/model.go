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
type Clock func() time.Time

func (p SetPatch) Empty() bool {
	return p.Exercise == nil && p.WeightKG == nil && p.Reps == nil && p.RPE == nil
}
