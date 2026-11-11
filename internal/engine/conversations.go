package engine

import (
	"context"
	"errors"
	"time"
)

// Conversation is one stored chat of a person with the coach.
type Conversation struct {
	ID        int64     `json:"id"`
	Title     string    `json:"title"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ConversationMessage is one stored message. Steps are the tools the coach used
// for an answer, as "name:outcome" (ok, error or pending). Photos are counted,
// never stored.
type ConversationMessage struct {
	Role   string    `json:"role"` // user or assistant
	Origin string    `json:"origin,omitempty"`
	Text   string    `json:"text"`
	Steps  []string  `json:"steps,omitempty"`
	Photos int       `json:"photos,omitempty"`
	At     time.Time `json:"at"`
}

// ErrNoConversation is a conversation that does not exist for this person.
var ErrNoConversation = errors.New("engine: no such conversation")

// ConversationStore keeps each person's conversations. Every call names the
// person; a conversation of someone else is ErrNoConversation.
type ConversationStore interface {
	CreateCoachConversation(ctx context.Context, userID int64, title string, at time.Time) (int64, error)
	AppendCoachMessage(ctx context.Context, userID, convID int64, msg ConversationMessage) error
	// CoachConversations lists conversations whose last message is at or after since, newest first.
	CoachConversations(ctx context.Context, userID int64, since time.Time) ([]Conversation, error)
	CoachConversation(ctx context.Context, userID, convID int64, since time.Time) (Conversation, []ConversationMessage, error)
	RenameCoachConversation(ctx context.Context, userID, convID int64, title string) error
	DeleteCoachConversation(ctx context.Context, userID, convID int64) error
	DeleteCoachConversations(ctx context.Context, userID int64) error
	// PurgeCoachConversations deletes every conversation whose last message is before the time.
	PurgeCoachConversations(ctx context.Context, before time.Time) (int64, error)
}
