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

	Now    func() time.Time
	Logger *log.Logger
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
	if s.Now == nil {
		s.Now = time.Now
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
}

type proc struct {
	cmd          *exec.Cmd
	userID       int64
	out          chan []byte
	startedAt    time.Time
	token        string
	toolsChecked bool
	idle         *time.Timer
	turn         *turnState
	refreshes    map[string]chan *Credential
	stopped      bool
	discard      bool
	exited       bool
	tornDown     bool
	done         chan struct{}
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
}

// Manager starts, feeds and stops engine processes: at most one per person,
// started on first use and stopped when idle. It owns the stored credentials
// and is the only place that refreshes one, so a rotating refresh token is never
// used twice.
type Manager struct {
	s      Settings
	mu     sync.Mutex
	people map[int64]*person
	alive  map[*proc]bool
	closed bool
}

func NewManager(s Settings) (*Manager, error) {
	if s.Vault == nil || s.Tokens == nil || s.Command == nil || s.MCPURL == "" {
		return nil, errors.New("engine: vault, tokens, command and MCP URL are required")
	}
	s.defaults()
	return &Manager{s: s, people: map[int64]*person{}, alive: map[*proc]bool{}}, nil
}

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
		m.forget(p)
	}
}

// Prompt starts one turn with the person's text and photos. Refusals are
// returned before anything starts; after that, every outcome arrives on the open
// streams. Photos go to this turn only and are kept nowhere.
func (m *Manager) Prompt(userID int64, text string, images ...Image) error {
	if utf8.RuneCountInString(text) > maxPromptChar || !utf8.ValidString(text) || !ValidImages(images) {
		return fail(CodeProviderError)
	}
	if text == "" {
		if len(images) == 0 {
			return fail(CodeProviderError)
		}
		text = photoOnlyText
	} else {
		text = guardText(text)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.startTurn(m.personOf(userID), text, originUser, images)
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
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	p := m.personOf(userID)
	m.dropStaleEvents(p)
	p.events = append(p.events, ev)
	if over := len(p.events) - m.s.MaxPendingEvents; over > 0 {
		p.events = append([]WorkoutEvent(nil), p.events[over:]...)
	}
	if len(p.subs) > 0 && (p.proc == nil || p.proc.turn == nil) {
		// A refusal (not connected, busy) leaves the event queued for later.
		_ = m.startTurn(p, reactionText, originEvent, nil)
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
	m.mu.Unlock()
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

func (m *Manager) startTurn(p *person, text, origin string, images []Image) error {
	if m.closed {
		return fail(CodeEngineUnavailable)
	}
	if p.proc != nil && p.proc.turn != nil {
		return fail(CodeTurnInProgress)
	}
	c, err := m.s.Vault.Read(p.id)
	if err != nil {
		p.reconnect = true
		return fail(CodeReconnect)
	}
	if c == nil {
		return fail(CodeNotConnected)
	}
	// A process whose token would expire during this turn is replaced first.
	if p.proc != nil && p.login == nil && p.proc.token != "" && m.s.Now().Sub(p.proc.startedAt) > m.s.MaxAge-m.s.TurnTimeout {
		m.stop(p, "max_age", nil)
	}
	pr, err := m.ensureProc(p, true)
	if err != nil {
		return err
	}
	if pr.token == "" {
		pr.token = m.s.Tokens.Mint(p.id, m.s.MaxAge)
		m.send(pr, parentMessage{Type: "start", MCPURL: m.s.MCPURL, MCPToken: pr.token, SystemPrompt: m.s.SystemPrompt})
	}
	t := &turnState{}
	t.timeout = time.AfterFunc(m.s.TurnTimeout, func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.limitTurn(p, pr, t)
	})
	pr.turn = t
	m.dropStaleEvents(p)
	contextLines := composeContext(m.s.Now(), p.events)
	p.events = nil
	m.broadcast(p, Event{Name: "turn", Data: map[string]any{"origin": origin}})
	m.send(pr, parentMessage{Type: "prompt", Text: text, Context: contextLines, Images: toPromptImages(images)})
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
	pr := &proc{cmd: cmd, userID: p.id, out: make(chan []byte, outBuffer), startedAt: m.s.Now(), refreshes: map[string]chan *Credential{}, done: make(chan struct{})}
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
		// Fail closed: training-mcp's MCP tools and nothing else.
		if !onlyTrainingTools(msg.Tools) {
			m.stop(p, "unexpected_tools", pr)
			return
		}
		pr.toolsChecked = true
	case "delta":
		t := pr.turn
		if t == nil || t.limited || !pr.toolsChecked {
			return
		}
		t.bytes += len(msg.Text)
		if t.bytes > m.s.TurnMaxBytes {
			m.limitTurn(p, pr, t)
			return
		}
		m.broadcast(p, Event{Name: "delta", Data: map[string]any{"text": msg.Text}})
	case "tool":
		if !pr.toolsChecked || pr.turn == nil || pr.turn.limited {
			return
		}
		data := map[string]any{"name": strings.TrimPrefix(msg.Name, MCPToolPrefix), "phase": msg.Phase}
		if msg.Phase == "end" {
			data["ok"] = msg.OK
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
	t.timeout.Stop()
	if t.abortTimer != nil {
		t.abortTimer.Stop()
	}
	if t.limited {
		m.broadcast(p, Event{Name: "error", Data: map[string]any{"code": string(CodeTurnLimit)}})
	} else {
		m.broadcast(p, Event{Name: name, Data: data})
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
