package evidence

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"testing/fstest"
)

func TestEmbeddedCorpusIsValid(t *testing.T) {
	c, err := Embedded()
	if err != nil {
		t.Fatalf("embedded corpus: %v", err)
	}
	if n := len(c.Sources); n < 20 || n > 40 {
		t.Fatalf("seed corpus has %d sources, want about 20 to 30", n)
	}
	if c.Version == "" || len(c.Digest) != 64 {
		t.Fatalf("version %q digest %q", c.Version, c.Digest)
	}
	topics := map[string]bool{}
	for _, s := range c.Sources {
		if s.ReviewStatus != ReviewUnreviewed {
			t.Errorf("%s: review_status %q; nothing in the seed corpus has been reviewed", s.ID, s.ReviewStatus)
		}
		if !strings.HasPrefix(s.URL, "https://") {
			t.Errorf("%s: url %q", s.ID, s.URL)
		}
		if s.DOI != "" && s.URL != "https://doi.org/"+s.DOI {
			t.Errorf("%s: url %q is not the DOI resolver URL", s.ID, s.URL)
		}
		for _, tp := range s.Topics {
			topics[tp] = true
		}
	}
	for _, want := range []string{"volume", "frequency", "fractional_volume", "proximity_to_failure", "rir_rpe", "rest_intervals", "autoregulation", "deload", "exercise_selection", "range_of_motion", "periodization"} {
		if !topics[want] {
			t.Errorf("no source covers topic %q", want)
		}
	}
	var pelland *Source
	for i, s := range c.Sources {
		if s.DOI == "10.1007/s40279-025-02344-w" {
			pelland = &c.Sources[i]
		}
	}
	if pelland == nil || pelland.Publication != "Sports Medicine" || pelland.Year != 2025 {
		t.Fatalf("Pelland et al. 2025 missing or wrong: %+v", pelland)
	}
}

// Every debate the corpus defines must show both sides, so it needs sources
// that do not all agree; the manifest names each debate's members.
func TestEmbeddedDebatesCoverTheContestedQuestions(t *testing.T) {
	c, err := Embedded()
	if err != nil {
		t.Fatal(err)
	}
	refs := map[string][]string{}
	for _, d := range c.DebateRefs() {
		refs[d.ID] = d.SourceIDs
	}
	for _, id := range []string{"volume_dose_response", "proximity_to_failure", "rest_interval_length", "deload_value"} {
		if len(refs[id]) < 3 {
			t.Errorf("debate %s has %v; the contested questions need at least three sources", id, refs[id])
		}
	}
}

func source(id string) Source {
	return Source{
		ID: id, Title: "A title", Authors: []string{"A Author"}, Year: 2020, Publication: "Journal",
		StudyType: "meta_analysis", DOI: "10.1234/" + id, URL: "https://doi.org/10.1234/" + id,
		Population: "Adults", Topics: []string{"volume"}, Debates: []string{"d1"},
		KeyFindings: []string{"A finding."}, Methods: "Methods.", Limitations: "Limits.",
		SummaryBasis: SummaryFromAbstract, Verification: Verification{Crossref: "2026-11-25", RetractionCheck: "None."},
		IngestedOn: "2026-11-25", ContentVersion: 1, ReviewStatus: ReviewUnreviewed,
	}
}

func corpusFS(t *testing.T, version string, sources ...Source) fstest.MapFS {
	t.Helper()
	m, _ := json.Marshal(manifest{Version: version, Debates: []Debate{{ID: "d1", Question: "Q?"}}})
	fsys := fstest.MapFS{"manifest.json": {Data: m}}
	for _, s := range sources {
		data, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		fsys["sources/"+s.ID+".json"] = &fstest.MapFile{Data: data}
	}
	return fsys
}

func TestParseAcceptsAValidCorpus(t *testing.T) {
	c, err := Parse(corpusFS(t, "v1", source("a-1"), source("b-2")))
	if err != nil {
		t.Fatal(err)
	}
	if c.Version != "v1" || len(c.Sources) != 2 {
		t.Fatalf("%+v", c)
	}
	other, _ := Parse(corpusFS(t, "v1", source("a-1"), source("b-3")))
	if other.Digest == c.Digest {
		t.Fatal("different sources must give a different digest")
	}
}

func TestParseRejectsBrokenCorpora(t *testing.T) {
	mutate := func(f func(*Source)) Source {
		s := source("b-2")
		f(&s)
		return s
	}
	cases := map[string]fstest.MapFS{
		"no version":           corpusFS(t, " ", source("a-1"), source("b-2")),
		"unknown topic":        corpusFS(t, "v1", source("a-1"), mutate(func(s *Source) { s.Topics = []string{"cardio"} })),
		"unknown study type":   corpusFS(t, "v1", source("a-1"), mutate(func(s *Source) { s.StudyType = "blog" })),
		"no doi nor pmid":      corpusFS(t, "v1", source("a-1"), mutate(func(s *Source) { s.DOI = "" })),
		"bad doi":              corpusFS(t, "v1", source("a-1"), mutate(func(s *Source) { s.DOI = "doi.org/x" })),
		"bad pmid":             corpusFS(t, "v1", source("a-1"), mutate(func(s *Source) { s.PMID = "12a" })),
		"http url":             corpusFS(t, "v1", source("a-1"), mutate(func(s *Source) { s.URL = "http://example.org/x" })),
		"empty url":            corpusFS(t, "v1", source("a-1"), mutate(func(s *Source) { s.URL = "" })),
		"no verification":      corpusFS(t, "v1", source("a-1"), mutate(func(s *Source) { s.Verification.Crossref = "" })),
		"no retraction check":  corpusFS(t, "v1", source("a-1"), mutate(func(s *Source) { s.Verification.RetractionCheck = "" })),
		"pubmed without pmid":  corpusFS(t, "v1", source("a-1"), mutate(func(s *Source) { s.Verification.PubMed = "2026-11-25" })),
		"bad review status":    corpusFS(t, "v1", source("a-1"), mutate(func(s *Source) { s.ReviewStatus = "approved" })),
		"reviewed, no name":    corpusFS(t, "v1", source("a-1"), mutate(func(s *Source) { s.ReviewStatus = ReviewReviewed })),
		"unreviewed, named":    corpusFS(t, "v1", source("a-1"), mutate(func(s *Source) { s.ReviewedBy = "Someone" })),
		"unknown debate":       corpusFS(t, "v1", source("a-1"), mutate(func(s *Source) { s.Debates = []string{"d1", "d9"} })),
		"one-sided debate":     corpusFS(t, "v1", source("a-1"), mutate(func(s *Source) { s.Debates = nil })),
		"empty finding":        corpusFS(t, "v1", source("a-1"), mutate(func(s *Source) { s.KeyFindings = []string{" "} })),
		"record_only too much": corpusFS(t, "v1", source("a-1"), mutate(func(s *Source) { s.SummaryBasis = SummaryRecordOnly; s.KeyFindings = []string{"a", "b"} })),
		"no content version":   corpusFS(t, "v1", source("a-1"), mutate(func(s *Source) { s.ContentVersion = 0 })),
		"bad ingested date":    corpusFS(t, "v1", source("a-1"), mutate(func(s *Source) { s.IngestedOn = "2026-13-01" })),
		"duplicate doi":        corpusFS(t, "v1", source("a-1"), mutate(func(s *Source) { s.DOI = "10.1234/A-1" })),
		"bad id":               corpusFS(t, "v1", source("a-1"), source("b-2"), mutate(func(s *Source) { s.ID = "Bad Id" })),
	}
	for name, fsys := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse(fsys); !errors.Is(err, ErrInvalid) {
				t.Fatalf("err = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestParseRejectsUnknownFieldsAndMisnamedFiles(t *testing.T) {
	fsys := corpusFS(t, "v1", source("a-1"), source("b-2"))
	fsys["sources/c-3.json"] = &fstest.MapFile{Data: []byte(`{"id":"c-3","abstract":"copied text"}`)}
	if _, err := Parse(fsys); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "abstract") {
		t.Fatalf("unknown field: %v", err)
	}
	fsys = corpusFS(t, "v1", source("a-1"), source("b-2"))
	fsys["sources/other.json"] = fsys["sources/b-2.json"]
	delete(fsys, "sources/b-2.json")
	if _, err := Parse(fsys); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "must be named") {
		t.Fatalf("misnamed file: %v", err)
	}
}

func TestCitation(t *testing.T) {
	s := source("a-1")
	s.Authors = []string{"Ann One", "Bo Two", "Cy Three", "Di Four"}
	s.Volume, s.Issue, s.Pages, s.PMID = "56", "2", "481-505", "41343037"
	want := "Ann One et al. (2020). A title. Journal 56(2):481-505. doi:10.1234/a-1 PMID:41343037"
	if got := s.Citation(); got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}
