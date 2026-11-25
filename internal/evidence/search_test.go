package evidence

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestTermsKeepOnlyLettersAndDigits(t *testing.T) {
	cases := map[string][]string{
		"":    nil,
		"   ": nil,
		`"rest" AND (NEAR(x y)) OR -volume* ^col:`: {"rest", "near", "volume", "col"},
		"descanso entre series ¿cuánto?":           {"descanso", "entre", "series"},
		"Hipertrofia, HIPERTROFIA hipertrofia":     {"hipertrofia"},
		"the effect of training on the study":      nil,
		"1RM; DROP TABLE evidence_sources; --":     {"1rm", "drop", "table", "sources"},
		"rest_intervals 3' 90\"":                   {"rest", "intervals", "90"},
		"a b c ñ é":                                nil,
		"\x00\x01 volume ‮":                        {"volume"},
	}
	for in, want := range cases {
		if got := Terms(in); !reflect.DeepEqual(got, want) {
			t.Errorf("Terms(%q) = %q, want %q", in, got, want)
		}
	}
	long := strings.Repeat("word", 1) + strings.Repeat(" w1 w2 w3 w4 w5 w6 w7 w8 w9 x1 x2 x3 x4 x5 x6 x7 x8 x9", 2)
	if n := len(Terms(long)); n != maxTerms {
		t.Fatalf("got %d terms, want the cap %d", n, maxTerms)
	}
}

// fakeIndex answers from a fixed list: a source matches when its title holds
// every term. Each match is returned twice, to prove the library deduplicates.
type fakeIndex struct {
	sources []Source
	queries []IndexQuery
}

func (f *fakeIndex) Search(_ context.Context, q IndexQuery) ([]Hit, error) {
	f.queries = append(f.queries, q)
	var out []Hit
	for _, s := range f.sources {
		title := strings.ToLower(s.Title)
		n := 0
		for _, term := range q.Terms {
			if strings.Contains(title, term) {
				n++
			}
		}
		if n == len(q.Terms) {
			out = append(out, Hit{Source: s, Snippet: s.Title}, Hit{Source: s, Snippet: "duplicate"})
		}
	}
	return out, nil
}

func (f *fakeIndex) Source(_ context.Context, id string) (Source, error) {
	for _, s := range f.sources {
		if s.ID == id {
			return s, nil
		}
	}
	return Source{}, ErrUnknownSource
}

func (f *fakeIndex) Loaded(context.Context) (LoadedCorpus, error) {
	return LoadedCorpus{Version: "v1", Debates: []DebateRef{{ID: "d1", Question: "Q?", SourceIDs: []string{"a-1", "b-2"}}}}, nil
}

func newFake() *fakeIndex {
	a, b := source("a-1"), source("b-2")
	a.Title, b.Title = "Rest intervals and hypertrophy", "Weekly volume and hypertrophy"
	return &fakeIndex{sources: []Source{a, b}}
}

func TestSearchEmptyQueryIsAnExplicitNoResult(t *testing.T) {
	idx := newFake()
	for _, q := range []string{"", "  ", "the of and", "¿?!"} {
		r, err := NewLibrary(idx).Search(context.Background(), SearchInput{Query: q})
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Results) != 0 || r.NoResults == "" || r.Match != MatchNone || r.Notice != Notice {
			t.Fatalf("%q: %+v", q, r)
		}
	}
	if len(idx.queries) != 0 {
		t.Fatal("a query without words must not reach the index")
	}
}

func TestSearchFallsBackToMostTermsAndDeduplicates(t *testing.T) {
	idx := newFake()
	r, err := NewLibrary(idx).Search(context.Background(), SearchInput{Query: "rest volume hypertrophy"})
	if err != nil {
		t.Fatal(err)
	}
	// One search for all three words, then one per word.
	if r.Match != MatchMostTerms || len(r.Results) != 2 || len(idx.queries) != 4 {
		t.Fatalf("%+v %+v", r, idx.queries)
	}
	if r.Results[0].SourceID == r.Results[1].SourceID {
		t.Fatal("a source appears twice")
	}
	if r.Results[0].ReviewStatus != ReviewUnreviewed || r.Results[0].URL == "" || r.Results[0].Citation == "" {
		t.Fatalf("result lacks citation data: %+v", r.Results[0])
	}
	if len(r.Debates) != 1 || !slices.Equal(r.Debates[0].SourceIDs, []string{"a-1", "b-2"}) {
		t.Fatalf("debates: %+v", r.Debates)
	}
	// Two words must both match: no fallback below three words.
	for _, q := range []string{"nutrition creatine", "rest volume", "creatine loading hypertrophy"} {
		r, _ = NewLibrary(idx).Search(context.Background(), SearchInput{Query: q})
		if len(r.Results) != 0 || r.NoResults == "" || r.Match != MatchNone {
			t.Fatalf("%q: %+v", q, r)
		}
	}
}

func TestSearchRejectsBadFiltersAndLimits(t *testing.T) {
	lib := NewLibrary(newFake())
	for name, in := range map[string]SearchInput{
		"topic":      {Query: "rest", Filters: Filters{Topic: "cardio"}},
		"study type": {Query: "rest", Filters: Filters{StudyType: "blog"}},
		"years":      {Query: "rest", Filters: Filters{YearFrom: 2020, YearTo: 2010}},
		"old year":   {Query: "rest", Filters: Filters{YearFrom: 1066}},
		"limit":      {Query: "rest", Limit: MaxLimit + 1},
		"negative":   {Query: "rest", Limit: -1},
		"long":       {Query: strings.Repeat("rest ", 200)},
	} {
		if _, err := lib.Search(context.Background(), in); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
}

func TestGetUnknownSource(t *testing.T) {
	lib := NewLibrary(newFake())
	for _, id := range []string{"missing-1", "", "../etc/passwd", "a-1' OR 1=1"} {
		if _, err := lib.Get(context.Background(), id); !errors.Is(err, ErrUnknownSource) {
			t.Errorf("%q: err = %v, want ErrUnknownSource", id, err)
		}
	}
	e, err := lib.Get(context.Background(), " a-1 ")
	if err != nil || e.Source.ID != "a-1" || e.Notice != Notice || len(e.Debates) != 1 || e.Citation == "" {
		t.Fatalf("%+v %v", e, err)
	}
}
