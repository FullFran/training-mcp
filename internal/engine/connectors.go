package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/fullfran/training-mcp/internal/training"
)

// Remote MCP servers a person connected ("connectors") reach the engine as
// extra MCP servers next to training-mcp's own. The engine dials them on
// loopback, through the host's proxy (internal/engine/connector), with the same
// engine token; it never holds a third-party credential.
//
// Every call of a connector tool needs a permission the host decides
// (HCS-0005): the engine asks before each call, the host answers from a session
// grant or denial, or asks the person on their chat stream. Only an explicit
// allow lets a call through, and each allow gives the proxy exactly one call.

// Server is one connected server, as the engine registers it: Name is the
// person's slug (lowercase letters and digits), so its tools are
// mcp__<name>__<tool>.
type Server struct {
	Name  string
	Label string
}

// Connectors lists a person's connected servers for a new engine session.
type Connectors interface {
	ForEngine(userID int64) []Server
}

// Answers a person can give to a permission request, as ACP names them. An
// "always" answer is held by the host and lasts until the session ends or the
// person revokes it (HCS-0005-R006, R007).
// They are the same four answers training-data proposals take (#75).
const (
	AnswerAllowOnce    = training.AnswerAllowOnce
	AnswerAllowAlways  = training.AnswerAllowAlways
	AnswerRejectOnce   = training.AnswerRejectOnce
	AnswerRejectAlways = training.AnswerRejectAlways
)

var (
	// ErrNoApproval: no open request with that id for this person.
	ErrNoApproval = errors.New("no such approval request")
	// ErrBadAnswer: not one of the four answers.
	ErrBadAnswer = errors.New("unknown answer")
)

const (
	defaultApprovalTimeout = 2 * time.Minute
	maxApprovalArgs        = 2000
	// approvalChannel is the person's own channel, the signed-in web app, as
	// for proposals (HCS-0005-R015).
	approvalChannel = training.ChannelWeb
	// ticketTTL bounds how long an allowed call may take to reach the proxy.
	ticketTTL = 2 * time.Minute
)

var reServerName = regexp.MustCompile(`^[a-z][a-z0-9]{0,23}$`)

// ValidServerName reports whether a slug can name a connector server.
func ValidServerName(name string) bool {
	return reServerName.MatchString(name) && name != MCPServerName
}

type approval struct {
	hostID   string
	engineID string
	tool     string
	timer    *time.Timer
}

// sessionAnswer is an always answer held for one session.
type sessionAnswer struct {
	answer string
	origin string // the decision record of the answer
}

// Grant is an always answer the person gave in the current session.
type Grant struct {
	Tool   string `json:"tool"`
	Server string `json:"server"`
	Answer string `json:"answer"`
}

// connectorState is the part of a process that belongs to connectors.
type connectorState struct {
	session   string
	servers   map[string]string // name -> label
	answers   map[string]sessionAnswer
	approvals map[string]*approval // by host id
	tickets   map[string][]time.Time
	reload    bool
}

// coachSession is the HCS-0005 session of one coach conversation: the same id
// mcpsrv derives from the engine bearer for that engine's /mcp calls, so
// connector approvals and training-data proposals share one session.
func coachSession(token string) string {
	sum := sha256.Sum256([]byte(token))
	return training.CoachSessionPrefix + hex.EncodeToString(sum[:12])
}

func (m *Manager) newConnectorState(userID int64, token string) connectorState {
	cs := connectorState{
		session:   coachSession(token),
		servers:   map[string]string{},
		answers:   map[string]sessionAnswer{},
		approvals: map[string]*approval{},
		tickets:   map[string][]time.Time{},
	}
	if m.s.Connectors != nil {
		for _, s := range m.s.Connectors.ForEngine(userID) {
			if ValidServerName(s.Name) {
				cs.servers[s.Name] = s.Label
			}
		}
	}
	return cs
}

// engineServers is what the start message carries: names and proxy URLs.
func (m *Manager) engineServers(pr *proc) []engineServer {
	var out []engineServer
	for name := range pr.cs.servers {
		out = append(out, engineServer{Name: name, URL: m.s.ConnectorURL + name + "/mcp"})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// serverOf is the connector a tool belongs to, or "".
func serverOf(tool string, servers map[string]string) string {
	rest, ok := strings.CutPrefix(tool, "mcp__")
	if !ok {
		return ""
	}
	name, short, ok := strings.Cut(rest, "__")
	if !ok || short == "" {
		return ""
	}
	if _, known := servers[name]; !known {
		return ""
	}
	return name
}

// allowedTools: training-mcp's tools, at least one, and tools of the servers
// the host gave this session. Anything else (a shell, a file tool, a script
// tool, a server nobody configured) stops the engine.
func allowedTools(tools []string, servers map[string]string) bool {
	training := false
	for _, t := range tools {
		switch {
		case strings.HasPrefix(t, MCPToolPrefix) && len(t) > len(MCPToolPrefix):
			training = true
		case serverOf(t, servers) != "":
		default:
			return false
		}
	}
	return training
}

func (m *Manager) noteLoaded(p *person, pr *proc, tools []string) {
	loaded := map[string]bool{}
	for name := range pr.cs.servers {
		loaded[name] = false
	}
	for _, t := range tools {
		if name := serverOf(t, pr.cs.servers); name != "" {
			loaded[name] = true
		}
	}
	p.loaded = loaded
}

// Loaded is which connector servers the person's current or last session
// loaded (HCS-0008-R014). Nil before any session.
func (m *Manager) Loaded(userID int64) map[string]bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := m.people[userID]
	if p == nil || p.loaded == nil {
		return nil
	}
	out := make(map[string]bool, len(p.loaded))
	for k, v := range p.loaded {
		out[k] = v
	}
	return out
}

// Reload ends the person's session so the next turn starts one with their
// current connectors. A running turn finishes first.
func (m *Manager) Reload(userID int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := m.people[userID]
	if p == nil || p.proc == nil || p.proc.token == "" {
		return
	}
	if p.proc.turn != nil {
		p.proc.cs.reload = true
		return
	}
	m.stop(p, "connectors_changed", nil)
}

func (m *Manager) handleApproval(p *person, pr *proc, msg childMessage) {
	reply := func(allow bool) {
		d := "deny"
		if allow {
			d = "allow"
			pr.cs.tickets[msg.Name] = append(pr.cs.tickets[msg.Name], m.s.Now().Add(ticketTTL))
		}
		m.send(pr, parentMessage{Type: "approval", ID: msg.ID, Decision: d})
	}
	server := serverOf(msg.Name, pr.cs.servers)
	rec := decisionRecord{Entry: entryID(server), Permission: permissionID(msg.Name), Operation: msg.ID, Session: pr.cs.session, Channel: approvalChannel}
	if server == "" || pr.turn == nil {
		rec.Entry = "connector.unknown"
		m.record(rec, "deny", "policy", "HCS-0005-R001", "")
		reply(false)
		return
	}
	if a, ok := pr.cs.answers[msg.Name]; ok {
		rec.Channel = "grant-store"
		if a.answer == AnswerRejectAlways {
			m.record(rec, "deny", "denial", a.origin, "")
			reply(false)
		} else {
			m.record(rec, "allow", "grant", a.origin, "")
			reply(true)
		}
		return
	}
	if len(p.subs) == 0 {
		// Nobody can answer: a lost connection denies (HCS-0005-R014).
		m.record(rec, "error", "policy", "HCS-0005-R014", "")
		reply(false)
		return
	}
	a := &approval{hostID: randomID(), engineID: msg.ID, tool: msg.Name}
	timeout := m.s.ApprovalTimeout
	a.timer = time.AfterFunc(timeout, func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if pr.cs.approvals[a.hostID] == a {
			m.closeApproval(p, pr, a, "timeout")
		}
	})
	pr.cs.approvals[a.hostID] = a
	data := map[string]any{"id": a.hostID, "server": pr.cs.servers[server], "tool": strings.SplitN(strings.TrimPrefix(msg.Name, "mcp__"), "__", 2)[1]}
	if msg.Args != "" {
		data["args"] = msg.Args
	}
	m.broadcast(p, Event{Name: "approval", Data: data})
}

// closeApproval denies an open request that got no answer: timeout or error.
func (m *Manager) closeApproval(p *person, pr *proc, a *approval, outcome string) {
	delete(pr.cs.approvals, a.hostID)
	a.timer.Stop()
	rec := decisionRecord{Entry: entryID(serverOf(a.tool, pr.cs.servers)), Permission: permissionID(a.tool), Operation: a.engineID, Session: pr.cs.session, Channel: approvalChannel}
	m.record(rec, outcome, "policy", "HCS-0005-R014", "")
	m.send(pr, parentMessage{Type: "approval", ID: a.engineID, Decision: "deny"})
	m.broadcast(p, Event{Name: "approval_closed", Data: map[string]any{"id": a.hostID}})
}

// denyOpenApprovals ends every open request of a process: its stream or its
// session is gone.
func (m *Manager) denyOpenApprovals(p *person, pr *proc) {
	for _, a := range pr.cs.approvals {
		m.closeApproval(p, pr, a, "error")
	}
}

// Answer applies the person's answer to one of their open requests. The answer
// comes only from the person's own authenticated request (HCS-0005-R015).
func (m *Manager) Answer(userID int64, id, answer string) error {
	switch answer {
	case AnswerAllowOnce, AnswerAllowAlways, AnswerRejectOnce, AnswerRejectAlways:
	default:
		return ErrBadAnswer
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p := m.people[userID]
	if p == nil || p.proc == nil {
		return ErrNoApproval
	}
	pr := p.proc
	a, ok := pr.cs.approvals[id]
	if !ok {
		return ErrNoApproval
	}
	delete(pr.cs.approvals, id)
	a.timer.Stop()
	allow := answer == AnswerAllowOnce || answer == AnswerAllowAlways
	outcome := "deny"
	if allow {
		outcome = "allow"
	}
	rec := decisionRecord{Entry: entryID(serverOf(a.tool, pr.cs.servers)), Permission: permissionID(a.tool), Operation: a.engineID, Session: pr.cs.session, Channel: approvalChannel}
	recordID := m.record(rec, outcome, "subject", subjectID(userID), answer)
	if answer == AnswerAllowAlways || answer == AnswerRejectAlways {
		pr.cs.answers[a.tool] = sessionAnswer{answer: answer, origin: recordID}
	}
	d := "deny"
	if allow {
		d = "allow"
		pr.cs.tickets[a.tool] = append(pr.cs.tickets[a.tool], m.s.Now().Add(ticketTTL))
	}
	m.send(pr, parentMessage{Type: "approval", ID: a.engineID, Decision: d})
	return nil
}

// Authorize spends one allowed call of a tool for the session the engine token
// belongs to. The proxy asks it before forwarding each tools/call; a token of
// another person or of an ended session never matches (HCS-0005-R003, R008).
func (m *Manager) Authorize(userID int64, token, tool string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := m.people[userID]
	if p == nil || p.proc == nil || token == "" || p.proc.token != token || p.proc.stopped {
		return false
	}
	pr := p.proc
	if a, ok := pr.cs.answers[tool]; ok && a.answer == AnswerRejectAlways {
		return false
	}
	now := m.s.Now()
	tickets := pr.cs.tickets[tool]
	for len(tickets) > 0 && !now.Before(tickets[0]) {
		tickets = tickets[1:]
	}
	if len(tickets) == 0 {
		delete(pr.cs.tickets, tool)
		return false
	}
	pr.cs.tickets[tool] = tickets[1:]
	return true
}

// Grants lists the always answers of the person's current session.
func (m *Manager) Grants(userID int64) []Grant {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := m.people[userID]
	if p == nil || p.proc == nil {
		return nil
	}
	var out []Grant
	for tool, a := range p.proc.cs.answers {
		out = append(out, Grant{Tool: tool, Server: serverOf(tool, p.proc.cs.servers), Answer: a.answer})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Tool < out[j].Tool })
	return out
}

// Revoke removes the always answers of one connector from the person's
// session; every later call asks again (HCS-0005-R003).
func (m *Manager) Revoke(userID int64, server string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := m.people[userID]
	if p == nil || p.proc == nil {
		return
	}
	pr := p.proc
	for tool := range pr.cs.answers {
		if serverOf(tool, pr.cs.servers) == server {
			delete(pr.cs.answers, tool)
			rec := decisionRecord{Entry: entryID(server), Permission: permissionID(tool), Session: pr.cs.session, Channel: approvalChannel}
			m.record(rec, "revoked", "subject", subjectID(userID), "")
		}
	}
}

// --- decision records (HCS-0005 section 3.3) -----------------------------------

type decisionRecord struct {
	ID         string  `json:"id"`
	Time       string  `json:"time"`
	Entry      string  `json:"entry"`
	Permission string  `json:"permission"`
	Outcome    string  `json:"outcome"`
	Operation  string  `json:"operation,omitempty"`
	Session    string  `json:"session,omitempty"`
	Decider    decider `json:"decider"`
	Channel    string  `json:"channel"`
	Answer     string  `json:"answer,omitempty"`
}

type decider struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// record logs one decision. It never holds the call's arguments or result
// (HCS-0005-R011); the person is a pseudonymous id.
func (m *Manager) record(rec decisionRecord, outcome, kind, deciderID, answer string) string {
	rec.ID = randomID()
	rec.Time = m.s.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	rec.Outcome = outcome
	rec.Decider = decider{Kind: kind, ID: deciderID}
	rec.Answer = answer
	line, err := json.Marshal(struct {
		Event string `json:"event"`
		decisionRecord
	}{"coach.decision", rec})
	if err == nil {
		m.s.Logger.Print(string(line))
	}
	return rec.ID
}

var dottedID = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)

// entryID names a connector as an HCS entry.
func entryID(server string) string {
	if server == "" {
		return "connector.unknown"
	}
	return "connector." + server
}

var nonDotted = regexp.MustCompile(`[^a-z0-9]+`)

// permissionID names one tool of a connector as a dotted permission id. A name
// that lost its case gets a short hash so two tools never share an id.
func permissionID(tool string) string {
	short := tool
	if rest, ok := strings.CutPrefix(tool, "mcp__"); ok {
		if _, s, ok := strings.Cut(rest, "__"); ok {
			short = s
		}
	}
	clean := strings.Trim(nonDotted.ReplaceAllString(strings.ToLower(short), "-"), "-")
	if len(clean) > 100 {
		clean = strings.Trim(clean[:100], "-")
	}
	if clean == "" || strings.ToLower(short) != short {
		sum := sha256.Sum256([]byte(short))
		clean = strings.Trim(clean+"-"+hex.EncodeToString(sum[:4]), "-")
	}
	return "tool." + clean
}

// subjectID is the person in a decision record: a pseudonym, not their id.
func subjectID(userID int64) string {
	sum := sha256.Sum256(subject(userID))
	return "subject-" + hex.EncodeToString(sum[:8])
}
