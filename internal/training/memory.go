package training

import (
	"context"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

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

const (
	maxMemoryTitle      = 120
	maxMemoryBody       = 4000
	maxMemoryTopicKey   = 120
	maxMemorySourceChat = 200
	defaultMemorySearch = 10
	maxMemoryList       = 200
	// memoryContextSize and memoryContextBody keep the start-of-chat context small:
	// pinned facts first, then the most recently updated, each body cut short.
	memoryContextSize = 20
	memoryContextBody = 280
)

// Memory is one durable fact the coach keeps about a person. Chats expire;
// memory stays until the person deletes it. The topic key names the fact, so
// saving the same key again updates it instead of adding a duplicate.
type Memory struct {
	ID           int64     `json:"id"`
	TopicKey     string    `json:"topic_key"`
	Type         string    `json:"type"`
	Title        string    `json:"title"`
	Body         string    `json:"body"`
	SourceChatID string    `json:"source_chat_id,omitempty"`
	Pinned       bool      `json:"pinned"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
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
// recently updated; text is a full-text search ranked by relevance.
type MemoryQuery struct {
	Text  string
	Type  string
	Limit int
}

// MemoryPatch edits a stored fact; nil fields keep their value.
type MemoryPatch struct {
	Type      *string
	Title     *string
	Body      *string
	Pinned    *bool
	UpdatedAt time.Time
}

// MemoryStore keeps one user's coach memory.
type MemoryStore interface {
	// SaveMemory inserts the fact, or updates the one with the same topic key.
	// A nil pinned keeps the stored value (false for a new fact).
	SaveMemory(context.Context, Memory, *bool) (Memory, error)
	GetMemory(context.Context, int64) (Memory, error)
	ListMemory(context.Context, MemoryQuery) ([]Memory, error)
	UpdateMemory(context.Context, int64, MemoryPatch) (Memory, error)
	// DeleteMemory removes the fact and returns it as it was.
	DeleteMemory(context.Context, int64) (Memory, error)
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

// SaveMemory stores a fact, updating the one with the same topic key.
func (s *Service) SaveMemory(ctx context.Context, in MemoryInput) (Memory, error) {
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
	m.CreatedAt = s.clock()
	m.UpdatedAt = m.CreatedAt
	return s.store.SaveMemory(ctx, m, in.Pinned)
}

func (s *Service) GetMemory(ctx context.Context, id int64) (Memory, error) {
	if id <= 0 {
		return Memory{}, ErrNotFound
	}
	return s.store.GetMemory(ctx, id)
}

// SearchMemory finds facts by text, or lists them when the text is empty.
func (s *Service) SearchMemory(ctx context.Context, q MemoryQuery) ([]Memory, error) {
	q.Text = strings.TrimSpace(q.Text)
	q.Type = strings.TrimSpace(q.Type)
	if q.Type != "" && !validMemoryType(q.Type) {
		return nil, ErrValidation
	}
	switch {
	case q.Limit <= 0:
		q.Limit = defaultMemorySearch
	case q.Limit > maxMemoryList:
		q.Limit = maxMemoryList
	}
	return s.store.ListMemory(ctx, q)
}

// MemoryContext is what the coach should know at the start of a chat: pinned
// facts, then the most recently updated, with long bodies cut short.
func (s *Service) MemoryContext(ctx context.Context) ([]Memory, error) {
	out, err := s.store.ListMemory(ctx, MemoryQuery{Limit: memoryContextSize})
	if err != nil {
		return nil, err
	}
	for i := range out {
		if r := []rune(out[i].Body); len(r) > memoryContextBody {
			out[i].Body = string(r[:memoryContextBody]) + "…"
		}
	}
	return out, nil
}

// UpdateMemory is the person editing a fact from the app.
func (s *Service) UpdateMemory(ctx context.Context, id int64, p MemoryPatch) (Memory, error) {
	if id <= 0 {
		return Memory{}, ErrNotFound
	}
	if p.Type != nil {
		v := strings.TrimSpace(*p.Type)
		if !validMemoryType(v) {
			return Memory{}, ErrValidation
		}
		p.Type = &v
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
	return s.store.UpdateMemory(ctx, id, p)
}

// DeleteMemory forgets a fact for good.
func (s *Service) DeleteMemory(ctx context.Context, id int64) (Memory, error) {
	if id <= 0 {
		return Memory{}, ErrNotFound
	}
	return s.store.DeleteMemory(ctx, id)
}
