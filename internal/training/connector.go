package training

import (
	"errors"
	"time"
)

// A Connector is a remote MCP server one person connected to their coach. The
// record holds no secret: tokens and client credentials live in the coach's
// encrypted vault, keyed by the person and the connector.
type Connector struct {
	ID int64
	// Slug names the server for the model: its tools are mcp__<slug>__<tool>.
	// Unique per person; lowercase letters and digits.
	Slug  string
	Label string
	URL   string
	// Auth is how the server is reached: "oauth", "bearer" or "none".
	Auth string
	// State is "disconnected" or "connected".
	State     string
	CreatedAt time.Time
}

const (
	ConnectorAuthOAuth  = "oauth"
	ConnectorAuthBearer = "bearer"
	ConnectorAuthNone   = "none"

	ConnectorDisconnected = "disconnected"
	ConnectorConnected    = "connected"

	// MaxConnectors is how many remote servers one person may add.
	MaxConnectors = 10
)

var (
	// ErrConnectorExists: the person already has a connector with that slug.
	ErrConnectorExists = errors.New("a connector with that name already exists")
	// ErrTooManyConnectors: the person reached MaxConnectors.
	ErrTooManyConnectors = errors.New("too many connectors")
)
