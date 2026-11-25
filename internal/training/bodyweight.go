package training

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"
)

// Session context: the person's bodyweight, at most one entry a day, and free
// notes on a session. Both are optional and recorded only when the person says
// so; nothing here judges them or sets a goal.

// Bodyweight bounds. They only catch typos and unit mix-ups (80 typed as lb
// when it was kg stays inside them, 8 or 800 does not), not any judgement.
const (
	MinBodyweightKG = 20
	MaxBodyweightKG = 400
	// MaxBodyweightNote is the longest note on a bodyweight entry, in characters.
	MaxBodyweightNote = 500
	// MaxSessionNotes is the longest session note, in characters: the same
	// room a plan's notes have.
	MaxSessionNotes = maxPlanNotesLen
	// BodyweightDefaultDays is the range bodyweight_history reads when none is
	// given: the 90 days ending today.
	BodyweightDefaultDays = 90
	// BodyweightMaxDays is the longest range one history read covers.
	BodyweightMaxDays = 366
)

// Bodyweight is the person's weight on one day, stored in kg.
type Bodyweight struct {
	Date     string  `json:"date"`
	WeightKG float64 `json:"weight_kg"`
	Note     string  `json:"note,omitempty"`
}

// BodyweightInput is one day's weight as the person typed it. Date "" is today.
type BodyweightInput struct {
	Date   string
	Weight float64
	// Unit is the unit Weight was typed in: kg or lb. It is required, so a
	// number is never read in the wrong unit.
	Unit string
	Note string
}

// BodyweightHistory is the entries of a date range, oldest first, and plain
// numbers about them. Summary is nil when the range holds no entry.
type BodyweightHistory struct {
	From    string             `json:"from"`
	To      string             `json:"to"`
	Entries []Bodyweight       `json:"entries"`
	Summary *BodyweightSummary `json:"summary,omitempty"`
}

// BodyweightSummary describes a range's entries. Min and Max are the earliest
// entry with that weight; ChangeKG is Last minus First.
type BodyweightSummary struct {
	Count    int               `json:"count"`
	First    Bodyweight        `json:"first"`
	Last     Bodyweight        `json:"last"`
	Min      Bodyweight        `json:"min"`
	Max      Bodyweight        `json:"max"`
	ChangeKG float64           `json:"change_kg"`
	Avg7     BodyweightAverage `json:"avg_7d"`
}

// BodyweightAverage is the mean of the entries in the 7 days ending on the
// range's last entry, both days included.
type BodyweightAverage struct {
	From     string  `json:"from"`
	To       string  `json:"to"`
	Entries  int     `json:"entries"`
	WeightKG float64 `json:"weight_kg"`
}

// BodyweightStore keeps one user's bodyweight entries and session notes.
type BodyweightStore interface {
	// PutBodyweight stores the entry of its date, replacing the one already
	// there. An empty note is stored as none.
	PutBodyweight(context.Context, Bodyweight) error
	// Bodyweights are the entries from..to, both included, oldest first.
	Bodyweights(ctx context.Context, from, to string) ([]Bodyweight, error)
	// DeleteBodyweight removes the entry of a date; ErrNotFound when there is none.
	DeleteBodyweight(ctx context.Context, date string) error
	// SetSessionNotes replaces a session's notes; "" clears them. A session that
	// is not the user's is ErrNotFound.
	SetSessionNotes(ctx context.Context, sessionID int64, notes string) error
}

// Today is the service clock's date, the day a dateless entry is recorded on.
func (s *Service) Today() string {
	return s.clock().In(s.clock().Location()).Format("2006-01-02")
}

// LogBodyweight records the weight of a day, today when no date is given,
// replacing that day's entry if there is one. The weight is converted from the
// unit it was typed in and stored in kg.
func (s *Service) LogBodyweight(ctx context.Context, in BodyweightInput) (Bodyweight, error) {
	date := in.Date
	if date == "" {
		date = s.Today()
	}
	if !validDate(date) {
		return Bodyweight{}, fmt.Errorf("%w: date must be YYYY-MM-DD", ErrValidation)
	}
	unit := ""
	if strings.TrimSpace(in.Unit) != "" {
		unit = NormalizeUnit(in.Unit)
	}
	if unit == "" {
		return Bodyweight{}, fmt.Errorf("%w: unit must be kg or lb", ErrValidation)
	}
	kg := math.NaN()
	if !math.IsNaN(in.Weight) && !math.IsInf(in.Weight, 0) {
		kg = ToKG(in.Weight, unit)
	}
	if !(kg >= MinBodyweightKG && kg <= MaxBodyweightKG) {
		return Bodyweight{}, fmt.Errorf("%w: weight must be between %d and %d kg (%s and %s lb)", ErrValidation,
			MinBodyweightKG, MaxBodyweightKG, trimFloat(FromKG(MinBodyweightKG, UnitLB)), trimFloat(FromKG(MaxBodyweightKG, UnitLB)))
	}
	note := strings.TrimSpace(in.Note)
	if utf8.RuneCountInString(note) > MaxBodyweightNote {
		return Bodyweight{}, fmt.Errorf("%w: note is at most %d characters", ErrValidation, MaxBodyweightNote)
	}
	entry := Bodyweight{Date: date, WeightKG: kg, Note: note}
	if err := s.store.PutBodyweight(ctx, entry); err != nil {
		return Bodyweight{}, err
	}
	return entry, nil
}

// BodyweightHistory reads the entries from..to, both included. To defaults to
// today and from to the 90 days ending on to; the range is at most 366 days.
func (s *Service) BodyweightHistory(ctx context.Context, from, to string) (BodyweightHistory, error) {
	if to == "" {
		to = s.Today()
	}
	end, err := time.Parse("2006-01-02", to)
	if err != nil {
		return BodyweightHistory{}, fmt.Errorf("%w: to must be YYYY-MM-DD", ErrValidation)
	}
	if from == "" {
		from = end.AddDate(0, 0, 1-BodyweightDefaultDays).Format("2006-01-02")
	}
	start, err := time.Parse("2006-01-02", from)
	if err != nil {
		return BodyweightHistory{}, fmt.Errorf("%w: from must be YYYY-MM-DD", ErrValidation)
	}
	if start.After(end) {
		return BodyweightHistory{}, fmt.Errorf("%w: from must not be after to", ErrValidation)
	}
	if days := int(end.Sub(start).Hours()/24) + 1; days > BodyweightMaxDays {
		return BodyweightHistory{}, fmt.Errorf("%w: the range is at most %d days", ErrValidation, BodyweightMaxDays)
	}
	entries, err := s.store.Bodyweights(ctx, from, to)
	if err != nil {
		return BodyweightHistory{}, err
	}
	if entries == nil {
		entries = []Bodyweight{}
	}
	return BodyweightHistory{From: from, To: to, Entries: entries, Summary: SummarizeBodyweight(entries)}, nil
}

// SummarizeBodyweight describes entries sorted oldest first; nil when there are
// none.
func SummarizeBodyweight(entries []Bodyweight) *BodyweightSummary {
	if len(entries) == 0 {
		return nil
	}
	first, last := entries[0], entries[len(entries)-1]
	sum := &BodyweightSummary{Count: len(entries), First: first, Last: last, Min: first, Max: first}
	for _, e := range entries[1:] {
		if e.WeightKG < sum.Min.WeightKG {
			sum.Min = e
		}
		if e.WeightKG > sum.Max.WeightKG {
			sum.Max = e
		}
	}
	sum.ChangeKG = RoundWeight(last.WeightKG - first.WeightKG)
	lastDay, _ := time.Parse("2006-01-02", last.Date)
	since := lastDay.AddDate(0, 0, -6).Format("2006-01-02")
	sum.Avg7 = BodyweightAverage{From: since, To: last.Date}
	total := 0.0
	for _, e := range entries {
		if e.Date >= since {
			total += e.WeightKG
			sum.Avg7.Entries++
		}
	}
	sum.Avg7.WeightKG = RoundWeight(total / float64(sum.Avg7.Entries))
	return sum
}

// DeleteBodyweight removes the entry of a date; ErrNotFound when there is none.
func (s *Service) DeleteBodyweight(ctx context.Context, date string) error {
	if !validDate(date) {
		return fmt.Errorf("%w: date must be YYYY-MM-DD", ErrValidation)
	}
	return s.store.DeleteBodyweight(ctx, date)
}

// SetSessionNotes replaces a session's free notes (how the person slept, a
// niggle, the gym was packed); blank clears them. It returns the session.
func (s *Service) SetSessionNotes(ctx context.Context, sessionID int64, notes string) (Session, error) {
	if sessionID <= 0 {
		return Session{}, fmt.Errorf("%w: session_id must be positive", ErrValidation)
	}
	notes = strings.TrimSpace(notes)
	if utf8.RuneCountInString(notes) > MaxSessionNotes {
		return Session{}, fmt.Errorf("%w: notes are at most %d characters", ErrValidation, MaxSessionNotes)
	}
	if err := s.store.SetSessionNotes(ctx, sessionID, notes); err != nil {
		return Session{}, err
	}
	return s.GetSession(ctx, sessionID)
}

// trimFloat prints a number with no trailing zeros.
func trimFloat(v float64) string { return strings.TrimSuffix(fmt.Sprintf("%g", v), ".0") }
