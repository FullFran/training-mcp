// Package engine runs the in-app coach: one engine process per person, on the
// Pi SDK, that talks to the person's own ChatGPT account and whose only tools
// are this server's MCP tools, used as that person.
//
// The host (this package) owns everything that is a secret or a limit: the
// encrypted credential vault, the single refresher per person, the short-lived
// engine bearer for /mcp, and the process slots. The engine program in engine/
// owns the model conversation and nothing else.
//
// Host and engine speak line-delimited JSON over the engine's stdin and stdout
// (the protocol of RE;write's embedded engine, re-implemented here). Everything
// the engine sends is untrusted: it is parsed against the shapes below, unknown
// fields are dropped, and free text from a model provider has no field to travel
// in except answer deltas.
package engine

import (
	"encoding/json"
	"regexp"
	"strings"
	"unicode/utf8"
)

// ErrorCode is the only error text that may reach a browser or a log.
type ErrorCode string

const (
	CodeModelBusy         ErrorCode = "model_busy"
	CodeReconnect         ErrorCode = "reconnect"
	CodeProviderError     ErrorCode = "provider_error"
	CodeBusy              ErrorCode = "busy"
	CodeTurnInProgress    ErrorCode = "turn_in_progress"
	CodeNotConnected      ErrorCode = "not_connected"
	CodeEngineUnavailable ErrorCode = "engine_unavailable"
	CodeLoginFailed       ErrorCode = "login_failed"
	CodeRateLimited       ErrorCode = "rate_limited"
	CodeTurnLimit         ErrorCode = "turn_limit"
)

var knownCodes = map[ErrorCode]bool{
	CodeModelBusy: true, CodeReconnect: true, CodeProviderError: true, CodeBusy: true,
	CodeTurnInProgress: true, CodeNotConnected: true, CodeEngineUnavailable: true,
	CodeLoginFailed: true, CodeRateLimited: true, CodeTurnLimit: true,
}

// Error is a refusal or failure with a fixed code; its text is the code.
type Error struct{ Code ErrorCode }

func (e *Error) Error() string { return string(e.Code) }

func fail(code ErrorCode) error { return &Error{Code: code} }

// toCode maps anything the engine sends to a known code.
func toCode(v string) ErrorCode {
	if c := ErrorCode(v); knownCodes[c] {
		return c
	}
	return CodeProviderError
}

// DeviceVerificationURI is where a person types the device code. It is fixed
// here and never taken from the engine.
const DeviceVerificationURI = "https://auth.openai.com/codex/device"

// MCPServerName is the name the engine registers this server under; its tools
// are then mcp__training__<tool>.
const MCPServerName = "training"

// MCPToolPrefix is what every tool name of the engine must start with.
const MCPToolPrefix = "mcp__" + MCPServerName + "__"

const (
	maxLineBytes  = 1 << 20
	maxDeltaChars = 65536
	maxSecretLen  = 16384
)

// Credential is a person's ChatGPT login. The refresh token is single-use and
// rotates, so only one refresh per person may ever be in flight.
type Credential struct {
	Access    string `json:"access"`
	Refresh   string `json:"refresh"`
	Expires   int64  `json:"expires"` // Unix milliseconds
	AccountID string `json:"accountId"`
}

func validSecret(s string) bool { return s != "" && len(s) <= maxSecretLen }

func (c *Credential) valid() bool {
	return c != nil && validSecret(c.Access) && validSecret(c.Refresh) && validSecret(c.AccountID) && c.Expires > 0
}

// parentMessage is what the host writes to the engine. Fields are optional by
// message type.
type parentMessage struct {
	Type         string      `json:"type"`
	ID           string      `json:"id,omitempty"`
	MCPURL       string      `json:"mcpUrl,omitempty"`
	MCPToken     string      `json:"mcpToken,omitempty"`
	SystemPrompt string      `json:"systemPrompt,omitempty"`
	Credential   *Credential `json:"credential,omitempty"`
	Access       string      `json:"access,omitempty"`
	AccountID    string      `json:"accountId,omitempty"`
	Expires      int64       `json:"expires,omitempty"`
	Error        ErrorCode   `json:"error,omitempty"`
	Text         string      `json:"text,omitempty"`
	Context      string      `json:"context,omitempty"`
	// Images are the photos of a prompt, base64 in JSON. They make a prompt
	// line far longer than anything the engine sends back.
	Images []promptImage `json:"images,omitempty"`
}

// childMessage is a parsed, validated message from the engine.
type childMessage struct {
	Type       string
	ID         string
	PiVersion  string
	UserCode   string
	Credential *Credential
	Tools      []string
	Text       string
	Name       string
	Phase      string
	OK         bool
	Call       int
	// Pending marks a write that became a proposal waiting for the person.
	Pending bool
	Aborted bool
	Code    ErrorCode
}

var (
	reID       = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)
	reUserCode = regexp.MustCompile(`^[A-Z0-9][A-Z0-9-]{2,18}[A-Z0-9]$`)
	reVersion  = regexp.MustCompile(`^[0-9A-Za-z.+-]{1,32}$`)
	reToolName = regexp.MustCompile(`^[A-Za-z0-9_]{1,128}$`)
)

const maxCall = 100000

// parseChildMessage accepts only known messages with well-formed fields; it
// returns false for anything else.
func parseChildMessage(line []byte) (childMessage, bool) {
	var raw struct {
		Type       string          `json:"type"`
		ID         string          `json:"id"`
		PiVersion  string          `json:"piVersion"`
		UserCode   string          `json:"userCode"`
		Credential *Credential     `json:"credential"`
		Tools      json.RawMessage `json:"tools"`
		Text       *string         `json:"text"`
		Name       string          `json:"name"`
		Phase      string          `json:"phase"`
		OK         bool            `json:"ok"`
		Call       int             `json:"call"`
		Pending    bool            `json:"pending"`
		Aborted    bool            `json:"aborted"`
		Code       string          `json:"code"`
	}
	if err := json.Unmarshal(line, &raw); err != nil {
		return childMessage{}, false
	}
	m := childMessage{Type: raw.Type}
	idOK := reID.MatchString(raw.ID)
	switch raw.Type {
	case "ready":
		m.PiVersion = "unknown"
		if reVersion.MatchString(raw.PiVersion) {
			m.PiVersion = raw.PiVersion
		}
	case "login.code":
		if !reUserCode.MatchString(raw.UserCode) {
			return m, false
		}
		m.UserCode = raw.UserCode
	case "login.done":
		if !raw.Credential.valid() {
			return m, false
		}
		m.Credential = raw.Credential
	case "login.failed":
	case "refresh.done":
		if !idOK || !raw.Credential.valid() {
			return m, false
		}
		m.ID, m.Credential = raw.ID, raw.Credential
	case "refresh.failed", "token.get":
		if !idOK {
			return m, false
		}
		m.ID = raw.ID
	case "session":
		var tools []string
		if err := json.Unmarshal(raw.Tools, &tools); err != nil || len(tools) > 256 {
			return m, false
		}
		for _, t := range tools {
			if !reToolName.MatchString(t) {
				return m, false
			}
		}
		m.Tools = tools
	case "delta":
		if raw.Text == nil || utf8.RuneCountInString(*raw.Text) > maxDeltaChars || !utf8.ValidString(*raw.Text) {
			return m, false
		}
		m.Text = *raw.Text
	case "tool":
		if !reToolName.MatchString(raw.Name) {
			return m, false
		}
		m.Name = raw.Name
		m.Phase = "end"
		if raw.Phase == "start" {
			m.Phase = "start"
		}
		m.OK = m.Phase == "start" || raw.OK
		m.Pending = m.Phase == "end" && m.OK && raw.Pending
		if raw.Call > 0 && raw.Call <= maxCall {
			m.Call = raw.Call
		}
	case "done":
		m.Aborted = raw.Aborted
	case "error":
		m.Code = toCode(raw.Code)
	default:
		return m, false
	}
	return m, true
}

// onlyTrainingTools reports whether a tool list is non-empty and holds this
// server's MCP tools and nothing else. Anything more (a shell, a file tool, a
// script tool) stops the engine.
func onlyTrainingTools(tools []string) bool {
	if len(tools) == 0 {
		return false
	}
	for _, t := range tools {
		if !strings.HasPrefix(t, MCPToolPrefix) || len(t) == len(MCPToolPrefix) {
			return false
		}
	}
	return true
}
