package connector

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"
)

// The connector catalog is a host-owned, versioned document of popular remote
// MCP servers a person can add with one tap (docs/coach.md). catalog.json lists
// the entries with their display metadata; each entry's server is an MCP
// Registry server.json (HCS-0008-R010) whose secret input never carries a value
// (R011). Adding or removing an entry is a reviewed change to these files.
//
//go:embed catalog
var catalogFS embed.FS

// CatalogEntry is one server of the catalog, ready to show and to add.
type CatalogEntry struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Icon     string `json:"icon"`
	Category string `json:"category"`
	// CategoryLabel is the category's name as the panel shows it (Spanish).
	CategoryLabel string `json:"category_label"`
	Summary       string `json:"summary"`
	URL           string `json:"url"`
}

// CatalogView is a catalog entry with the person's connector for it, if any.
type CatalogView struct {
	CatalogEntry
	Connector *View `json:"connector,omitempty"`
}

var (
	catalogIDPattern    = regexp.MustCompile(`^[a-z][a-z0-9]{0,23}$`)
	registryNamePattern = regexp.MustCompile(`^[a-zA-Z0-9.-]+/[a-zA-Z0-9._-]+$`)
)

const (
	maxCatalogBytes   = 256 << 10
	maxCatalogEntries = 64
	maxSummaryRunes   = 160
	maxIconBytes      = 16
	maxServerDescr    = 100 // the MCP Registry's limit
)

type catalogDoc struct {
	ID         string               `json:"id"`
	Version    string               `json:"version"`
	Categories []catalogDocCategory `json:"categories"`
	Entries    []catalogDocEntry    `json:"entries"`
}

// catalogDocCategory is a group of cards; the panel shows the groups in the
// order they are declared.
type catalogDocCategory struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type catalogDocEntry struct {
	ID       string `json:"id"`
	Server   string `json:"server"`
	Icon     string `json:"icon"`
	Category string `json:"category"`
	Summary  string `json:"summary"`
}

// strictJSON decodes one JSON document with no unknown member: a server input
// with a value or a default is refused, as is any field the host does not know.
func strictJSON(fsys fs.FS, name string, v any) error {
	b, err := fs.ReadFile(fsys, name)
	if err != nil {
		return err
	}
	if len(b) > maxCatalogBytes {
		return fmt.Errorf("%s: too large", name)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	if dec.More() {
		return fmt.Errorf("%s: trailing data", name)
	}
	return nil
}

// LoadCatalog reads and checks a catalog: catalog.json at the root of fsys and
// the server.json files it names. Any error refuses the whole document.
func LoadCatalog(fsys fs.FS) ([]CatalogEntry, error) {
	var doc catalogDoc
	if err := strictJSON(fsys, "catalog.json", &doc); err != nil {
		return nil, err
	}
	if doc.ID == "" || doc.Version == "" {
		return nil, errors.New("catalog: id and version are required")
	}
	if len(doc.Entries) == 0 || len(doc.Entries) > maxCatalogEntries {
		return nil, errors.New("catalog: wrong number of entries")
	}
	if len(doc.Categories) == 0 {
		return nil, errors.New("catalog: categories are required")
	}
	labels := map[string]string{}
	order := map[string]int{}
	for i, c := range doc.Categories {
		if !catalogIDPattern.MatchString(c.ID) || labels[c.ID] != "" {
			return nil, fmt.Errorf("catalog category %q: bad or duplicate id", c.ID)
		}
		if strings.TrimSpace(c.Label) == "" || utf8.RuneCountInString(c.Label) > maxLabelRunes {
			return nil, fmt.Errorf("catalog category %q: bad label", c.ID)
		}
		labels[c.ID], order[c.ID] = c.Label, i
	}
	strict := &Guard{}
	seen := map[string]bool{}
	out := make([]CatalogEntry, 0, len(doc.Entries))
	for _, e := range doc.Entries {
		bad := func(why string) error { return fmt.Errorf("catalog entry %q: %s", e.ID, why) }
		switch {
		case !catalogIDPattern.MatchString(e.ID):
			return nil, bad("bad id")
		case seen[e.ID]:
			return nil, bad("duplicate id")
		case e.Icon == "" || len(e.Icon) > maxIconBytes || !utf8.ValidString(e.Icon):
			return nil, bad("bad icon")
		case labels[e.Category] == "":
			return nil, bad("unknown category")
		case strings.TrimSpace(e.Summary) == "" || utf8.RuneCountInString(e.Summary) > maxSummaryRunes:
			return nil, bad("bad summary")
		case !strings.HasPrefix(e.Server, "servers/") || !strings.HasSuffix(e.Server, ".server.json") || !fs.ValidPath(e.Server) || path.Clean(e.Server) != e.Server:
			return nil, bad("bad server path")
		}
		seen[e.ID] = true
		var s ServerJSON
		if err := strictJSON(fsys, e.Server, &s); err != nil {
			return nil, bad(err.Error())
		}
		switch {
		case s.Schema != ServerSchema:
			return nil, bad("server.json schema is not " + ServerSchema)
		case !registryNamePattern.MatchString(s.Name):
			return nil, bad("bad server name")
		case s.Description == "" || utf8.RuneCountInString(s.Description) > maxServerDescr:
			return nil, bad("bad server description")
		case s.Version == "":
			return nil, bad("server version is required")
		case s.Title == "" || utf8.RuneCountInString(s.Title) > maxLabelRunes:
			return nil, bad("bad title")
		case len(s.Remotes) != 1 || s.Remotes[0].Type != "streamable-http":
			return nil, bad("needs exactly one streamable-http remote")
		}
		r := s.Remotes[0]
		u, err := strict.CheckURL(r.URL)
		if err != nil || u.String() != r.URL || u.RawQuery != "" {
			return nil, bad("url refused")
		}
		// The connector made from the entry is named by its title, and that
		// name is the entry's id: the coach's tools are mcp__<id>__*.
		if slugFor(s.Title, u) != e.ID {
			return nil, bad("title does not name the id")
		}
		if len(r.Headers) != 1 || r.Headers[0].Name != "Authorization" || !r.Headers[0].IsSecret || !r.Headers[0].IsRequired {
			return nil, bad("the one header must be the required, secret Authorization")
		}
		out = append(out, CatalogEntry{ID: e.ID, Title: s.Title, Icon: e.Icon, Category: e.Category, CategoryLabel: labels[e.Category], Summary: e.Summary, URL: r.URL})
	}
	sort.SliceStable(out, func(i, j int) bool { return order[out[i].Category] < order[out[j].Category] })
	return out, nil
}

var builtinCatalog = sync.OnceValues(func() ([]CatalogEntry, error) {
	sub, err := fs.Sub(catalogFS, "catalog")
	if err != nil {
		return nil, err
	}
	return LoadCatalog(sub)
})

// BuiltinCatalog is the catalog shipped with the binary.
func BuiltinCatalog() ([]CatalogEntry, error) { return builtinCatalog() }

func (s *Service) catalog() ([]CatalogEntry, error) {
	if s.CatalogEntries != nil {
		return s.CatalogEntries, nil
	}
	return BuiltinCatalog()
}

// Catalog is the catalog with the person's own connector for each entry: the
// one whose URL is the entry's.
func (s *Service) Catalog(ctx context.Context, userID int64) ([]CatalogView, error) {
	entries, err := s.catalog()
	if err != nil {
		return nil, fail("catalog")
	}
	mine, err := s.List(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]CatalogView, 0, len(entries))
	for _, e := range entries {
		v := CatalogView{CatalogEntry: e}
		for i := range mine {
			if mine[i].URL == e.URL {
				v.Connector = &mine[i]
				break
			}
		}
		out = append(out, v)
	}
	return out, nil
}

// AddFromCatalog adds a catalog entry for the person exactly as the manual
// path would with the entry's URL and title, so the guard, the limits and the
// OAuth flow that follows are the same.
func (s *Service) AddFromCatalog(ctx context.Context, userID int64, entryID string) (View, error) {
	entries, err := s.catalog()
	if err != nil {
		return View{}, fail("catalog")
	}
	for _, e := range entries {
		if e.ID == entryID {
			return s.Add(ctx, userID, e.URL, e.Title, "")
		}
	}
	return View{}, fail("not_found")
}
