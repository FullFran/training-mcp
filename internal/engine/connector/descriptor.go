package connector

import (
	"context"
	"net/url"
	"strings"

	"github.com/fullfran/training-mcp/internal/training"
)

// ServerSchema is the MCP Registry server.json revision connectors are
// described with (HCS-0008-R010).
const ServerSchema = "https://static.modelcontextprotocol.io/schemas/2025-12-11/server.schema.json"

// ServerJSON is a connector as an MCP Registry server.json document: one
// remote streamable-http transport. Its secret input, the Authorization header,
// never carries a value or a default (HCS-0008-R011): the host fills it from the
// person's vault inside the proxy, and it never reaches the engine (R012).
type ServerJSON struct {
	Schema      string   `json:"$schema"`
	Name        string   `json:"name"`
	Title       string   `json:"title,omitempty"`
	Description string   `json:"description"`
	Version     string   `json:"version"`
	Remotes     []Remote `json:"remotes"`
}

// Remote is a server.json remote transport.
type Remote struct {
	Type    string          `json:"type"`
	URL     string          `json:"url"`
	Headers []KeyValueInput `json:"headers,omitempty"`
}

// KeyValueInput is a server.json header input. It has no value field at all.
type KeyValueInput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	IsRequired  bool   `json:"isRequired"`
	IsSecret    bool   `json:"isSecret"`
}

// Describe builds the server.json document of a connector.
func Describe(c training.Connector) ServerJSON {
	d := ServerJSON{
		Schema:      ServerSchema,
		Name:        registryName(c),
		Title:       c.Label,
		Description: "Remote MCP server a person connected to their training-mcp coach.",
		Version:     "0.0.0",
		Remotes:     []Remote{{Type: "streamable-http", URL: c.URL}},
	}
	if c.Auth != training.ConnectorAuthNone {
		d.Remotes[0].Headers = []KeyValueInput{{
			Name:        "Authorization",
			Description: "Bearer token for this person, held by the host and added by its proxy.",
			IsRequired:  true,
			IsSecret:    true,
		}}
	}
	return d
}

// registryName is the reverse-DNS namespace of the server's host and the
// connector's name, for example com.notion.mcp/notion.
func registryName(c training.Connector) string {
	host := "invalid"
	if u, err := url.Parse(c.URL); err == nil && u.Hostname() != "" {
		host = u.Hostname()
	}
	labels := strings.Split(strings.ToLower(host), ".")
	for i, j := 0, len(labels)-1; i < j; i, j = i+1, j-1 {
		labels[i], labels[j] = labels[j], labels[i]
	}
	ns := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '-' {
			return r
		}
		return '-'
	}, strings.Join(labels, "."))
	return ns + "/" + c.Slug
}

// Descriptor is the server.json document of one of the person's connectors.
func (s *Service) Descriptor(ctx context.Context, userID, id int64) (ServerJSON, error) {
	c, err := s.Store.Connector(ctx, userID, id)
	if err != nil {
		return ServerJSON{}, err
	}
	return Describe(c), nil
}
