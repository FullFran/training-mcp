package evidence

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
	"sync"
)

//go:embed corpus/manifest.json corpus/sources/*.json
var embedded embed.FS

// Corpus is one version of the library: the manifest and every source.
type Corpus struct {
	// Version names this corpus; bump it with every change to a source.
	Version string
	// Digest is the SHA-256 of every corpus file, so an edit that forgot to
	// bump Version still reaches the index.
	Digest  string
	Debates []Debate
	Sources []Source
}

type manifest struct {
	Version     string   `json:"version"`
	Description string   `json:"description"`
	Debates     []Debate `json:"debates"`
}

var embeddedCorpus = sync.OnceValues(func() (Corpus, error) {
	sub, err := fs.Sub(embedded, "corpus")
	if err != nil {
		return Corpus{}, err
	}
	return Parse(sub)
})

// Embedded is the corpus compiled into this binary, parsed and validated once.
func Embedded() (Corpus, error) { return embeddedCorpus() }

func decodeStrict(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// Parse reads a corpus directory: manifest.json and sources/<id>.json, one
// file per source. It returns the corpus only when every rule holds.
func Parse(fsys fs.FS) (Corpus, error) {
	hash := sha256.New()
	raw, err := fs.ReadFile(fsys, "manifest.json")
	if err != nil {
		return Corpus{}, err
	}
	hash.Write(raw)
	var m manifest
	if err = decodeStrict(raw, &m); err != nil {
		return Corpus{}, fmt.Errorf("%w: manifest.json: %v", ErrInvalid, err)
	}
	names, err := fs.Glob(fsys, "sources/*.json")
	if err != nil {
		return Corpus{}, err
	}
	sort.Strings(names)
	c := Corpus{Version: strings.TrimSpace(m.Version), Debates: m.Debates}
	for _, name := range names {
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			return Corpus{}, err
		}
		hash.Write([]byte("\x00" + name + "\x00"))
		hash.Write(data)
		var s Source
		if err = decodeStrict(data, &s); err != nil {
			return Corpus{}, fmt.Errorf("%w: %s: %v", ErrInvalid, name, err)
		}
		if want := s.ID + ".json"; path.Base(name) != want {
			return Corpus{}, fmt.Errorf("%w: %s: the file of source %q must be named %s", ErrInvalid, name, s.ID, want)
		}
		c.Sources = append(c.Sources, s)
	}
	c.Digest = hex.EncodeToString(hash.Sum(nil))
	return c, c.Validate()
}

// Validate checks the whole corpus: a version, well-formed debates each with
// at least two sources, valid sources, and no id, DOI or PMID used twice.
func (c Corpus) Validate() error {
	if c.Version == "" {
		return fmt.Errorf("%w: the manifest has no version", ErrInvalid)
	}
	if len(c.Sources) == 0 {
		return fmt.Errorf("%w: the corpus has no sources", ErrInvalid)
	}
	debates := map[string]bool{}
	for _, d := range c.Debates {
		if !debatePattern.MatchString(d.ID) || strings.TrimSpace(d.Question) == "" {
			return fmt.Errorf("%w: debate %q needs a lower-case id and a question", ErrInvalid, d.ID)
		}
		if debates[d.ID] {
			return fmt.Errorf("%w: debate %q is defined twice", ErrInvalid, d.ID)
		}
		debates[d.ID] = true
	}
	ids, dois, pmids := map[string]bool{}, map[string]string{}, map[string]string{}
	members := map[string]int{}
	for _, s := range c.Sources {
		if err := s.Validate(debates); err != nil {
			return err
		}
		if ids[s.ID] {
			return fmt.Errorf("%w: source id %q is used twice", ErrInvalid, s.ID)
		}
		ids[s.ID] = true
		if s.DOI != "" {
			doi := strings.ToLower(s.DOI)
			if other, dup := dois[doi]; dup {
				return fmt.Errorf("%w: sources %q and %q share the DOI %s", ErrInvalid, other, s.ID, s.DOI)
			}
			dois[doi] = s.ID
		}
		if s.PMID != "" {
			if other, dup := pmids[s.PMID]; dup {
				return fmt.Errorf("%w: sources %q and %q share the PMID %s", ErrInvalid, other, s.ID, s.PMID)
			}
			pmids[s.PMID] = s.ID
		}
		for _, d := range s.Debates {
			members[d]++
		}
	}
	for _, d := range c.Debates {
		if members[d.ID] < 2 {
			return fmt.Errorf("%w: debate %q has %d sources; a debate shows at least two", ErrInvalid, d.ID, members[d.ID])
		}
	}
	return nil
}

// TopicTerms is the index text for a list of topic ids: their ids, labels and
// search terms.
func TopicTerms(ids []string) string {
	var parts []string
	for _, id := range ids {
		for _, t := range Topics {
			if t.ID == id {
				parts = append(parts, strings.ReplaceAll(t.ID, "_", " "), t.Label, t.Terms)
			}
		}
	}
	return strings.Join(parts, " ")
}
