package training

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Rest target bounds, in seconds. The default applies until the person changes
// it, which keeps an unset target meaning exactly what the app always did.
const (
	DefaultRestSeconds = 120
	MinRestSeconds     = 30
	MaxRestSeconds     = 900
)

// RestIdleAfter is how long after the last set the gap stops being a rest
// between sets: past it the person has finished or paused, not resting.
const RestIdleAfter = 20 * time.Minute

// Rest states, as the agent and the phone read them.
const (
	// RestNone: no set logged today, so there is nothing to rest from.
	RestNone = "none"
	// RestResting: inside the rest target.
	RestResting = "resting"
	// RestReady: the target is reached; time for the next set.
	RestReady = "ready"
	// RestSuperset: the next exercise of the same round follows without rest.
	RestSuperset = "superset"
	// RestIdle: too long since the last set to call it a rest.
	RestIdle = "idle"
)

// Rest is the rest between sets, derived from when the last set was logged.
type Rest struct {
	TargetSeconds int    `json:"target_seconds"`
	State         string `json:"state"`
	// Since is when the last set was logged, the moment the rest began.
	Since            string `json:"since,omitempty"`
	ElapsedSeconds   int    `json:"elapsed_seconds,omitempty"`
	RemainingSeconds int    `json:"remaining_seconds,omitempty"`
	OverBySeconds    int    `json:"over_by_seconds,omitempty"`
}

// LastSet is the most recently logged set of the session and when it was logged.
type LastSet struct {
	Set        Set    `json:"set"`
	LoggedAt   string `json:"logged_at"`
	SecondsAgo int    `json:"seconds_ago"`
}

// LiveWorkout is what a coach watching the session would see right now: today's
// session, what is planned next, the last set and the rest since it.
type LiveWorkout struct {
	Date    string   `json:"date"`
	Now     string   `json:"now"`
	Session *Session `json:"session,omitempty"`
	// Progress is listed when the session has planned exercises.
	Progress []PlanProgress `json:"progress,omitempty"`
	Next     *PlanProgress  `json:"next,omitempty"`
	LastSet  *LastSet       `json:"last_set,omitempty"`
	Rest     Rest           `json:"rest"`
}

// RestState describes the rest at now after a set logged at lastAt. A zero
// lastAt means no set yet. Within a superset round the next exercise follows at
// once, so the state says so instead of counting down.
func RestState(lastAt, now time.Time, target int, superset bool) Rest {
	out := Rest{TargetSeconds: target, State: RestNone}
	if lastAt.IsZero() {
		return out
	}
	elapsed := int(now.Sub(lastAt) / time.Second)
	if elapsed < 0 {
		elapsed = 0
	}
	out.Since = lastAt.Format(time.RFC3339)
	out.ElapsedSeconds = elapsed
	switch {
	case time.Duration(elapsed)*time.Second > RestIdleAfter:
		out.State = RestIdle
	case superset:
		out.State = RestSuperset
	case elapsed < target:
		out.State = RestResting
		out.RemainingSeconds = target - elapsed
	default:
		out.State = RestReady
		out.OverBySeconds = elapsed - target
	}
	return out
}

// NextPlanned is the exercise that comes next after the last logged one. Inside
// a superset it is the next unfinished exercise of the same round, reported with
// superset true because no rest comes first. Otherwise it is the exercise just
// worked while it still has sets to go, then the first unfinished one in plan
// order. Skipped and off-plan exercises are never next.
func NextPlanned(progress []PlanProgress, last string) (*PlanProgress, bool) {
	pending := func(p PlanProgress) bool { return p.TargetSets > 0 && !p.Skipped && !p.Done() }
	current := -1
	for i, p := range progress {
		if p.Exercise == last {
			current = i
			break
		}
	}
	if current >= 0 && progress[current].Superset != "" {
		var round []int
		for i, p := range progress {
			if p.Superset == progress[current].Superset {
				round = append(round, i)
			}
		}
		from := 0
		for k, i := range round {
			if i == current {
				from = k
			}
		}
		for step := 1; step < len(round); step++ {
			i := round[(from+step)%len(round)]
			if pending(progress[i]) {
				next := progress[i]
				return &next, true
			}
		}
	}
	if current >= 0 && pending(progress[current]) {
		next := progress[current]
		return &next, false
	}
	for _, p := range progress {
		if pending(p) {
			next := p
			return &next, false
		}
	}
	return nil, false
}

// RestTarget is the person's target rest between sets.
func (s *Service) RestTarget(ctx context.Context) (int, error) {
	v, err := s.store.RestTarget(ctx)
	if err != nil || v == 0 {
		return DefaultRestSeconds, err
	}
	return v, nil
}

// SetRestTarget stores the person's target rest between sets.
func (s *Service) SetRestTarget(ctx context.Context, seconds int) error {
	if seconds < MinRestSeconds || seconds > MaxRestSeconds {
		return fmt.Errorf("%w: rest must be %d to %d seconds", ErrValidation, MinRestSeconds, MaxRestSeconds)
	}
	return s.store.SetRestTarget(ctx, seconds)
}

// LiveWorkout reads today's session as it stands now.
func (s *Service) LiveWorkout(ctx context.Context) (LiveWorkout, error) {
	now := s.clock()
	out := LiveWorkout{Date: now.Format("2006-01-02"), Now: now.Format(time.RFC3339)}
	target, err := s.RestTarget(ctx)
	if err != nil {
		return out, err
	}
	out.Rest = RestState(time.Time{}, now, target, false)

	sessions, err := s.store.ListSessions(ctx, ListFilter{From: out.Date, To: out.Date, Limit: 1})
	if err != nil || len(sessions) == 0 {
		return out, err
	}
	session, err := s.store.GetSession(ctx, sessions[0].ID)
	if err != nil {
		return out, err
	}
	out.Session = &session

	progress, err := s.store.SessionProgress(ctx, session.ID)
	if err != nil {
		return out, err
	}
	for _, p := range progress {
		if p.TargetSets > 0 || session.PlanID > 0 {
			out.Progress = progress
			break
		}
	}

	lastID, lastAt, err := s.store.LastSetAt(ctx, session.ID)
	switch {
	case errors.Is(err, ErrNotFound):
		out.Next, _ = NextPlanned(out.Progress, "")
		return out, nil
	case err != nil:
		return out, err
	}
	for _, set := range session.Sets {
		if set.ID == lastID {
			out.LastSet = &LastSet{Set: set, LoggedAt: lastAt.Format(time.RFC3339)}
		}
	}
	last := ""
	if out.LastSet != nil {
		last = out.LastSet.Set.Exercise
	}
	var superset bool
	out.Next, superset = NextPlanned(out.Progress, last)
	out.Rest = RestState(lastAt, now, target, superset)
	if out.LastSet != nil {
		out.LastSet.SecondsAgo = out.Rest.ElapsedSeconds
	}
	return out, nil
}
