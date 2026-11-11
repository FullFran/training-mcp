package training

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// ErrStale is confirming a plan whose facts changed after it was previewed.
var ErrStale = errors.New("the memory changed since the preview; preview it again")

// Memory types: what kind of durable fact the coach keeps about a person.
const (
	MemoryPreference = "preference"
	MemoryLimitation = "limitation"
	MemoryGoal       = "goal"
	MemoryEquipment  = "equipment"
	MemoryWorked     = "worked"
	MemoryNote       = "note"
)

// MemoryTypes lists every memory type in the order the app shows them.
var MemoryTypes = []string{MemoryPreference, MemoryLimitation, MemoryGoal, MemoryEquipment, MemoryWorked, MemoryNote}

// memoryReviewDays is how long a fact of each type is trusted before the coach
// should check it still holds: an injury changes in weeks, a preference in months.
var memoryReviewDays = map[string]int{
	MemoryLimitation: 14,
	MemoryGoal:       30,
	MemoryWorked:     60,
	MemoryNote:       90,
	MemoryEquipment:  180,
	MemoryPreference: 180,
}

func memoryReviewAfter(kind string, now time.Time) time.Time {
	return now.AddDate(0, 0, memoryReviewDays[kind])
}

// Memory views: which facts a listing shows. Active facts are neither forgotten
// nor superseded; they are what the coach reads by default.
const (
	MemoryViewActive     = "active"
	MemoryViewDue        = "due"
	MemoryViewSuperseded = "superseded"
	MemoryViewForgotten  = "forgotten"
)

// MemoryViews lists every view in the order the app shows them.
var MemoryViews = []string{MemoryViewActive, MemoryViewDue, MemoryViewSuperseded, MemoryViewForgotten}

// Why a fact was forgotten: the coach forgot it, or it was merged into another.
const (
	MemoryForgotten    = "forgotten"
	MemoryConsolidated = "consolidated"
)

// Relations the coach records between a fact just saved and a similar one.
const (
	RelationSupersedes    = "supersedes"
	RelationConflictsWith = "conflicts_with"
	RelationRelated       = "related"
	RelationCompatible    = "compatible"
	RelationScoped        = "scoped"
	RelationNotConflict   = "not_conflict"
)

// MemoryRelations lists every relation the coach may record.
var MemoryRelations = []string{RelationSupersedes, RelationConflictsWith, RelationRelated, RelationCompatible, RelationScoped, RelationNotConflict}

// Relation statuses: pending until the coach judges it; a conflict stays judged
// until the person resolves it.
const (
	RelationPending  = "pending"
	RelationJudged   = "judged"
	RelationResolved = "resolved"
)

// How the person resolves a conflict: keep the newer fact, the older, or both.
const (
	KeepNew  = "new"
	KeepOld  = "old"
	KeepBoth = "both"
)

const (
	maxMemoryTitle      = 120
	maxMemoryBody       = 4000
	maxMemoryTopicKey   = 120
	maxMemorySourceChat = 200
	maxMemoryReason     = 500
	defaultMemorySearch = 10
	maxMemoryList       = 200
	// memoryContextSize and memoryContextBody keep the start-of-chat context small:
	// pinned facts first, then the most recently updated, each body cut short.
	memoryContextSize = 20
	memoryContextBody = 280
	memoryContextDue  = 5
	// A consolidation merges between 2 and maxConsolidate facts.
	maxConsolidate = 10
)

// Memory is one durable fact the coach keeps about a person. Chats expire;
// memory stays until the person deletes it. The topic key names the fact, so
// saving the same key again updates it, keeping the previous text as a revision.
type Memory struct {
	ID           int64     `json:"id"`
	TopicKey     string    `json:"topic_key"`
	Type         string    `json:"type"`
	Title        string    `json:"title"`
	Body         string    `json:"body"`
	SourceChatID string    `json:"source_chat_id,omitempty"`
	Pinned       bool      `json:"pinned"`
	Revision     int       `json:"revision"`
	ReviewAfter  time.Time `json:"review_after,omitzero"`
	// SupersededBy is the fact that replaced this one; a superseded fact leaves
	// context and search but stays for history.
	SupersededBy int64 `json:"superseded_by,omitempty"`
	// DeletedAt is when the fact was forgotten; ForgetReason says how, and
	// ConsolidatedInto is the fact it was merged into.
	DeletedAt        time.Time `json:"deleted_at,omitzero"`
	ForgetReason     string    `json:"forget_reason,omitempty"`
	ConsolidatedInto int64     `json:"consolidated_into,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// Due reports whether the fact should be checked with the person again.
func (m Memory) Due(now time.Time) bool {
	return !m.ReviewAfter.IsZero() && !m.ReviewAfter.After(now)
}

// MemoryRevision is a fact's text as it was before a change replaced it.
// Revisions are numbered from 1, oldest first.
type MemoryRevision struct {
	Revision   int       `json:"revision"`
	Type       string    `json:"type"`
	Title      string    `json:"title"`
	Body       string    `json:"body"`
	ReplacedAt time.Time `json:"replaced_at"`
}

// MemoryCandidate is a similar fact found when saving: the coach judges the
// relation between the two through RelationID.
type MemoryCandidate struct {
	RelationID int64  `json:"relation_id"`
	Memory     Memory `json:"memory"`
}

// MemorySaved is a saved fact and the similar facts still to be judged against it.
type MemorySaved struct {
	Memory     Memory            `json:"memory"`
	Candidates []MemoryCandidate `json:"candidates"`
}

// MemoryRelation is the coach's judgement of how a fact relates to a similar one.
type MemoryRelation struct {
	ID          int64     `json:"id"`
	MemoryID    int64     `json:"memory_id"`
	CandidateID int64     `json:"candidate_id"`
	Relation    string    `json:"relation,omitempty"`
	Reason      string    `json:"reason,omitempty"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	JudgedAt    time.Time `json:"judged_at,omitzero"`
}

// MemoryConflict is a conflict the person has to resolve, with both facts.
type MemoryConflict struct {
	Relation MemoryRelation
	New, Old Memory
}

// MemoryContext is what the coach reads at the start of a chat.
type MemoryContext struct {
	Memories []Memory `json:"memories"`
	// Due are facts to check with the person, e.g. whether an injury still hurts.
	Due []Memory `json:"due"`
	// Conflicts counts conflicts waiting for the person in the memory panel.
	Conflicts int `json:"conflicts"`
}

// MemoryInput saves a fact. An empty topic key is derived from the type and
// title; an empty type is a note. A nil Pinned keeps what is stored.
type MemoryInput struct {
	TopicKey     string
	Type         string
	Title        string
	Body         string
	SourceChatID string
	Pinned       *bool
}

// MemoryQuery finds facts. Empty text lists them, pinned first and then the most
// recently updated; text is a full-text search ranked by relevance. View picks
// which facts (active when empty); due compares review dates with Now.
type MemoryQuery struct {
	Text  string
	Type  string
	View  string
	Limit int
	Now   time.Time
}

// MemoryPatch edits a stored fact; nil fields keep their value.
type MemoryPatch struct {
	Type        *string
	Title       *string
	Body        *string
	Pinned      *bool
	ReviewAfter time.Time
	UpdatedAt   time.Time
}

// ConsolidationInput merges facts into one survivor. Without a fingerprint it
// is a preview; the fingerprint of that preview confirms exactly what was shown.
type ConsolidationInput struct {
	MemoryIDs   []int64
	TopicKey    string
	Type        string
	Title       string
	Body        string
	Fingerprint string
}

// ConsolidationPlan is what a consolidation would do: the facts it merges and
// the fact that survives.
type ConsolidationPlan struct {
	Sources     []Memory `json:"sources"`
	Survivor    Memory   `json:"survivor"`
	Fingerprint string   `json:"fingerprint"`
}

// MemoryConsolidation is a confirmed plan for the store: each source with the
// revision the plan saw, and the survivor to write.
type MemoryConsolidation struct {
	Sources  map[int64]int
	Survivor Memory
	At       time.Time
}

// MemoryStore keeps one user's coach memory.
type MemoryStore interface {
	// SaveMemory inserts the fact, or updates the one with the same topic key,
	// keeping changed text as a revision and bringing a forgotten fact back. A
	// nil pinned keeps the stored value. It returns similar active facts whose
	// relation to this one is not judged yet.
	SaveMemory(context.Context, Memory, *bool) (MemorySaved, error)
	// GetMemory returns a fact that is not forgotten.
	GetMemory(context.Context, int64) (Memory, error)
	ListMemory(context.Context, MemoryQuery) ([]Memory, error)
	MemoryHistory(context.Context, int64) ([]MemoryRevision, error)
	UpdateMemory(context.Context, int64, MemoryPatch) (Memory, error)
	ConfirmMemory(ctx context.Context, id int64, reviewAfter time.Time) (Memory, error)
	// ForgetMemory soft-deletes a fact: it leaves context and search but is kept.
	ForgetMemory(ctx context.Context, id int64, reason string, at time.Time) (Memory, error)
	// DeleteMemory removes a fact for good, forgotten or not, with its history.
	DeleteMemory(context.Context, int64) (Memory, error)
	DeleteAllMemory(context.Context) (int, error)
	JudgeMemory(ctx context.Context, relationID int64, relation, reason string, at time.Time) (MemoryRelation, error)
	ListMemoryConflicts(context.Context) ([]MemoryConflict, error)
	ResolveMemoryConflict(ctx context.Context, relationID int64, keep string, at time.Time) error
	// ConsolidateMemory writes the survivor and forgets the sources, all or
	// nothing; a source changed or forgotten since the plan is ErrStale.
	ConsolidateMemory(context.Context, MemoryConsolidation) (Memory, error)
}

func validMemoryType(t string) bool { return slices.Contains(MemoryTypes, t) }

// memoryTopicKey lowercases a key and turns each run of characters other than
// letters, digits, '/', '.', '_' and '-' into one '-'.
func memoryTopicKey(raw string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(strings.TrimSpace(raw)) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("/._", r):
			b.WriteRune(r)
			dash = false
		case !dash:
			b.WriteRune('-')
			dash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func checkText(v string, max int) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" || utf8.RuneCountInString(v) > max {
		return "", ErrValidation
	}
	return v, nil
}

// newMemory validates and normalises a fact before it is written.
func newMemory(in MemoryInput, now time.Time) (Memory, error) {
	m := Memory{Type: strings.TrimSpace(in.Type), SourceChatID: strings.TrimSpace(in.SourceChatID)}
	if m.Type == "" {
		m.Type = MemoryNote
	}
	if !validMemoryType(m.Type) || utf8.RuneCountInString(m.SourceChatID) > maxMemorySourceChat {
		return Memory{}, ErrValidation
	}
	var err error
	if m.Title, err = checkText(in.Title, maxMemoryTitle); err != nil {
		return Memory{}, err
	}
	if m.Body, err = checkText(in.Body, maxMemoryBody); err != nil {
		return Memory{}, err
	}
	m.TopicKey = memoryTopicKey(in.TopicKey)
	if strings.TrimSpace(in.TopicKey) == "" {
		m.TopicKey = memoryTopicKey(m.Type + "/" + m.Title)
	}
	if m.TopicKey == "" || utf8.RuneCountInString(m.TopicKey) > maxMemoryTopicKey {
		return Memory{}, ErrValidation
	}
	m.CreatedAt, m.UpdatedAt = now, now
	m.ReviewAfter = memoryReviewAfter(m.Type, now)
	return m, nil
}

// SaveMemory stores a fact, updating the one with the same topic key. Saving
// restarts the fact's review cycle.
func (s *Service) SaveMemory(ctx context.Context, in MemoryInput) (MemorySaved, error) {
	m, err := newMemory(in, s.clock())
	if err != nil {
		return MemorySaved{}, err
	}
	return s.store.SaveMemory(ctx, m, in.Pinned)
}

func (s *Service) GetMemory(ctx context.Context, id int64) (Memory, error) {
	if id <= 0 {
		return Memory{}, ErrNotFound
	}
	return s.store.GetMemory(ctx, id)
}

// MemoryHistory is a fact's earlier texts, oldest first.
func (s *Service) MemoryHistory(ctx context.Context, id int64) ([]MemoryRevision, error) {
	if id <= 0 {
		return nil, ErrNotFound
	}
	return s.store.MemoryHistory(ctx, id)
}

// SearchMemory finds facts by text, or lists them when the text is empty.
func (s *Service) SearchMemory(ctx context.Context, q MemoryQuery) ([]Memory, error) {
	q.Text = strings.TrimSpace(q.Text)
	q.Type = strings.TrimSpace(q.Type)
	q.View = strings.TrimSpace(q.View)
	if q.View == "" {
		q.View = MemoryViewActive
	}
	if (q.Type != "" && !validMemoryType(q.Type)) || !slices.Contains(MemoryViews, q.View) {
		return nil, ErrValidation
	}
	switch {
	case q.Limit <= 0:
		q.Limit = defaultMemorySearch
	case q.Limit > maxMemoryList:
		q.Limit = maxMemoryList
	}
	q.Now = s.clock()
	return s.store.ListMemory(ctx, q)
}

func compactBodies(ms []Memory) {
	for i := range ms {
		if r := []rune(ms[i].Body); len(r) > memoryContextBody {
			ms[i].Body = string(r[:memoryContextBody]) + "…"
		}
	}
}

// MemoryContext is what the coach should know at the start of a chat: pinned
// facts, then the most recently updated, the facts due a check with the person,
// and how many conflicts wait in the panel. Long bodies are cut short.
func (s *Service) MemoryContext(ctx context.Context) (MemoryContext, error) {
	now := s.clock()
	memories, err := s.store.ListMemory(ctx, MemoryQuery{View: MemoryViewActive, Limit: memoryContextSize, Now: now})
	if err != nil {
		return MemoryContext{}, err
	}
	due, err := s.store.ListMemory(ctx, MemoryQuery{View: MemoryViewDue, Limit: memoryContextDue, Now: now})
	if err != nil {
		return MemoryContext{}, err
	}
	conflicts, err := s.store.ListMemoryConflicts(ctx)
	if err != nil {
		return MemoryContext{}, err
	}
	compactBodies(memories)
	compactBodies(due)
	return MemoryContext{Memories: memories, Due: due, Conflicts: len(conflicts)}, nil
}

// UpdateMemory is the person editing a fact from the app; it restarts the
// fact's review cycle.
func (s *Service) UpdateMemory(ctx context.Context, id int64, p MemoryPatch) (Memory, error) {
	current, err := s.GetMemory(ctx, id)
	if err != nil {
		return Memory{}, err
	}
	kind := current.Type
	if p.Type != nil {
		v := strings.TrimSpace(*p.Type)
		if !validMemoryType(v) {
			return Memory{}, ErrValidation
		}
		p.Type, kind = &v, v
	}
	for _, f := range []struct {
		v   **string
		max int
	}{{&p.Title, maxMemoryTitle}, {&p.Body, maxMemoryBody}} {
		if *f.v == nil {
			continue
		}
		v, err := checkText(**f.v, f.max)
		if err != nil {
			return Memory{}, err
		}
		*f.v = &v
	}
	p.UpdatedAt = s.clock()
	p.ReviewAfter = memoryReviewAfter(kind, p.UpdatedAt)
	return s.store.UpdateMemory(ctx, id, p)
}

// ConfirmMemory records that a fact still holds and restarts its review cycle.
func (s *Service) ConfirmMemory(ctx context.Context, id int64) (Memory, error) {
	current, err := s.GetMemory(ctx, id)
	if err != nil {
		return Memory{}, err
	}
	return s.store.ConfirmMemory(ctx, id, memoryReviewAfter(current.Type, s.clock()))
}

// ForgetMemory is the coach forgetting a fact: it leaves context and search but
// is kept, so the person can still see it and delete it for good.
func (s *Service) ForgetMemory(ctx context.Context, id int64) (Memory, error) {
	if id <= 0 {
		return Memory{}, ErrNotFound
	}
	return s.store.ForgetMemory(ctx, id, MemoryForgotten, s.clock())
}

// DeleteMemory is the person deleting a fact for good, with its history.
func (s *Service) DeleteMemory(ctx context.Context, id int64) (Memory, error) {
	if id <= 0 {
		return Memory{}, ErrNotFound
	}
	return s.store.DeleteMemory(ctx, id)
}

// DeleteAllMemory is the person deleting everything the coach remembers.
func (s *Service) DeleteAllMemory(ctx context.Context) (int, error) {
	return s.store.DeleteAllMemory(ctx)
}

// JudgeMemory records the coach's judgement of a pending relation. Supersedes
// retires the older fact; conflicts_with waits for the person in the panel.
func (s *Service) JudgeMemory(ctx context.Context, relationID int64, relation, reason string) (MemoryRelation, error) {
	if relationID <= 0 {
		return MemoryRelation{}, ErrNotFound
	}
	reason = strings.TrimSpace(reason)
	if !slices.Contains(MemoryRelations, relation) || utf8.RuneCountInString(reason) > maxMemoryReason {
		return MemoryRelation{}, ErrValidation
	}
	return s.store.JudgeMemory(ctx, relationID, relation, reason, s.clock())
}

// MemoryConflicts are the conflicts waiting for the person.
func (s *Service) MemoryConflicts(ctx context.Context) ([]MemoryConflict, error) {
	return s.store.ListMemoryConflicts(ctx)
}

// ResolveMemoryConflict is the person settling a conflict.
func (s *Service) ResolveMemoryConflict(ctx context.Context, relationID int64, keep string) error {
	if relationID <= 0 {
		return ErrNotFound
	}
	if keep != KeepNew && keep != KeepOld && keep != KeepBoth {
		return ErrValidation
	}
	return s.store.ResolveMemoryConflict(ctx, relationID, keep, s.clock())
}

// PreviewConsolidation shows what merging facts would do, without writing.
func (s *Service) PreviewConsolidation(ctx context.Context, in ConsolidationInput) (ConsolidationPlan, error) {
	ids := slices.Clone(in.MemoryIDs)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	if len(ids) < 2 || len(ids) > maxConsolidate || ids[0] <= 0 {
		return ConsolidationPlan{}, ErrValidation
	}
	kind := in.Type
	plan := ConsolidationPlan{}
	for _, id := range ids {
		m, err := s.store.GetMemory(ctx, id)
		if err != nil {
			return ConsolidationPlan{}, err
		}
		if kind == "" {
			kind = m.Type
		}
		plan.Sources = append(plan.Sources, m)
	}
	survivor, err := newMemory(MemoryInput{TopicKey: in.TopicKey, Type: kind, Title: in.Title, Body: in.Body}, s.clock())
	if err != nil {
		return ConsolidationPlan{}, err
	}
	for _, m := range plan.Sources {
		survivor.Pinned = survivor.Pinned || m.Pinned
	}
	plan.Survivor = survivor
	plan.Fingerprint = consolidationFingerprint(plan)
	return plan, nil
}

// consolidationFingerprint names a plan by what it merges and what it writes:
// each source's id, revision and text, and the survivor's key, type and text.
func consolidationFingerprint(p ConsolidationPlan) string {
	type source struct {
		ID       int64
		Revision int
		Type     string
		Title    string
		Body     string
		Pinned   bool
	}
	sources := make([]source, len(p.Sources))
	for i, m := range p.Sources {
		sources[i] = source{m.ID, m.Revision, m.Type, m.Title, m.Body, m.Pinned}
	}
	data, _ := json.Marshal(struct {
		V                      int
		Sources                []source
		Key, Type, Title, Body string
	}{1, sources, p.Survivor.TopicKey, p.Survivor.Type, p.Survivor.Title, p.Survivor.Body})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:16])
}

// ConsolidateMemory merges facts as previewed: the fingerprint must be the
// preview's, so nothing is merged that the coach and the person did not see.
func (s *Service) ConsolidateMemory(ctx context.Context, in ConsolidationInput) (Memory, error) {
	plan, err := s.PreviewConsolidation(ctx, in)
	if err != nil {
		return Memory{}, err
	}
	if in.Fingerprint != plan.Fingerprint {
		return Memory{}, ErrStale
	}
	c := MemoryConsolidation{Sources: map[int64]int{}, Survivor: plan.Survivor, At: s.clock()}
	for _, m := range plan.Sources {
		c.Sources[m.ID] = m.Revision
	}
	return s.store.ConsolidateMemory(ctx, c)
}
