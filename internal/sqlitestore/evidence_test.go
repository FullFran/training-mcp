package sqlitestore

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/fullfran/training-mcp/internal/evidence"
)

func openEvidence(t testing.TB) (*Store, *evidence.Library) {
	t.Helper()
	store, err := Open(freshDB(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store, evidence.NewLibrary(store.Evidence())
}

func search(t *testing.T, lib *evidence.Library, in evidence.SearchInput) evidence.Result {
	t.Helper()
	r, err := lib.Search(context.Background(), in)
	if err != nil {
		t.Fatalf("search %+v: %v", in, err)
	}
	return r
}

func ids(r evidence.Result) []string {
	out := make([]string, len(r.Results))
	for i, item := range r.Results {
		out[i] = item.SourceID
	}
	return out
}

func TestOpenLoadsTheEmbeddedCorpus(t *testing.T) {
	store, _ := openEvidence(t)
	c, err := evidence.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Evidence().Loaded(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Version != c.Version || loaded.Digest != c.Digest || loaded.SourceCount != len(c.Sources) {
		t.Fatalf("loaded %+v, embedded %s %s %d", loaded, c.Version, c.Digest, len(c.Sources))
	}
	var n int
	if err = store.db.QueryRow(`SELECT COUNT(*) FROM evidence_fts`).Scan(&n); err != nil || n != len(c.Sources) {
		t.Fatalf("fts rows %d, err %v", n, err)
	}
}

// Each fixed query names the sources it must find in its top results; the
// test pins the precision of the index on the questions it is built for.
func TestSearchPrecision(t *testing.T) {
	_, lib := openEvidence(t)
	cases := []struct {
		query string
		top   int
		want  []string
	}{
		{"weekly volume dose response hypertrophy", 5, []string{"schoenfeld-2016-weekly-volume-meta-analysis", "pelland-2025-volume-frequency-dose-response"}},
		{"fractional volume indirect sets", 3, []string{"pelland-2025-volume-frequency-dose-response"}},
		{"training frequency per week", 4, []string{"schoenfeld-2018-frequency-hypertrophy-meta-analysis", "grgic-2018-frequency-strength-meta-analysis"}},
		{"proximity to failure", 4, []string{"refalo-2022-proximity-to-failure-hypertrophy", "robinson-2024-proximity-to-failure-dose-response"}},
		{"repetitions in reserve accuracy", 3, []string{"halperin-2021-predicting-repetitions-to-failure"}},
		{"RPE scale", 4, []string{"zourdos-2016-rir-rpe-scale", "helms-2016-rir-based-rpe-application"}},
		{"rest interval hypertrophy", 4, []string{"singer-2024-rest-intervals-bayesian-meta-analysis", "grgic-2017-rest-intervals-systematic-review"}},
		{"deload", 3, []string{"coleman-2024-one-week-deload-trial", "bell-2023-deloading-delphi-consensus", "rogerson-2024-deloading-practices-survey"}},
		{"velocity based autoregulation", 3, []string{"hickmott-2022-load-volume-autoregulation"}},
		{"partial range of motion", 4, []string{"pallares-2021-range-of-motion-meta-analysis", "wolf-2023-partial-vs-full-range-of-motion"}},
		{"long muscle length hamstrings", 2, []string{"maeo-2020-hamstrings-long-muscle-length"}},
		{"exercise variation", 3, []string{"kassiano-2022-exercise-variation-review"}},
		{"periodization", 4, []string{"moesgaard-2022-periodization-volume-equated", "williams-2017-periodization-maximal-strength", "grgic-2018-periodization-hypertrophy-review"}},
		{"low load vs high load", 3, []string{"schoenfeld-2017-low-vs-high-load"}},
		{"Pelland 2025", 1, []string{"pelland-2025-volume-frequency-dose-response"}},
	}
	for _, tc := range cases {
		got := ids(search(t, lib, evidence.SearchInput{Query: tc.query, Limit: tc.top}))
		for _, want := range tc.want {
			if !slices.Contains(got, want) {
				t.Errorf("%q: top %d = %v, missing %s", tc.query, tc.top, got, want)
			}
		}
	}
}

func TestSearchInSpanish(t *testing.T) {
	_, lib := openEvidence(t)
	// Every one of the top three carries the topic, and the named source is in
	// the top five.
	cases := []struct {
		query, topic, want string
	}{
		{"descanso entre series", "rest_intervals", "singer-2024-rest-intervals-bayesian-meta-analysis"},
		{"semana de descarga", "deload", "coleman-2024-one-week-deload-trial"},
		{"cerca del fallo muscular", "proximity_to_failure", "refalo-2022-proximity-to-failure-hypertrophy"},
		{"repeticiones en reserva", "rir_rpe", "halperin-2021-predicting-repetitions-to-failure"},
		{"periodización ondulante", "periodization", "moesgaard-2022-periodization-volume-equated"},
		{"rango de movimiento parcial", "range_of_motion", "wolf-2023-partial-vs-full-range-of-motion"},
		{"frecuencia hipertrofia", "frequency", "schoenfeld-2018-frequency-hypertrophy-meta-analysis"},
		{"series semanales", "volume", "pelland-2025-volume-frequency-dose-response"},
		{"autorregulación", "autoregulation", "larsen-2021-autoregulation-maximal-strength"},
	}
	for _, tc := range cases {
		r := search(t, lib, evidence.SearchInput{Query: tc.query, Limit: 5})
		if !slices.Contains(ids(r), tc.want) {
			t.Errorf("%q: %v, missing %s", tc.query, ids(r), tc.want)
		}
		for i, item := range r.Results {
			if i < 3 && !slices.Contains(item.Topics, tc.topic) {
				t.Errorf("%q: result %d %s lacks topic %s", tc.query, i, item.SourceID, tc.topic)
			}
		}
	}
}

// Words that look alike but mean other things must not lead the answer.
func TestSearchKeepsConfusableTermsApart(t *testing.T) {
	_, lib := openEvidence(t)
	first := func(q string) string {
		got := ids(search(t, lib, evidence.SearchInput{Query: q, Limit: 5}))
		if len(got) == 0 {
			t.Fatalf("%q found nothing", q)
		}
		return got[0]
	}
	// Porter stems "periodization" and "deload period" alike; the
	// periodization sources must still lead.
	if got := first("periodization"); !strings.Contains(got, "periodization") {
		t.Errorf("periodization led by %s", got)
	}
	// Rest between sets is not training at long muscle lengths.
	if got := first("long rest intervals"); !strings.Contains(got, "rest-intervals") {
		t.Errorf("long rest intervals led by %s", got)
	}
	// Autoregulating load is not low-load training.
	if got := first("load autoregulation"); got != "hickmott-2022-load-volume-autoregulation" {
		t.Errorf("load autoregulation led by %s", got)
	}
	// Frequency, not the volume studies that mention sessions.
	if got := first("frequency"); !strings.Contains(got, "frequency") {
		t.Errorf("frequency led by %s", got)
	}
}

// Contested questions surface sources on each side, and the answer names the
// debate with all its sources.
func TestSearchSurfacesContradictorySources(t *testing.T) {
	_, lib := openEvidence(t)
	cases := []struct {
		query, debate string
		sides         []string
	}{
		// A week off kept growth (Coleman) while coaches and athletes plan
		// deloads (Bell, Rogerson).
		{"deload", "deload_value", []string{"coleman-2024-one-week-deload-trial", "rogerson-2024-deloading-practices-survey"}},
		// More sets gave more growth (Schoenfeld 2019) and did not differ
		// significantly (Heaselgrave 2019).
		{"volume hypertrophy trained", "volume_dose_response", []string{"schoenfeld-2019-volume-trained-men", "heaselgrave-2019-biceps-volume-frequency"}},
		// Failure helped little (Refalo) or growth rose nearer failure (Robinson).
		{"failure hypertrophy", "proximity_to_failure", []string{"refalo-2022-proximity-to-failure-hypertrophy", "robinson-2024-proximity-to-failure-dose-response", "grgic-2022-failure-vs-non-failure"}},
		// Longer rest helped (Schoenfeld 2016); 1 to 2 minutes recommended (ACSM).
		{"rest", "rest_interval_length", []string{"schoenfeld-2016-rest-intervals-trained-men", "acsm-2009-progression-models-position-stand"}},
	}
	for _, tc := range cases {
		r := search(t, lib, evidence.SearchInput{Query: tc.query, Limit: 10})
		got := ids(r)
		for _, side := range tc.sides {
			if !slices.Contains(got, side) {
				t.Errorf("%q: %v, missing side %s", tc.query, got, side)
			}
		}
		var debate *evidence.DebateRef
		for i := range r.Debates {
			if r.Debates[i].ID == tc.debate {
				debate = &r.Debates[i]
			}
		}
		if debate == nil || len(debate.SourceIDs) < 2 {
			t.Errorf("%q: debate %s not named: %+v", tc.query, tc.debate, r.Debates)
		}
	}
}

func TestSearchNoResults(t *testing.T) {
	_, lib := openEvidence(t)
	for _, q := range []string{"", "creatine loading phase", "xyzzy", "¿¡!?", "the and of"} {
		r := search(t, lib, evidence.SearchInput{Query: q})
		if len(r.Results) != 0 || r.NoResults == "" || r.Match != evidence.MatchNone {
			t.Errorf("%q: %+v", q, r)
		}
	}
}

// Nothing a caller types reaches FTS5 as syntax.
func TestSearchSpecialCharacters(t *testing.T) {
	_, lib := openEvidence(t)
	for _, q := range []string{
		`"`, `"rest`, `rest"`, `rest*`, `*`, `rest AND`, `OR OR`, `NOT rest`, `rest NEAR/2 sets`, `NEAR(rest sets)`,
		`title:rest`, `{title}:rest`, `^rest`, `rest + sets`, `(rest`, `rest)`, `'; DROP TABLE evidence_sources; --`,
		`rest -- comment`, `rest\x00sets`, "rest‮sets", `ñandú`, `🏋️ rest`, `rest_intervals`, `1-2 min`, `%rest%`,
		strings.Repeat(`" `, 240),
	} {
		if _, err := lib.Search(context.Background(), evidence.SearchInput{Query: q}); err != nil {
			t.Errorf("%q: %v", q, err)
		}
	}
	r := search(t, lib, evidence.SearchInput{Query: `"rest" AND (intervals)*`})
	if len(r.Results) == 0 || !strings.Contains(r.Results[0].SourceID, "rest") {
		t.Fatalf("quoted operators: %v", ids(r))
	}
}

func TestSearchFilters(t *testing.T) {
	_, lib := openEvidence(t)
	r := search(t, lib, evidence.SearchInput{Query: "hypertrophy", Filters: evidence.Filters{Topic: "deload"}, Limit: 20})
	if len(r.Results) == 0 {
		t.Fatal("topic filter found nothing")
	}
	for _, item := range r.Results {
		if !slices.Contains(item.Topics, "deload") {
			t.Errorf("topic filter let in %s %v", item.SourceID, item.Topics)
		}
	}
	r = search(t, lib, evidence.SearchInput{Query: "hypertrophy", Filters: evidence.Filters{StudyType: "randomized_trial"}, Limit: 20})
	for _, item := range r.Results {
		if item.StudyType != "randomized_trial" {
			t.Errorf("study type filter let in %s %s", item.SourceID, item.StudyType)
		}
	}
	r = search(t, lib, evidence.SearchInput{Query: "hypertrophy", Filters: evidence.Filters{YearFrom: 2022, YearTo: 2024}, Limit: 20})
	if len(r.Results) == 0 {
		t.Fatal("year filter found nothing")
	}
	for _, item := range r.Results {
		if item.Year < 2022 || item.Year > 2024 {
			t.Errorf("year filter let in %s %d", item.SourceID, item.Year)
		}
	}
	r = search(t, lib, evidence.SearchInput{Query: "deload", Filters: evidence.Filters{YearTo: 2010}})
	if len(r.Results) != 0 || r.NoResults == "" {
		t.Fatalf("filtered to nothing: %v", ids(r))
	}
	r = search(t, lib, evidence.SearchInput{Query: "hypertrophy", Limit: 3})
	if len(r.Results) != 3 {
		t.Fatalf("limit 3 gave %d", len(r.Results))
	}
}

func TestGetEvidence(t *testing.T) {
	_, lib := openEvidence(t)
	e, err := lib.Get(context.Background(), "pelland-2025-volume-frequency-dose-response")
	if err != nil {
		t.Fatal(err)
	}
	if e.Source.DOI != "10.1007/s40279-025-02344-w" || e.Source.ReviewStatus != evidence.ReviewUnreviewed || len(e.Debates) != 2 {
		t.Fatalf("%+v", e)
	}
	for _, id := range []string{"no-such-source", "PELLAND", "'; --", ""} {
		if _, err = lib.Get(context.Background(), id); !errors.Is(err, evidence.ErrUnknownSource) {
			t.Errorf("%q: %v", id, err)
		}
	}
}

func corpusVersion(t *testing.T, version string, sources ...evidence.Source) evidence.Corpus {
	t.Helper()
	c := evidence.Corpus{Version: version, Digest: version + "-digest", Debates: []evidence.Debate{{ID: "d1", Question: "Q?"}}, Sources: sources}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	return c
}

func fakeSource(id, title string) evidence.Source {
	return evidence.Source{
		ID: id, Title: title, Authors: []string{"A Author"}, Year: 2020, Publication: "Journal",
		StudyType: "meta_analysis", DOI: "10.1234/" + id, URL: "https://doi.org/10.1234/" + id,
		Population: "Adults", Topics: []string{"volume"}, Debates: []string{"d1"},
		KeyFindings: []string{"A finding about " + title}, Methods: "Methods.", Limitations: "Limits.",
		SummaryBasis: evidence.SummaryFromAbstract, Verification: evidence.Verification{Crossref: "2026-11-25", RetractionCheck: "None."},
		IngestedOn: "2026-11-25", ContentVersion: 1, ReviewStatus: evidence.ReviewUnreviewed,
	}
}

func TestSyncEvidenceRebuildsOnlyWhenTheCorpusChanges(t *testing.T) {
	ctx := context.Background()
	store, lib := openEvidence(t)
	loads := func() int {
		var n int
		if err := store.db.QueryRow(`SELECT COUNT(*) FROM evidence_corpus_loads`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := loads()
	embedded, _ := evidence.Embedded()
	if err := store.SyncEvidence(ctx, embedded); err != nil {
		t.Fatal(err)
	}
	if loads() != before {
		t.Fatal("the same corpus was loaded again")
	}

	v2 := corpusVersion(t, "v2", fakeSource("zeta-1", "Zeta quokka study"), fakeSource("eta-2", "Eta wombat study"))
	if err := store.SyncEvidence(ctx, v2); err != nil {
		t.Fatal(err)
	}
	if loads() != before+1 {
		t.Fatal("a new version was not loaded")
	}
	r := search(t, lib, evidence.SearchInput{Query: "quokka"})
	if r.CorpusVersion != "v2" || len(r.Results) != 1 || r.Results[0].SourceID != "zeta-1" {
		t.Fatalf("after rebuild: %+v", r)
	}
	if r = search(t, lib, evidence.SearchInput{Query: "deload"}); len(r.Results) != 0 {
		t.Fatalf("old sources survived the rebuild: %v", ids(r))
	}
	if _, err := lib.Get(ctx, "pelland-2025-volume-frequency-dose-response"); !errors.Is(err, evidence.ErrUnknownSource) {
		t.Fatalf("old source still readable: %v", err)
	}

	// Same version, different content: the digest still triggers a rebuild.
	v2b := corpusVersion(t, "v2", fakeSource("zeta-1", "Zeta numbat study"), fakeSource("eta-2", "Eta wombat study"))
	v2b.Digest = "v2-edited"
	if err := store.SyncEvidence(ctx, v2b); err != nil {
		t.Fatal(err)
	}
	if r = search(t, lib, evidence.SearchInput{Query: "numbat"}); len(r.Results) != 1 {
		t.Fatalf("an edit under the same version was not indexed: %+v", r)
	}

	// Syncing the embedded corpus again restores it, as Open does after an
	// upgrade.
	if err := store.SyncEvidence(ctx, embedded); err != nil {
		t.Fatal(err)
	}
	if r = search(t, lib, evidence.SearchInput{Query: "deload"}); len(r.Results) == 0 || r.CorpusVersion != embedded.Version {
		t.Fatalf("embedded corpus not restored: %+v", r)
	}
}

// Open rebuilds the index when the database holds an older corpus.
func TestOpenRebuildsAStaleIndex(t *testing.T) {
	path := freshDB(t)
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.SyncEvidence(context.Background(), corpusVersion(t, "old", fakeSource("zeta-1", "Zeta quokka"), fakeSource("eta-2", "Eta wombat"))); err != nil {
		t.Fatal(err)
	}
	store.Close()
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	embedded, _ := evidence.Embedded()
	loaded, err := store.Evidence().Loaded(context.Background())
	if err != nil || loaded.Version != embedded.Version || loaded.SourceCount != len(embedded.Sources) {
		t.Fatalf("after reopen: %+v %v", loaded, err)
	}
}

func TestSyncEvidenceRefusesAnInvalidCorpus(t *testing.T) {
	store, lib := openEvidence(t)
	bad := evidence.Corpus{Version: "v9", Digest: "x", Sources: []evidence.Source{{ID: "x"}}}
	if err := store.SyncEvidence(context.Background(), bad); !errors.Is(err, evidence.ErrInvalid) {
		t.Fatalf("err = %v", err)
	}
	if r := search(t, lib, evidence.SearchInput{Query: "deload"}); len(r.Results) == 0 {
		t.Fatal("a refused corpus emptied the index")
	}
}

func BenchmarkSearchEvidence(b *testing.B) {
	_, lib := openEvidence(b)
	ctx := context.Background()
	queries := []string{"weekly volume hypertrophy", "descanso entre series", "proximity to failure strength", "deload", "creatine"}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := lib.Search(ctx, evidence.SearchInput{Query: queries[i%len(queries)]}); err != nil {
			b.Fatal(err)
		}
	}
}
