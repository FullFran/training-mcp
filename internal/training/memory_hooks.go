package training

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Memory hooks are product features the host runs around the coach (issue #69):
// memory context when a chat starts, a deterministic summary when a workout
// ends, and passive capture of a reply's learnings. They are never guards
// (HCS ADR-0025): a failure is logged by the caller and nothing waits on it.
// Every write they lead to is a proposal of the memory_save tool, decided like
// the coach's own memory writes.

// The tool and action type a host memory proposal uses, the same as the coach's
// memory_save, so accepting it goes through that tool's code.
const (
	MemorySaveTool   = "memory_save"
	MemorySaveAction = "memory.save"
)

// WorkoutIdleAfter is how long after a session's last set it counts as over for
// its summary. Longer than RestIdleAfter, so a long pause is not an ending.
const WorkoutIdleAfter = 2 * time.Hour

// MaxLearnings bounds the items one reply's learnings block can propose.
const MaxLearnings = 5

// memorySavePayload is memory_save's input, as the coach would send it.
type memorySavePayload struct {
	TopicKey string `json:"topic_key,omitempty"`
	Type     string `json:"type,omitempty"`
	Title    string `json:"title"`
	Body     string `json:"body"`
}

// ProposeMemory proposes saving a fact as the memory_save tool would, in the
// context's agent session. Nothing is written unless the proposal is accepted,
// now by a standing answer or later by the person, through apply. A fact that
// could never be saved is refused before anything is recorded.
func (s *Service) ProposeMemory(ctx context.Context, in MemoryInput, apply Applier) (Proposal, error) {
	if apply == nil {
		return Proposal{}, ErrValidation
	}
	m, err := newMemory(in, s.clock())
	if err != nil {
		return Proposal{}, err
	}
	payload, err := json.Marshal(memorySavePayload{TopicKey: strings.TrimSpace(in.TopicKey), Type: m.Type, Title: m.Title, Body: m.Body})
	if err != nil {
		return Proposal{}, err
	}
	return s.Propose(ctx, ProposalInput{Tool: MemorySaveTool, ActionType: MemorySaveAction, Payload: payload}, func(ctx context.Context) (json.RawMessage, error) {
		return apply(ctx, s, MemorySaveTool, payload)
	})
}

// HostMemory runs the memory hooks for whichever person the coach serves. The
// person is always named by the host, never by the model.
type HostMemory struct {
	ForUser func(userID int64) *Service
	Apply   Applier
}

// MemoryContext is the person's memory_context.
func (h HostMemory) MemoryContext(ctx context.Context, userID int64) (MemoryContext, error) {
	return h.ForUser(userID).MemoryContext(ctx)
}

// ProposeMemory proposes a fact for the person, in the given agent session.
func (h HostMemory) ProposeMemory(ctx context.Context, userID int64, session string, in MemoryInput) error {
	_, err := h.ForUser(userID).ProposeMemory(WithAgentSession(ctx, session), in, h.Apply)
	return err
}

// --- passive capture --------------------------------------------------------

var (
	learningsHeading = regexp.MustCompile(`(?i)^##\s+aprendizajes\s*:?\s*$`)
	learningsItem    = regexp.MustCompile(`^\s{0,3}(?:[-*•]|\d{1,2}[.)])\s+(.*)$`)
	learningsType    = regexp.MustCompile(`^(\pL+)\s*:\s*(.+)$`)
)

// learningTypes maps the type words an item may start with to memory types.
var learningTypes = map[string]string{
	"preference": MemoryPreference, "preferencia": MemoryPreference,
	"limitation": MemoryLimitation, "limitación": MemoryLimitation, "limitacion": MemoryLimitation, "lesión": MemoryLimitation, "lesion": MemoryLimitation,
	"goal": MemoryGoal, "objetivo": MemoryGoal, "meta": MemoryGoal,
	"equipment": MemoryEquipment, "material": MemoryEquipment, "equipo": MemoryEquipment,
	"worked": MemoryWorked, "funcionó": MemoryWorked, "funciono": MemoryWorked,
	"note": MemoryNote, "nota": MemoryNote,
}

// ParseLearnings reads the "## Aprendizajes" block a coach reply may end with:
// one memory fact per list item, at most MaxLearnings, duplicates dropped. An
// item may start with its type and a colon ("limitation: ...", "objetivo:
// ..."); otherwise it is a note. The block must close the reply: a heading or
// plain text after it, or a block inside a code fence, captures nothing.
func ParseLearnings(reply string) []MemoryInput {
	lines := strings.Split(strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(reply), "\n")
	start, fenced := -1, false
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "```") {
			fenced = !fenced
			continue
		}
		if !fenced && learningsHeading.MatchString(strings.TrimSpace(l)) {
			start = i
		}
	}
	if start < 0 || fenced {
		return nil
	}
	var items []string
	for _, l := range lines[start+1:] {
		if strings.TrimSpace(l) == "" {
			continue
		}
		if m := learningsItem.FindStringSubmatch(l); m != nil {
			items = append(items, m[1])
			continue
		}
		indented := strings.HasPrefix(l, "  ") || strings.HasPrefix(l, "\t")
		if !indented || len(items) == 0 || strings.HasPrefix(strings.TrimSpace(l), "#") {
			return nil
		}
		items[len(items)-1] += " " + l
	}
	var out []MemoryInput
	seen := map[string]bool{}
	for _, item := range items {
		text := strings.Join(strings.Fields(item), " ")
		kind := MemoryNote
		if m := learningsType.FindStringSubmatch(text); m != nil {
			if t, ok := learningTypes[strings.ToLower(m[1])]; ok {
				kind, text = t, m[2]
			}
		}
		key := strings.ToLower(text)
		if text == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, MemoryInput{Type: kind, Title: learningTitle(text), Body: cutRunes(text, maxMemoryBody)})
		if len(out) == MaxLearnings {
			break
		}
	}
	return out
}

// learningTitle is the item's first sentence when it fits a title, else the
// item cut at a word.
func learningTitle(text string) string {
	if i := strings.Index(text, ". "); i >= 0 && utf8.RuneCountInString(text[:i+1]) <= maxMemoryTitle {
		return text[:i+1]
	}
	if utf8.RuneCountInString(text) <= maxMemoryTitle {
		return text
	}
	cut := string([]rune(text)[:maxMemoryTitle-1])
	if i := strings.LastIndex(cut, " "); i > 0 {
		cut = cut[:i]
	}
	return cut + "…"
}

// cutRunes keeps a text within max runes, marking a cut with an ellipsis.
func cutRunes(text string, max int) string {
	if utf8.RuneCountInString(text) <= max {
		return text
	}
	return string([]rune(text)[:max-1]) + "…"
}

// --- workout summary ----------------------------------------------------------

func kgText(v float64) string {
	return strconv.FormatFloat(math.Round(v*100)/100, 'f', -1, 64)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

func setText(s Set) string {
	out := kgText(s.WeightKG) + " kg × " + strconv.Itoa(s.Reps)
	if !s.RPEImputed && s.RPE > 0 {
		out += " @ RPE " + kgText(s.RPE)
	}
	if s.Technique != "" {
		out += " + " + s.Technique
	}
	return out
}

// WorkoutSummary is the memory note for one finished session, computed from
// its data alone, with no model and no tokens: exercises, working sets, records
// against earlier sessions, volume and how it went against its plan. Warm-ups
// never count. False when the session has no working set yet.
func (s *Service) WorkoutSummary(ctx context.Context, sessionID int64) (MemoryInput, bool, error) {
	if sessionID <= 0 {
		return MemoryInput{}, false, ErrValidation
	}
	sess, err := s.store.GetSession(ctx, sessionID)
	if err != nil {
		return MemoryInput{}, false, err
	}
	sets := append([]Set(nil), sess.Sets...)
	sort.SliceStable(sets, func(i, j int) bool { return sets[i].Position < sets[j].Position })
	var order []string
	byExercise := map[string][]Set{}
	warmups, working := 0, 0
	volume := 0.0
	for _, st := range sets {
		if st.Category == SetWarmup {
			warmups++
			continue
		}
		if _, seen := byExercise[st.Exercise]; !seen {
			order = append(order, st.Exercise)
		}
		byExercise[st.Exercise] = append(byExercise[st.Exercise], st)
		working++
		volume += st.WeightKG * float64(st.Reps)
	}
	if working == 0 {
		return MemoryInput{}, false, nil
	}
	progress, err := s.store.SessionProgress(ctx, sessionID)
	if err != nil {
		return MemoryInput{}, false, err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Resumen calculado por la app (sin calentamientos): %s, %s, volumen %s kg, SI %s.\n",
		plural(len(order), "ejercicio", "ejercicios"), plural(working, "serie efectiva", "series efectivas"), kgText(volume), kgText(sess.TotalSI))
	for _, ex := range order {
		done := byExercise[ex]
		texts := make([]string, len(done))
		for i, st := range done {
			texts[i] = setText(st)
		}
		fmt.Fprintf(&b, "- %s: %s (%s)", ex, plural(len(done), "serie", "series"), strings.Join(texts, ", "))
		record, err := s.sessionRecord(ctx, sess, done)
		if err != nil {
			return MemoryInput{}, false, err
		}
		b.WriteString(record)
		b.WriteString("\n")
	}
	// A session without a plan lists what was done as off-plan: no plan line.
	if slices.ContainsFunc(progress, func(p PlanProgress) bool { return p.TargetSets > 0 || p.Skipped }) {
		b.WriteString("Respecto al plan: " + planDeviations(progress, byExercise) + "\n")
	}
	if warmups > 0 {
		b.WriteString("Calentamientos no contados.\n")
	}
	day := sess.Date
	if t, err := time.Parse("2006-01-02", sess.Date); err == nil {
		day = t.Format("02/01/2006")
	}
	title := "Entreno del " + day
	if sess.PlanName != "" {
		title = cutRunes(title+" · "+sess.PlanName, maxMemoryTitle)
	}
	return MemoryInput{
		TopicKey: "workout/" + sess.Date + "-" + strconv.FormatInt(sess.ID, 10),
		Type:     MemoryNote,
		Title:    title,
		Body:     cutRunes(strings.TrimSpace(b.String()), maxMemoryBody),
	}, true, nil
}

// sessionRecord says when an exercise's best straight working set of the
// session beats every earlier session's, the way records are kept elsewhere: by
// estimated 1RM, without techniques. An exercise never done before has no record.
func (s *Service) sessionRecord(ctx context.Context, sess Session, done []Set) (string, error) {
	var top *Set
	for i := range done {
		if done[i].Technique == "" && (top == nil || Epley1RM(done[i].WeightKG, done[i].Reps) > Epley1RM(top.WeightKG, top.Reps)) {
			top = &done[i]
		}
	}
	if top == nil {
		return "", nil
	}
	history, err := s.store.ExerciseHistory(ctx, top.Exercise, 500)
	if err != nil {
		return "", err
	}
	before := 0.0
	for _, h := range history.Sets {
		if h.SessionID != sess.ID && h.Date <= sess.Date && h.Category != SetWarmup && h.Technique == "" {
			before = math.Max(before, Epley1RM(h.WeightKG, h.Reps))
		}
	}
	now := Epley1RM(top.WeightKG, top.Reps)
	if before == 0 || now <= before {
		return "", nil
	}
	return fmt.Sprintf("; récord: %s (1RM est. %s kg, antes %s kg)", setText(Set{WeightKG: top.WeightKG, Reps: top.Reps, RPEImputed: true}), kgText(now), kgText(before)), nil
}

// planDeviations lists, in plan order, what differed from the plan: sets done
// against the target, working sets outside the rep range, skipped and off-plan
// exercises.
func planDeviations(progress []PlanProgress, done map[string][]Set) string {
	var out []string
	for _, p := range progress {
		var notes []string
		switch {
		case p.Skipped:
			notes = append(notes, "saltado")
		case p.TargetSets == 0:
			notes = append(notes, "fuera del plan")
		case p.DoneSets != p.TargetSets:
			notes = append(notes, fmt.Sprintf("%d de %d series", p.DoneSets, p.TargetSets))
		}
		if p.RepMin > 0 || p.RepMax > 0 {
			outside := 0
			for _, st := range done[p.Exercise] {
				if (p.RepMin > 0 && st.Reps < p.RepMin) || (p.RepMax > 0 && st.Reps > p.RepMax) {
					outside++
				}
			}
			if outside > 0 {
				notes = append(notes, fmt.Sprintf("%s fuera de %s reps", plural(outside, "serie", "series"), repRange(p.RepMin, p.RepMax)))
			}
		}
		if len(notes) > 0 {
			out = append(out, p.Exercise+": "+strings.Join(notes, ", "))
		}
	}
	if len(out) == 0 {
		return "completo."
	}
	return strings.Join(out, "; ") + "."
}

func repRange(low, high int) string {
	switch {
	case low > 0 && high > 0 && low != high:
		return strconv.Itoa(low) + "-" + strconv.Itoa(high)
	case high > 0:
		return strconv.Itoa(high)
	default:
		return strconv.Itoa(low) + "+"
	}
}

// ProposeWorkoutSummary proposes the session's summary as a memory note, once
// per session whatever the answer. False when it was proposed before or the
// session has no working set (then nothing is recorded, so a later working set
// still gets one). The summary belongs to no agent session.
func (s *Service) ProposeWorkoutSummary(ctx context.Context, sessionID int64, apply Applier) (Proposal, bool, error) {
	in, ok, err := s.WorkoutSummary(ctx, sessionID)
	if err != nil || !ok {
		return Proposal{}, false, err
	}
	claimed, err := s.store.ClaimWorkoutSummary(ctx, sessionID, s.clock())
	if err != nil || !claimed {
		return Proposal{}, false, err
	}
	p, err := s.ProposeMemory(WithAgentSession(ctx, ""), in, apply)
	return p, err == nil, err
}

// ProposeIdleWorkoutSummaries proposes the summary of each session from today or
// yesterday whose last set is at least WorkoutIdleAfter old and that has none
// yet. Older sessions, imported history among them, are left alone.
func (s *Service) ProposeIdleWorkoutSummaries(ctx context.Context, apply Applier) ([]Proposal, error) {
	now := s.clock()
	sessions, err := s.store.ListSessions(ctx, ListFilter{From: now.AddDate(0, 0, -1).Format("2006-01-02"), To: now.Format("2006-01-02"), Limit: 5})
	if err != nil {
		return nil, err
	}
	var out []Proposal
	var errs []error
	for _, sess := range sessions {
		_, last, err := s.store.LastSetAt(ctx, sess.ID)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if now.Sub(last) < WorkoutIdleAfter {
			continue
		}
		p, ok, err := s.ProposeWorkoutSummary(ctx, sess.ID, apply)
		if err != nil {
			errs = append(errs, err)
		}
		if ok {
			out = append(out, p)
		}
	}
	return out, errors.Join(errs...)
}
