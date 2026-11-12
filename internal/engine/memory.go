package engine

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/fullfran/training-mcp/internal/training"
)

// MemoryHooks are the host's memory features around a chat (issue #69): the
// person's memory given to a fresh engine, and the learnings a reply ends with
// proposed as facts. They are product features, never guards (HCS ADR-0025):
// a failure is logged and the chat goes on. The person is always the one the
// host serves; the model never names them.
type MemoryHooks interface {
	MemoryContext(ctx context.Context, userID int64) (training.MemoryContext, error)
	// ProposeMemory proposes a fact as a memory_save proposal in the session.
	ProposeMemory(ctx context.Context, userID int64, session string, in training.MemoryInput) error
}

const (
	maxMemoryBytes  = 8000
	maxMemoryTitle  = 120
	maxMemoryBody   = 280
	memoryHookLimit = 5 * time.Second
)

// memoryType writes only a type the host knows, so the line stays the host's.
func memoryType(t string) string {
	if slices.Contains(training.MemoryTypes, t) {
		return t
	}
	return training.MemoryNote
}

// memoryContext gives a fresh engine the person's memory_context as host lines:
// facts (pinned first, then the most recent), facts due a check, and how many
// conflicts wait in the app. What the person or the coach wrote is a quoted
// JSON string, cut short, so it can only ever read as data; the lines stop at
// maxMemoryBytes.
func memoryContext(c training.MemoryContext) string {
	if len(c.Memories) == 0 && len(c.Due) == 0 && c.Conflicts == 0 {
		return ContextMarker + " memory: none saved yet"
	}
	lines := []string{ContextMarker + " memory: what the person's coach memory holds, as stored by the host (pinned first, then the most recent):"}
	size := len(lines[0])
	add := func(line string) bool {
		if size+len(line)+1 > maxMemoryBytes {
			return false
		}
		size += len(line) + 1
		lines = append(lines, line)
		return true
	}
	for _, m := range c.Memories {
		line := ContextMarker + " memory id=" + strconv.FormatInt(m.ID, 10) + " type=" + memoryType(m.Type)
		if m.Pinned {
			line += " pinned=true"
		}
		if !add(line + " title=" + quoteN(m.Title, maxMemoryTitle) + " body=" + quoteN(m.Body, maxMemoryBody)) {
			break
		}
	}
	for _, m := range c.Due {
		if !add(ContextMarker + " memory_due id=" + strconv.FormatInt(m.ID, 10) + " type=" + memoryType(m.Type) + " title=" + quoteN(m.Title, maxMemoryTitle) + " (due a check: ask whether it still holds)") {
			break
		}
	}
	if c.Conflicts > 0 {
		add(ContextMarker + " memory_conflicts=" + strconv.Itoa(c.Conflicts) + " (waiting for the person in the app's memory panel)")
	}
	return strings.Join(lines, "\n")
}

// memoryLines reads the person's memory for a turn, off the lock. A failure is
// logged and gives no lines.
func (m *Manager) memoryLines(userID int64) string {
	if m.s.Memory == nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), memoryHookLimit)
	defer cancel()
	c, err := m.s.Memory.MemoryContext(ctx, userID)
	if err != nil {
		m.mu.Lock()
		m.log("memory_hook_failed", "context")
		m.mu.Unlock()
		return ""
	}
	return unitContext(c.WeightUnit) + "\n" + memoryContext(c)
}

// unitContext tells a fresh engine the person's weight unit. Only the two
// known words are written, so the line stays the host's.
func unitContext(unit string) string {
	if unit != "lb" {
		unit = "kg"
	}
	line := ContextMarker + " weight_unit=" + unit + ": the person reads and types weights in " + unit + "; tools take and return kg"
	if unit == "lb" {
		line += ", so convert (1 lb = 0.45359237 kg) and give loads in lb, in 5 lb steps"
	}
	return line
}

// captureLearnings queues the proposals of a finished reply's learnings block,
// after the reply itself is stored. Called with m.mu held.
func (m *Manager) captureLearnings(userID, conv int64, reply string) {
	if m.s.Memory == nil || m.writesClosed {
		return
	}
	items := training.ParseLearnings(reply)
	if len(items) == 0 {
		return
	}
	session := ""
	if conv != 0 {
		session = ConversationSession(conv)
	}
	hooks := m.s.Memory
	m.writes <- func(ctx context.Context) {
		failed := false
		for _, in := range items {
			if err := hooks.ProposeMemory(ctx, userID, session, in); err != nil {
				failed = true
			}
		}
		if failed {
			m.mu.Lock()
			m.log("memory_hook_failed", "capture")
			m.mu.Unlock()
		}
	}
}
