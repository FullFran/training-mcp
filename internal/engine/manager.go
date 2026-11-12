package engine

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"os/exec"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

// Settings configure the manager. Zero durations and limits take the defaults
// documented in docs/coach.md.
type Settings struct {
	Vault  *Vault
	Tokens *Tokens
	// Command builds a new, unstarted engine process. The manager sets its
	// stdin and stdout; whoever builds it decides the program and environment.
	Command func() *exec.Cmd
	// MCPURL is where an engine reaches this server's /mcp, on loopback.
	MCPURL       string
	SystemPrompt string

	MaxProcesses int
	// Idle stops a process with no turn and no pending login for this long.
	Idle time.Duration
	// MaxAge bounds a process, and with it its /mcp token.
	MaxAge time.Duration
	// RefreshMargin: a credential this close to expiry is refreshed first.
	RefreshMargin time.Duration
	// ReplyTimeout bounds how long the engine has to answer a login start or a refresh.
	ReplyTimeout time.Duration
	// LoginTTL is the hard deadline of a pending device login.
	LoginTTL    time.Duration
	LoginLimit  int
	LoginWindow time.Duration
	TurnTimeout time.Duration
	// TurnMaxBytes bounds the answer text one turn may stream.
	TurnMaxBytes int
	AbortGrace   time.Duration
	// RefreshGrace keeps a stopping process alive this long so a refresh it was
	// already asked for can come back and be stored.
	RefreshGrace time.Duration
	// MaxPendingEvents bounds the live workout events queued per person.
	MaxPendingEvents int

	// Preferences keeps each person's model and thinking choice. Without one,
	// choices live in memory until the server stops.
	Preferences PreferenceStore
	// Connectors lists each person's connected remote MCP servers. Nil: none.
	Connectors Connectors
	// ConnectorURL is the loopback prefix of the host's connector proxy; a
	// server's URL is ConnectorURL + name + "/mcp". Default: next to MCPURL.
	ConnectorURL string
	// ApprovalTimeout is how long a connector tool call waits for the person.
	ApprovalTimeout time.Duration
	// Conversations keeps each person's chats. Without one, nothing is stored
	// and a turn belongs to no conversation.
	Conversations ConversationStore
	// Retention is how long a conversation is kept after its last message.
	Retention time.Duration
	// PurgeEvery is how often conversations past their retention are deleted.
	PurgeEvery time.Duration
	// Memory runs the host's memory hooks: memory for a fresh engine and the
	// learnings of a reply proposed as facts. Nil: none.
	Memory MemoryHooks

	Now    func() time.Time
	Logger *log.Logger
}

// PreferenceStore keeps each person's model and thinking choice; empty strings
// mean never chosen.
type PreferenceStore interface {
	CoachPreferences(ctx context.Context, userID int64) (model, thinking string, err error)
	SetCoachPreferences(ctx context.Context, userID int64, model, thinking string) error
}

type memoryPreferences struct {
	mu sync.Mutex
	m  map[int64][2]string
}

func (p *memoryPreferences) CoachPreferences(_ context.Context, userID int64) (string, string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	v := p.m[userID]
	return v[0], v[1], nil
}

func (p *memoryPreferences) SetCoachPreferences(_ context.Context, userID int64, model, thinking string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.m[userID] = [2]string{model, thinking}
	return nil
}

func (s *Settings) defaults() {
	set := func(d *time.Duration, v time.Duration) {
		if *d <= 0 {
			*d = v
		}
	}
	if s.MaxProcesses <= 0 {
		s.MaxProcesses = 2
	}
	set(&s.Idle, 5*time.Minute)
	set(&s.MaxAge, time.Hour)
	set(&s.RefreshMargin, 10*time.Minute)
	set(&s.ReplyTimeout, time.Minute)
	set(&s.LoginTTL, 15*time.Minute)
	if s.LoginLimit <= 0 {
		s.LoginLimit = 3
	}
	set(&s.LoginWindow, 15*time.Minute)
	set(&s.TurnTimeout, 10*time.Minute)
	if s.TurnMaxBytes <= 0 {
		s.TurnMaxBytes = 2 << 20
	}
	set(&s.AbortGrace, 5*time.Second)
	set(&s.RefreshGrace, 10*time.Second)
	if s.MaxPendingEvents <= 0 {
		s.MaxPendingEvents = 20
	}
	set(&s.ApprovalTimeout, defaultApprovalTimeout)
	if s.ConnectorURL == "" {
		s.ConnectorURL = strings.TrimSuffix(s.MCPURL, "/mcp") + "/engine/connectors/"
	}
	if s.Now == nil {
		s.Now = time.Now
	}
	set(&s.Retention, 90*24*time.Hour)
	set(&s.PurgeEvery, time.Hour)
	if s.Preferences == nil {
		s.Preferences = &memoryPreferences{m: map[int64][2]string{}}
	}
	if s.Logger == nil {
		s.Logger = log.Default()
	}
}

// Status is what the panel shows about a person's connection.
type Status struct {
	State           string `json:"state"` // disconnected, pending, connected, reconnect
	UserCode        string `json:"user_code,omitempty"`
	VerificationURI string `json:"verification_uri,omitempty"`
}

// LoginInfo is the answer to a login start.
type LoginInfo struct {
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
}

// Event is one thing a subscribed chat panel is told: turn (a turn started,
// with its origin), delta, tool, done or error.
type Event struct {
	Name string
	Data map[string]any
}

const (
	killGrace     = 2 * time.Second
	subBuffer     = 512
	outBuffer     = 64
	originUser    = "user"
	originEvent   = "reaction"
	reactionText  = "[training-mcp, set by the host] The person just logged the set above and is resting. React briefly, in their language, in one or two sentences: feedback on that set and what to do next. Use the tools only if you need data you do not have."
	maxPromptChar = 4000
	photoOnlyText = "(The person sent only the attached photo.)"
)

type refreshCall struct {
	done chan struct{}
	cred *Credential
	err  error
}

type loginState struct {
	userCode string
	// result receives the user code ("" with a code on failure) once.
	result chan loginResult
	expiry *time.Timer
}

type loginResult struct {
	userCode string
	code     ErrorCode
}

type turnState struct {
	bytes      int
	limited    bool
	timeout    *time.Timer
	abortTimer *time.Timer
	// What is stored of the answer when the turn ends.
	conv   int64
	origin string
	text   strings.Builder
	steps  []string
}

// turnRequest is one turn to start.
type turnRequest struct {
	conv     int64
	history  []ConversationMessage // stored messages, given to a fresh engine
	memory   string                // the person's memory lines, given to a fresh engine
	text     string                // what the engine gets
	origin   string
	images   []Image
	model    string
	thinking string
	record   *ConversationMessage // the person's message to store, if any
}

type proc struct {
	cmd          *exec.Cmd
	userID       int64
	out          chan []byte
	startedAt    time.Time
	token        string
	toolsChecked bool
	// used is set once the process served a turn of conversation conv; its
	// model context then belongs to that conversation.
	used      bool
	conv      int64
	idle      *time.Timer
	turn      *turnState
	refreshes map[string]chan *Credential
	cs        connectorState
	stopped   bool
	discard   bool
	exited    bool
	tornDown  bool
	done      chan struct{}
}

type person struct {
	id          int64
	proc        *proc
	login       *loginState
	reconnect   bool
	refreshing  *refreshCall
	loginStarts []time.Time
	subs        map[int]chan Event
	nextSub     int
	events      []WorkoutEvent
	// models is the list the person's engine reported; modelWait is closed when
	// a requested list (or its failure, in modelErr) arrives.
	models    *ModelList
	modelWait chan struct{}
	modelErr  ErrorCode
	// loaded is which connector servers the last session loaded.
	loaded map[string]bool
	// conv is the conversation the person has open; reactions go there.
	conv int64
}

// Manager starts, feeds and stops engine processes: at most one per person,
// started on first use and stopped when idle. It owns the stored credentials
// and is the only place that refreshes one, so a rotating refresh token is never
// used twice.
type Manager struct {
	s      Settings
	mu     sync.Mutex
	people map[int64]*person
	// convAnswers holds the always answers given in each stored conversation,
	// by conversation id: a conversation is one HCS-0005 session, whatever
	// engine process serves it.
	convAnswers map[int64]*conversationAnswers
	alive       map[*proc]bool
	closed      bool
	// writes stores conversation messages in order, off the lock.
	writes       chan func(context.Context)
	writesClosed bool
	writesDone   chan struct{}
	quit         chan struct{}
}

func NewManager(s Settings) (*Manager, error) {
	if s.Vault == nil || s.Tokens == nil || s.Command == nil || s.MCPURL == "" {
		return nil, errors.New("engine: vault, tokens, command and MCP URL are required")
	}
	s.defaults()
	m := &Manager{s: s, people: map[int64]*person{}, convAnswers: map[int64]*conversationAnswers{}, alive: map[*proc]bool{}, writes: make(chan func(context.Context), 256), writesDone: make(chan struct{}), quit: make(chan struct{})}
	go m.writeLoop()
	if s.Conversations != nil {
		go m.purgeLoop()
	}
	return m, nil
}

// writeLoop runs stored-message writes one at a time, in the order the turns
// produced them, so a reply is never stored before the message it answers.
func (m *Manager) writeLoop() {
	defer close(m.writesDone)
	for w := range m.writes {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		w(ctx)
		cancel()
	}
}

// persist queues a write of a conversation message. Called with m.mu held.
func (m *Manager) persist(userID, conv int64, msg ConversationMessage) {
	st := m.s.Conversations
	if st == nil || conv == 0 || m.writesClosed {
		return
	}
	m.writes <- func(ctx context.Context) {
		if err := st.AppendCoachMessage(ctx, userID, conv, msg); err != nil && !errors.Is(err, ErrNoConversation) {
			m.log("store_failed", "append_message")
		}
	}
}

// purgeLoop deletes conversations past their retention when the coach starts
// and every PurgeEvery after.
func (m *Manager) purgeLoop() {
	tick := time.NewTicker(m.s.PurgeEvery)
	defer tick.Stop()
	for {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		if _, err := m.s.Conversations.PurgeCoachConversations(ctx, m.cutoff()); err != nil {
			m.log("store_failed", "purge")
		}
		m.mu.Lock()
		m.dropConversationAnswers(func(a *conversationAnswers) bool { return a.used.Before(m.cutoff()) })
		m.mu.Unlock()
		cancel()
		select {
		case <-tick.C:
		case <-m.quit:
			return
		}
	}
}

// cutoff is the oldest last-message time a kept conversation can have.
func (m *Manager) cutoff() time.Time { return m.s.Now().Add(-m.s.Retention) }

// LiveCount is the number of processes holding a slot.
func (m *Manager) LiveCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.liveCount()
}

func (m *Manager) liveCount() int {
	n := 0
	for _, p := range m.people {
		if p.proc != nil {
			n++
		}
	}
	return n
}

func (m *Manager) Status(userID int64) Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := m.people[userID]
	if p != nil && p.login != nil {
		return Status{State: "pending", UserCode: p.login.userCode, VerificationURI: DeviceVerificationURI}
	}
	c, err := m.s.Vault.Read(userID)
	switch {
	case err != nil:
		return Status{State: "reconnect"}
	case c == nil:
		return Status{State: "disconnected"}
	case p != nil && p.reconnect:
		return Status{State: "reconnect"}
	}
	return Status{State: "connected"}
}

// StartLogin starts a device-code login. One at a time per person: a new one
// cancels the previous. A login holds a process without a credential, so starts
// are rate-limited per person and the login has a hard deadline.
func (m *Manager) StartLogin(userID int64) (LoginInfo, error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return LoginInfo{}, fail(CodeEngineUnavailable)
	}
	p := m.personOf(userID)
	m.pruneStarts(p)
	if len(p.loginStarts) >= m.s.LoginLimit {
		m.mu.Unlock()
		return LoginInfo{}, fail(CodeRateLimited)
	}
	if p.login != nil {
		m.endLogin(p, CodeLoginFailed, true, true)
	}
	pr, err := m.ensureProc(p, false)
	if err != nil {
		m.forget(p)
		m.mu.Unlock()
		return LoginInfo{}, err
	}
	p.loginStarts = append(p.loginStarts, m.s.Now())
	l := &loginState{result: make(chan loginResult, 1)}
	l.expiry = time.AfterFunc(m.s.LoginTTL, func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if p.login == l {
			m.endLogin(p, CodeLoginFailed, true, false)
		}
	})
	p.login = l
	m.send(pr, parentMessage{Type: "login.start"})
	m.touch(p, pr)
	m.mu.Unlock()

	select {
	case r := <-l.result:
		if r.code != "" {
			return LoginInfo{}, fail(r.code)
		}
		return LoginInfo{UserCode: r.userCode, VerificationURI: DeviceVerificationURI, ExpiresIn: int(m.s.LoginTTL / time.Second)}, nil
	case <-time.After(m.s.ReplyTimeout):
		m.mu.Lock()
		if p.login == l {
			m.endLogin(p, CodeEngineUnavailable, true, false)
		}
		m.mu.Unlock()
		return LoginInfo{}, fail(CodeEngineUnavailable)
	}
}

// Disconnect removes the stored credential, stops the person's process and
// revokes its token.
func (m *Manager) Disconnect(userID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := m.people[userID]
	if p != nil {
		if p.login != nil {
			m.endLogin(p, CodeLoginFailed, true, true)
		}
		m.stop(p, "disconnect", nil)
		p.reconnect = false
		p.events = nil
		p.models = nil
	}
	// A process already stopped but kept for a refresh in flight must not write
	// the refreshed credential back after this.
	for pr := range m.alive {
		if pr.userID == userID {
			pr.discard = true
		}
	}
	err := m.s.Vault.Delete(userID)
	if p != nil {
		m.forget(p)
	}
	return err
}

// Subscribe opens the person's chat stream. Turns, the user's and the
// automatic reactions to live events alike, are told to every open stream.
// The returned function closes it.
func (m *Manager) Subscribe(userID int64) (<-chan Event, func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ch := make(chan Event, subBuffer)
	if m.closed {
		// Shutting down: a stream opened now would hold the server's drain.
		close(ch)
		return ch, func() {}
	}
	p := m.personOf(userID)
	id := p.nextSub
	p.nextSub++
	p.subs[id] = ch
	return ch, func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if c, ok := p.subs[id]; ok {
			delete(p.subs, id)
			close(c)
		}
		if len(p.subs) == 0 && p.proc != nil {
			// Nobody is left to answer: open questions are denied.
			m.denyOpenApprovals(p, p.proc)
		}
		m.forget(p)
	}
}

// Prompt starts one turn with the person's text and photos, in conversation
// conv (0 starts a new one, when conversations are kept). It returns the
// conversation. Refusals are returned before anything starts; after that, every
// outcome arrives on the open streams. Photos go to this turn only and are kept
// nowhere: a stored message counts them.
func (m *Manager) Prompt(userID, conv int64, text string, images ...Image) (int64, error) {
	if utf8.RuneCountInString(text) > maxPromptChar || !utf8.ValidString(text) || !ValidImages(images) {
		return 0, fail(CodeProviderError)
	}
	stored := text
	if text == "" {
		if len(images) == 0 {
			return 0, fail(CodeProviderError)
		}
		text = photoOnlyText
	} else {
		text = guardText(text)
	}
	model, thinking := m.preferences(userID)
	at := m.s.Now()
	conv, history, created, err := m.prepareConversation(userID, conv, conversationTitle(stored))
	if err != nil {
		return 0, err
	}
	memory := m.memoryLines(userID)
	m.mu.Lock()
	err = m.startTurn(m.personOf(userID), turnRequest{
		conv: conv, history: history, memory: memory, text: text, origin: originUser, images: images, model: model, thinking: thinking,
		record: &ConversationMessage{Role: "user", Origin: originUser, Text: stored, Photos: len(images), At: at},
	})
	m.mu.Unlock()
	if err != nil {
		if created {
			m.dropConversation(userID, conv)
		}
		return 0, err
	}
	return conv, nil
}

// prepareConversation finds the person's conversation and its stored messages,
// or creates one when conv is 0. Without a store, everything is conversation 0.
func (m *Manager) prepareConversation(userID, conv int64, title string) (int64, []ConversationMessage, bool, error) {
	st := m.s.Conversations
	if st == nil {
		if conv != 0 {
			return 0, nil, false, fail(CodeNotFound)
		}
		return 0, nil, false, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if conv != 0 {
		_, history, err := st.CoachConversation(ctx, userID, conv, m.cutoff())
		return conv, history, false, storeError(err)
	}
	// No conversation is created for someone who cannot start a turn.
	if c, err := m.s.Vault.Read(userID); err != nil || c == nil {
		return 0, nil, false, nil
	}
	id, err := st.CreateCoachConversation(ctx, userID, title, m.s.Now())
	return id, nil, err == nil, storeError(err)
}

func (m *Manager) dropConversation(userID, conv int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = m.s.Conversations.DeleteCoachConversation(ctx, userID, conv)
}

// storeError maps a store failure to a fixed code.
func storeError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrNoConversation):
		return fail(CodeNotFound)
	default:
		return fail(CodeEngineUnavailable)
	}
}

const maxTitleRunes = 80

// conversationTitle is the first line of the first message, cut short.
func conversationTitle(text string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	line = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return ' '
		}
		return r
	}, line))
	if line == "" {
		return "Foto"
	}
	if r := []rune(line); len(r) > 60 {
		line = strings.TrimSpace(string(r[:60])) + "…"
	}
	return line
}

// Conversations lists the person's kept conversations, newest first.
func (m *Manager) Conversations(userID int64) ([]Conversation, error) {
	st := m.s.Conversations
	if st == nil {
		return []Conversation{}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	list, err := st.CoachConversations(ctx, userID, m.cutoff())
	if err != nil {
		return nil, storeError(err)
	}
	return list, nil
}

// Conversation returns one of the person's conversations with its messages.
func (m *Manager) Conversation(userID, conv int64) (Conversation, []ConversationMessage, error) {
	st := m.s.Conversations
	if st == nil {
		return Conversation{}, nil, fail(CodeNotFound)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, msgs, err := st.CoachConversation(ctx, userID, conv, m.cutoff())
	return c, msgs, storeError(err)
}

// OpenConversation makes conv the person's open conversation (0: a new one),
// where reactions to logged sets go.
func (m *Manager) OpenConversation(userID, conv int64) error {
	if conv != 0 {
		if _, _, err := m.Conversation(userID, conv); err != nil {
			return err
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p := m.personOf(userID)
	p.conv = conv
	m.forget(p)
	return nil
}

// RenameConversation gives one of the person's conversations a new title.
func (m *Manager) RenameConversation(userID, conv int64, title string) error {
	title = strings.TrimSpace(title)
	if !validModelName(title) {
		return fail(CodeBadRequest)
	}
	st := m.s.Conversations
	if st == nil {
		return fail(CodeNotFound)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return storeError(st.RenameCoachConversation(ctx, userID, conv, title))
}

// DeleteConversation deletes one of the person's conversations; an engine that
// holds it in its context is stopped.
func (m *Manager) DeleteConversation(userID, conv int64) error {
	st := m.s.Conversations
	if st == nil {
		return fail(CodeNotFound)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := st.DeleteCoachConversation(ctx, userID, conv); err != nil {
		return storeError(err)
	}
	m.forgetConversations(userID, func(c int64) bool { return c == conv })
	return nil
}

// DeleteConversations deletes all of the person's conversations («borrar todo»).
func (m *Manager) DeleteConversations(userID int64) error {
	st := m.s.Conversations
	if st == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := st.DeleteCoachConversations(ctx, userID); err != nil {
		return storeError(err)
	}
	m.forgetConversations(userID, func(int64) bool { return true })
	return nil
}

// forgetConversations closes deleted conversations in memory: the open one,
// and an engine whose context holds one.
func (m *Manager) forgetConversations(userID int64, deleted func(int64) bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := m.people[userID]
	if p == nil {
		return
	}
	if deleted(p.conv) {
		p.conv = 0
	}
	m.dropConversationAnswers(func(a *conversationAnswers) bool { return a.user == userID && deleted(a.conv) })
	if p.proc != nil && p.proc.used && deleted(p.proc.conv) {
		m.stop(p, "conversation_deleted", nil)
	}
	m.forget(p)
}

// preferences reads the person's choice; any failure means the engine's default.
func (m *Manager) preferences(userID int64) (string, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	model, thinking, err := m.s.Preferences.CoachPreferences(ctx, userID)
	if err != nil {
		return "", ""
	}
	return model, thinking
}

// Models returns the models the person's ChatGPT login can use, as their own
// engine reports them, and the current choice. The first call starts the
// person's engine and waits for its list; later calls answer from memory.
func (m *Manager) Models(userID int64) (ModelList, error) {
	model, thinking := m.preferences(userID)
	m.mu.Lock()
	p := m.personOf(userID)
	// The list belongs to the login it came from: no login, no list.
	if err := m.connected(p); err != nil {
		p.models = nil
		m.forget(p)
		m.mu.Unlock()
		return ModelList{}, err
	}
	list := p.models
	var wait chan struct{}
	if list == nil {
		var err error
		if wait, err = m.requestModels(p); err != nil {
			m.forget(p)
			m.mu.Unlock()
			return ModelList{}, err
		}
	}
	m.mu.Unlock()
	if wait != nil {
		select {
		case <-wait:
		case <-time.After(m.s.ReplyTimeout):
			return ModelList{}, fail(CodeEngineUnavailable)
		}
		m.mu.Lock()
		list = p.models
		code := p.modelErr
		m.mu.Unlock()
		if list == nil {
			if code == "" {
				code = CodeEngineUnavailable
			}
			return ModelList{}, fail(code)
		}
	}
	out := ModelList{Models: append([]ModelInfo(nil), list.Models...), Model: list.Model, Thinking: list.Thinking}
	// A stored choice the account no longer offers is not reported.
	if mi := out.find(model); mi != nil && mi.takes(thinking) {
		out.Model, out.Thinking = model, thinking
	}
	return out, nil
}

// SetPreferences keeps the person's choice of model and thinking level. Only a
// model their own engine listed, with a level that model takes, is accepted:
// a model outside the person's ChatGPT login can never be chosen here.
func (m *Manager) SetPreferences(userID int64, model, thinking string) error {
	list, err := m.Models(userID)
	if err != nil {
		return err
	}
	mi := list.find(model)
	if mi == nil || !mi.takes(thinking) {
		return fail(CodeBadRequest)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := m.s.Preferences.SetCoachPreferences(ctx, userID, model, thinking); err != nil {
		return fail(CodeEngineUnavailable)
	}
	return nil
}

// requestModels asks the person's engine for its model list, once at a time.
func (m *Manager) requestModels(p *person) (chan struct{}, error) {
	if p.modelWait != nil {
		return p.modelWait, nil
	}
	if err := m.connected(p); err != nil {
		return nil, err
	}
	pr, err := m.startEngine(p)
	if err != nil {
		return nil, err
	}
	p.modelWait = make(chan struct{})
	p.modelErr = ""
	m.send(pr, parentMessage{Type: "models.get"})
	m.touch(p, pr)
	return p.modelWait, nil
}

// endModels wakes whoever waits for a model list.
func (m *Manager) endModels(p *person, code ErrorCode) {
	if p.modelWait == nil {
		return
	}
	p.modelErr = code
	close(p.modelWait)
	p.modelWait = nil
}

// connected refuses a person without a usable stored credential.
func (m *Manager) connected(p *person) error {
	c, err := m.s.Vault.Read(p.id)
	if err != nil {
		p.reconnect = true
		return fail(CodeReconnect)
	}
	if c == nil {
		return fail(CodeNotConnected)
	}
	return nil
}

// startEngine returns the person's process, started with its MCP token.
func (m *Manager) startEngine(p *person) (*proc, error) {
	pr, err := m.ensureProc(p, true)
	if err != nil {
		return nil, err
	}
	if pr.token == "" {
		pr.token = m.s.Tokens.Mint(p.id, m.s.MaxAge)
		pr.cs = m.newConnectorState(p.id, pr.token)
		m.send(pr, parentMessage{Type: "start", MCPURL: m.s.MCPURL, MCPToken: pr.token, SystemPrompt: m.s.SystemPrompt, Servers: m.engineServers(pr)})
	}
	return pr, nil
}

// Abort asks the running turn of this person to stop. False when there is none.
func (m *Manager) Abort(userID int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := m.people[userID]
	if p == nil || p.proc == nil || p.proc.turn == nil {
		return false
	}
	m.requestAbort(p, p.proc, "abort_timeout")
	return true
}

// Notify queues a live workout event for the person's coach. It is delivered as
// host-written context at the start of the next turn. With a chat stream open
// and no turn running, it also starts a short automatic reaction.
func (m *Manager) Notify(userID int64, ev WorkoutEvent) {
	model, thinking := m.preferences(userID)
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	p := m.personOf(userID)
	m.dropStaleEvents(p)
	p.events = append(p.events, ev)
	if over := len(p.events) - m.s.MaxPendingEvents; over > 0 {
		p.events = append([]WorkoutEvent(nil), p.events[over:]...)
	}
	canStart := func(p *person) bool { return len(p.subs) > 0 && (p.proc == nil || p.proc.turn == nil) }
	if !canStart(p) {
		m.mu.Unlock()
		return
	}
	conv := p.conv
	m.mu.Unlock()
	// The reaction goes to the open conversation, or a new one.
	conv, history, created, err := m.prepareConversation(userID, conv, "Entreno del "+m.s.Now().Format("02/01"))
	if err != nil {
		conv, history, created, err = m.prepareConversation(userID, 0, "Entreno del "+m.s.Now().Format("02/01"))
	}
	memory := ""
	if err == nil {
		memory = m.memoryLines(userID)
	}
	m.mu.Lock()
	p = m.personOf(userID)
	started := false
	if err == nil && !m.closed && canStart(p) {
		// A refusal (not connected, busy) leaves the event queued for later.
		started = m.startTurn(p, turnRequest{conv: conv, history: history, memory: memory, text: reactionText, origin: originEvent, model: model, thinking: thinking}) == nil
	}
	m.mu.Unlock()
	if created && !started {
		m.dropConversation(userID, conv)
	}
}

// PendingEvents is how many live events wait for the person's next turn.
func (m *Manager) PendingEvents(userID int64) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p := m.people[userID]; p != nil {
		return len(p.events)
	}
	return 0
}

// Close stops every process and revokes every token; the manager accepts no work
// afterwards. It waits, bounded by ctx, for the processes to exit.
func (m *Manager) Close(ctx context.Context) {
	m.mu.Lock()
	if !m.closed {
		close(m.quit)
	}
	m.closed = true
	for _, p := range m.people {
		if p.login != nil {
			m.endLogin(p, CodeEngineUnavailable, true, true)
		}
		m.stop(p, "shutdown", nil)
		for id, c := range p.subs {
			delete(p.subs, id)
			close(c)
		}
	}
	var waits []chan struct{}
	for pr := range m.alive {
		waits = append(waits, pr.done)
	}
	if !m.writesClosed {
		m.writesClosed = true
		close(m.writes)
	}
	m.mu.Unlock()
	// Messages already queued are stored before the database closes.
	waits = append(waits, m.writesDone)
	for _, d := range waits {
		select {
		case <-d:
		case <-ctx.Done():
			m.mu.Lock()
			for pr := range m.alive {
				if pr.cmd.Process != nil {
					pr.cmd.Process.Kill()
				}
			}
			m.mu.Unlock()
			return
		}
	}
}

// --- internals, all called with m.mu held unless noted ---

func (m *Manager) personOf(userID int64) *person {
	p := m.people[userID]
	if p == nil {
		p = &person{id: userID, subs: map[int]chan Event{}}
		m.people[userID] = p
	}
	return p
}

// eventTTL: a set older than this says nothing about the workout in progress.
const eventTTL = 3 * time.Hour

func (m *Manager) dropStaleEvents(p *person) {
	now := m.s.Now()
	kept := p.events[:0]
	for _, ev := range p.events {
		if now.Sub(ev.At) < eventTTL {
			kept = append(kept, ev)
		}
	}
	p.events = kept
}

func (m *Manager) pruneStarts(p *person) {
	now := m.s.Now()
	kept := p.loginStarts[:0]
	for _, at := range p.loginStarts {
		if now.Sub(at) < m.s.LoginWindow {
			kept = append(kept, at)
		}
	}
	p.loginStarts = kept
}

// forget drops a person the manager no longer needs to remember anything about.
func (m *Manager) forget(p *person) {
	m.pruneStarts(p)
	if p.proc != nil || p.login != nil || p.refreshing != nil || p.reconnect || len(p.loginStarts) > 0 || len(p.subs) > 0 || len(p.events) > 0 {
		return
	}
	if m.people[p.id] == p {
		delete(m.people, p.id)
	}
}

func (m *Manager) startTurn(p *person, r turnRequest) error {
	if m.closed {
		return fail(CodeEngineUnavailable)
	}
	if p.proc != nil && p.proc.turn != nil {
		return fail(CodeTurnInProgress)
	}
	if err := m.connected(p); err != nil {
		return err
	}
	// A process whose token would expire during this turn is replaced first.
	if p.proc != nil && p.login == nil && p.proc.token != "" && m.s.Now().Sub(p.proc.startedAt) > m.s.MaxAge-m.s.TurnTimeout {
		m.stop(p, "max_age", nil)
	}
	// An engine holds the context of the conversation it served; another
	// conversation gets a fresh one.
	if p.proc != nil && p.proc.used && p.proc.conv != r.conv {
		m.stop(p, "switch_conversation", nil)
	}
	pr, err := m.startEngine(p)
	if err != nil {
		return err
	}
	fresh := !pr.used
	pr.used, pr.conv, p.conv = true, r.conv, r.conv
	t := &turnState{conv: r.conv, origin: r.origin}
	t.timeout = time.AfterFunc(m.s.TurnTimeout, func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.limitTurn(p, pr, t)
	})
	pr.turn = t
	m.dropStaleEvents(p)
	// A fresh engine gets the person's memory, then the conversation so far,
	// then this turn's lines.
	contextLines := composeContext(m.s.Now(), p.events)
	if fresh && len(r.history) > 0 {
		contextLines = historyContext(r.history) + "\n" + contextLines
	}
	if fresh && r.memory != "" {
		contextLines = r.memory + "\n" + contextLines
	}
	p.events = nil
	if r.record != nil {
		m.persist(p.id, r.conv, *r.record)
	}
	m.broadcast(p, Event{Name: "turn", Data: map[string]any{"origin": r.origin, "conversation": r.conv}})
	m.send(pr, parentMessage{Type: "prompt", Text: r.text, Context: contextLines, Images: toPromptImages(r.images), Model: r.model, Thinking: r.thinking})
	m.touch(p, pr)
	return nil
}

func (m *Manager) ensureProc(p *person, forTurn bool) (*proc, error) {
	if m.closed {
		return nil, fail(CodeEngineUnavailable)
	}
	if p.proc != nil {
		return p.proc, nil
	}
	if forTurn && m.liveCount() >= m.s.MaxProcesses {
		m.evictPendingLogin()
	}
	if m.liveCount() >= m.s.MaxProcesses {
		return nil, fail(CodeBusy)
	}
	cmd := m.s.Command()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fail(CodeEngineUnavailable)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fail(CodeEngineUnavailable)
	}
	// stderr is discarded: provider errors can quote response bodies.
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		m.log("start_failed", "spawn_error")
		return nil, fail(CodeEngineUnavailable)
	}
	pr := &proc{cmd: cmd, userID: p.id, out: make(chan []byte, outBuffer), startedAt: m.s.Now(), refreshes: map[string]chan *Credential{}, done: make(chan struct{}), cs: connectorState{servers: map[string]string{}, answers: map[string]sessionAnswer{}, approvals: map[string]*approval{}, tickets: map[string][]time.Time{}}}
	p.proc = pr
	// A stop just before (max age, eviction) may have forgotten this person; a
	// process must always belong to a tracked person, or no cap, status,
	// disconnect or shutdown would reach it.
	m.people[p.id] = p
	m.alive[pr] = true
	go writeLoop(stdin, pr.out)
	go m.readLoop(p, pr, stdout)
	go func() {
		cmd.Wait()
		m.mu.Lock()
		defer m.mu.Unlock()
		pr.exited = true
		delete(m.alive, pr)
		close(pr.done)
		for _, ch := range pr.refreshes {
			select {
			case ch <- nil:
			default:
			}
		}
		m.stop(p, "exit", pr)
	}()
	m.log("start", "first_use")
	m.touch(p, pr)
	return pr, nil
}

// writeLoop owns the engine's stdin. Closing out ends the process's input,
// which an engine takes as the signal to exit.
func writeLoop(stdin io.WriteCloser, out <-chan []byte) {
	for line := range out {
		if _, err := stdin.Write(line); err != nil {
			break
		}
	}
	stdin.Close()
	for range out {
	}
}

func (m *Manager) readLoop(p *person, pr *proc, stdout io.Reader) {
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64<<10), maxLineBytes)
	for sc.Scan() {
		msg, ok := parseChildMessage(sc.Bytes())
		if !ok {
			continue
		}
		m.mu.Lock()
		// A stopped process is heard only for the refresh it was already asked for.
		if !pr.stopped || msg.Type == "refresh.done" || msg.Type == "refresh.failed" {
			m.handle(p, pr, msg)
		}
		m.mu.Unlock()
	}
	if errors.Is(sc.Err(), bufio.ErrTooLong) {
		m.mu.Lock()
		m.stop(p, "protocol", pr)
		m.mu.Unlock()
	}
	io.Copy(io.Discard, stdout)
}

// evictPendingLogin: a connected person's turn comes before a process that only
// waits for a device code.
func (m *Manager) evictPendingLogin() {
	var oldest *person
	for _, p := range m.people {
		if p.proc == nil || p.login == nil || p.proc.turn != nil {
			continue
		}
		if oldest == nil || p.proc.startedAt.Before(oldest.proc.startedAt) {
			oldest = p
		}
	}
	if oldest != nil {
		m.endLogin(oldest, CodeBusy, true, false)
		if oldest.proc != nil {
			m.stop(oldest, "evicted", nil)
		}
	}
}

func (m *Manager) handle(p *person, pr *proc, msg childMessage) {
	switch msg.Type {
	case "ready":
	case "login.code":
		if p.login != nil && p.proc == pr && p.login.userCode == "" {
			p.login.userCode = msg.UserCode
			select {
			case p.login.result <- loginResult{userCode: msg.UserCode}:
			default:
			}
		}
	case "login.done":
		if p.login == nil || p.proc != pr {
			return
		}
		if err := m.s.Vault.Write(p.id, msg.Credential); err != nil {
			m.endLogin(p, CodeEngineUnavailable, false, false)
			return
		}
		p.reconnect = false
		p.models = nil // a new login may be another account
		m.endLogin(p, CodeLoginFailed, false, false)
	case "login.failed":
		if p.login != nil && p.proc == pr {
			m.endLogin(p, CodeLoginFailed, false, false)
		}
	case "refresh.done", "refresh.failed":
		if ch, ok := pr.refreshes[msg.ID]; ok {
			select {
			case ch <- msg.Credential: // nil on failure
			default:
			}
		}
	case "token.get":
		go m.answerToken(p, pr, msg.ID)
	case "session":
		// Fail closed: training-mcp's MCP tools and the tools of the servers
		// the host gave this session, nothing else.
		if !allowedTools(msg.Tools, pr.cs.servers) {
			m.stop(p, "unexpected_tools", pr)
			return
		}
		pr.toolsChecked = true
		m.noteLoaded(p, pr, msg.Tools)
	case "approval.request":
		if !pr.toolsChecked {
			m.send(pr, parentMessage{Type: "approval", ID: msg.ID, Decision: "deny"})
			return
		}
		m.handleApproval(p, pr, msg)
	case "delta", "thinking":
		t := pr.turn
		if t == nil || t.limited || !pr.toolsChecked {
			return
		}
		t.bytes += len(msg.Text)
		if t.bytes > m.s.TurnMaxBytes {
			m.limitTurn(p, pr, t)
			return
		}
		if msg.Type == "delta" {
			t.text.WriteString(msg.Text)
		}
		m.broadcast(p, Event{Name: msg.Type, Data: map[string]any{"text": msg.Text}})
	case "models":
		if !pr.toolsChecked || p.proc != pr {
			return
		}
		p.models = &ModelList{Models: msg.Models, Model: msg.Model, Thinking: msg.Thinking}
		m.endModels(p, "")
	case "models.failed":
		if p.proc == pr {
			m.endModels(p, msg.Code)
		}
	case "tool":
		if !pr.toolsChecked || pr.turn == nil || pr.turn.limited {
			return
		}
		data := map[string]any{"name": strings.TrimPrefix(msg.Name, MCPToolPrefix), "phase": msg.Phase}
		if msg.Phase == "end" {
			data["ok"] = msg.OK
			outcome := "ok"
			if !msg.OK {
				outcome = "error"
			} else if msg.Pending {
				outcome = "pending"
			}
			if len(pr.turn.steps) < 64 {
				pr.turn.steps = append(pr.turn.steps, data["name"].(string)+":"+outcome)
			}
		}
		if msg.Pending {
			data["pending"] = true
		}
		if msg.Call > 0 {
			data["call"] = msg.Call
		}
		m.broadcast(p, Event{Name: "tool", Data: data})
	case "done":
		if !pr.toolsChecked {
			m.stop(p, "unexpected_tools", pr)
			return
		}
		m.endTurn(p, pr, "done", map[string]any{"aborted": msg.Aborted})
	case "error":
		if msg.Code == CodeReconnect {
			p.reconnect = true
		}
		m.endTurn(p, pr, "error", map[string]any{"code": string(msg.Code)})
	}
}

func (m *Manager) answerToken(p *person, pr *proc, id string) {
	c, err := m.freshCredential(p, pr)
	m.mu.Lock()
	defer m.mu.Unlock()
	if err != nil {
		code := CodeEngineUnavailable
		var e *Error
		if errors.As(err, &e) {
			code = e.Code
		}
		m.send(pr, parentMessage{Type: "token", ID: id, Error: code})
		return
	}
	m.send(pr, parentMessage{Type: "token", ID: id, Access: c.Access, AccountID: c.AccountID, Expires: c.Expires})
}

// freshCredential is single flight per person: every caller that arrives during
// a refresh waits for that one refresh. Called without m.mu.
func (m *Manager) freshCredential(p *person, pr *proc) (*Credential, error) {
	m.mu.Lock()
	if call := p.refreshing; call != nil {
		m.mu.Unlock()
		<-call.done
		return call.cred, call.err
	}
	c, err := m.s.Vault.Read(p.id)
	if err != nil {
		p.reconnect = true
		m.mu.Unlock()
		return nil, fail(CodeReconnect)
	}
	if c == nil {
		m.mu.Unlock()
		return nil, fail(CodeNotConnected)
	}
	if time.UnixMilli(c.Expires).Sub(m.s.Now()) > m.s.RefreshMargin {
		m.mu.Unlock()
		return c, nil
	}
	if pr.stopped || pr.exited {
		m.mu.Unlock()
		return nil, fail(CodeEngineUnavailable)
	}
	call := &refreshCall{done: make(chan struct{})}
	p.refreshing = call
	id := randomID()
	reply := make(chan *Credential, 1)
	pr.refreshes[id] = reply
	m.send(pr, parentMessage{Type: "refresh", ID: id, Credential: c})
	m.mu.Unlock()

	var next *Credential
	select {
	case next = <-reply:
	case <-time.After(m.s.ReplyTimeout):
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	delete(pr.refreshes, id)
	switch {
	case next != nil && !pr.discard:
		// The old refresh token is spent once the new one exists, so the new one
		// is stored even if the process was stopped meanwhile. Only a disconnect
		// throws it away.
		if err := m.s.Vault.Write(p.id, next); err != nil {
			p.reconnect = true
			call.err = fail(CodeReconnect)
		} else {
			p.reconnect = false
			call.cred = next
		}
	default:
		// The stored credential is left as it was; the person connects again.
		if !pr.discard {
			p.reconnect = true
		}
		call.err = fail(CodeReconnect)
	}
	if p.refreshing == call {
		p.refreshing = nil
	}
	close(call.done)
	if pr.stopped && len(pr.refreshes) == 0 {
		m.teardown(pr)
	}
	m.forget(p)
	return call.cred, call.err
}

// endLogin ends a pending login. A process left with no pending login and no
// stored credential has nothing to do and is stopped, unless keepProcess.
func (m *Manager) endLogin(p *person, code ErrorCode, cancel, keepProcess bool) {
	l := p.login
	if l == nil {
		return
	}
	p.login = nil
	l.expiry.Stop()
	select {
	case l.result <- loginResult{code: code}:
	default:
	}
	if cancel && p.proc != nil {
		m.send(p.proc, parentMessage{Type: "login.cancel"})
	}
	if keepProcess || p.proc == nil || p.proc.turn != nil {
		return
	}
	if c, err := m.s.Vault.Read(p.id); err == nil && c == nil {
		m.stop(p, "login_ended", nil)
	}
}

func (m *Manager) limitTurn(p *person, pr *proc, t *turnState) {
	if pr.turn != t || t.limited {
		return
	}
	t.limited = true
	m.requestAbort(p, pr, "turn_limit")
}

func (m *Manager) requestAbort(p *person, pr *proc, reason string) {
	t := pr.turn
	if t == nil {
		return
	}
	m.send(pr, parentMessage{Type: "abort"})
	if t.abortTimer == nil {
		t.abortTimer = time.AfterFunc(m.s.AbortGrace, func() {
			m.mu.Lock()
			defer m.mu.Unlock()
			if pr.turn == t {
				m.stop(p, reason, pr)
			}
		})
	}
}

func (m *Manager) endTurn(p *person, pr *proc, name string, data map[string]any) {
	t := pr.turn
	if t == nil {
		return
	}
	pr.turn = nil
	m.denyOpenApprovals(p, pr)
	t.timeout.Stop()
	if t.abortTimer != nil {
		t.abortTimer.Stop()
	}
	if t.limited {
		m.broadcast(p, Event{Name: "error", Data: map[string]any{"code": string(CodeTurnLimit)}})
	} else {
		m.broadcast(p, Event{Name: name, Data: data})
	}
	// What the coach said, even cut short, is kept with its steps.
	if t.text.Len() > 0 || len(t.steps) > 0 {
		m.persist(p.id, t.conv, ConversationMessage{Role: "assistant", Origin: t.origin, Text: t.text.String(), Steps: t.steps, At: m.s.Now()})
	}
	// Only a reply that ended on its own can close with learnings.
	if name == "done" && !t.limited && data["aborted"] != true {
		m.captureLearnings(p.id, t.conv, t.text.String())
	}
	if pr.cs.reload && p.proc == pr {
		m.stop(p, "connectors_changed", pr)
		return
	}
	m.touch(p, pr)
}

func (m *Manager) touch(p *person, pr *proc) {
	if pr.idle != nil {
		pr.idle.Stop()
	}
	if pr.stopped {
		return
	}
	pr.idle = time.AfterFunc(m.s.Idle, func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if pr.stopped {
			return
		}
		// A pending login has its own deadline, and a turn its own limit.
		if pr.turn != nil || (p.login != nil && p.proc == pr) {
			m.touch(p, pr)
			return
		}
		m.stop(p, "idle", pr)
	})
}

// broadcast tells every open stream. A stream that cannot keep up is closed
// rather than handed a gap in the middle of an answer.
func (m *Manager) broadcast(p *person, ev Event) {
	for id, ch := range p.subs {
		select {
		case ch <- ev:
		default:
			delete(p.subs, id)
			close(ch)
		}
	}
}

func (m *Manager) send(pr *proc, msg parentMessage) {
	if pr.stopped || pr.exited || pr.tornDown {
		return
	}
	line, err := json.Marshal(msg)
	if err != nil {
		return
	}
	select {
	case pr.out <- append(line, '\n'):
	default:
		// The engine stopped reading its input; it is of no further use.
		go func() {
			if pr.cmd.Process != nil {
				pr.cmd.Process.Kill()
			}
		}()
	}
}

// stop detaches a process from its person at once: the slot is free, the token
// is revoked, the turn and the login end. The process itself is torn down right
// away, unless a refresh was already sent to it: then it is kept, bounded, until
// that refresh is back.
func (m *Manager) stop(p *person, reason string, only *proc) {
	pr := only
	if pr == nil {
		pr = p.proc
	}
	if pr == nil || pr.stopped {
		return
	}
	if reason == "disconnect" {
		pr.discard = true
	}
	if p.proc == pr {
		p.proc = nil
	}
	m.log("stop", reason)
	if pr.turn != nil {
		m.endTurn(p, pr, "error", map[string]any{"code": string(CodeEngineUnavailable)})
	}
	m.endModels(p, CodeEngineUnavailable)
	pr.stopped = true
	if pr.idle != nil {
		pr.idle.Stop()
	}
	if pr.token != "" {
		m.s.Tokens.Revoke(pr.token)
	}
	if p.login != nil {
		m.endLogin(p, CodeEngineUnavailable, false, true)
	}
	if len(pr.refreshes) == 0 || pr.discard || pr.exited {
		for _, ch := range pr.refreshes {
			select {
			case ch <- nil:
			default:
			}
		}
		m.teardown(pr)
	} else {
		time.AfterFunc(m.s.RefreshGrace, func() {
			m.mu.Lock()
			defer m.mu.Unlock()
			m.teardown(pr)
		})
	}
	m.forget(p)
}

func (m *Manager) teardown(pr *proc) {
	if pr.tornDown {
		return
	}
	if !pr.exited {
		if line, err := json.Marshal(parentMessage{Type: "shutdown"}); err == nil {
			select {
			case pr.out <- append(line, '\n'):
			default:
			}
		}
	}
	pr.tornDown = true
	close(pr.out)
	if pr.exited {
		return
	}
	time.AfterFunc(killGrace, func() {
		select {
		case <-pr.done:
		default:
			if pr.cmd.Process != nil {
				pr.cmd.Process.Kill()
			}
		}
	})
}

// log writes metadata only: no person, no token, nothing the engine said.
func (m *Manager) log(action, reason string) {
	line, _ := json.Marshal(map[string]any{"event": "engine.process", "action": action, "reason": reason, "live": m.liveCount()})
	m.s.Logger.Print(string(line))
}

func randomID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
