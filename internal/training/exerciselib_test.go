package training

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// libraryFake keeps one person's library in memory. Its UserStore is nil: the
// library must not need anything else.
type libraryFake struct {
	UserStore
	entries map[string]OwnExerciseEntry
	aliases map[string]ExerciseAlias
	volume  []ExerciseVolume
	filters []ListFilter
}

func newLibraryFake() *libraryFake {
	return &libraryFake{entries: map[string]OwnExerciseEntry{}, aliases: map[string]ExerciseAlias{}}
}

func (f *libraryFake) OwnExerciseEntries(context.Context) ([]OwnExerciseEntry, error) {
	var out []OwnExerciseEntry
	for _, e := range f.entries {
		out = append(out, e)
	}
	return out, nil
}
func (f *libraryFake) SaveOwnExerciseEntry(_ context.Context, e OwnExerciseEntry) error {
	f.entries[e.Key] = e
	return nil
}
func (f *libraryFake) ExerciseAliases(context.Context) ([]ExerciseAlias, error) {
	var out []ExerciseAlias
	for _, a := range f.aliases {
		out = append(out, a)
	}
	return out, nil
}
func (f *libraryFake) SetExerciseAlias(_ context.Context, a ExerciseAlias) error {
	f.aliases[a.Key] = a
	return nil
}
func (f *libraryFake) ExerciseVolume(_ context.Context, filter ListFilter) ([]ExerciseVolume, error) {
	f.filters = append(f.filters, filter)
	return f.volume, nil
}

func newLibraryService() (*Service, *libraryFake) {
	fake := newLibraryFake()
	return NewService(fake, nil), fake
}

func TestPublicMuscleMapIsFixedAndFlagsEveryNameWithoutOneTarget(t *testing.T) {
	clean := map[string]string{
		"abdominals": "abdomen", "adductors": "aductor", "biceps": "biceps", "calves": "gemelo",
		"chest": "pecho", "forearms": "antebrazo", "glutes": "gluteo", "hamstrings": "isquios",
		"lats": "espalda", "quadriceps": "cuadriceps", "traps": "trapecio", "triceps": "triceps",
	}
	uncertain := map[string][]string{
		"shoulders":   {"hombro anterior", "hombro lateral", "hombro posterior"},
		"middle back": {"espalda", "trapecio"},
		"lower back":  {"espalda"},
		"abductors":   {"gluteo"},
		"neck":        {"trapecio"},
	}
	if len(PublicMuscleMap) != 17 {
		t.Fatalf("the table has %d names, want free-exercise-db's 17", len(PublicMuscleMap))
	}
	for name, m := range PublicMuscleMap {
		if group, ok := clean[name]; ok {
			if m.Uncertain || m.MuscleGroup != group || len(m.Candidates) != 0 {
				t.Errorf("%s = %+v, want %s", name, m, group)
			}
			continue
		}
		want, ok := uncertain[name]
		if !ok {
			t.Errorf("%s is in neither list", name)
			continue
		}
		if !m.Uncertain || m.MuscleGroup != "" || !reflect.DeepEqual(m.Candidates, want) {
			t.Errorf("%s = %+v, want uncertain among %v", name, m, want)
		}
		for _, c := range m.Candidates {
			if !ValidMuscleGroup(c) {
				t.Errorf("%s candidate %q is not a muscle group", name, c)
			}
		}
	}
}

func TestPublicLayerIsThePinnedDatasetAndNeverGuessesAMuscle(t *testing.T) {
	info, err := PublicLibraryInfo()
	if err != nil {
		t.Fatal(err)
	}
	if info.Commit != "f00c92c7dcf1216a928a52c3706c7ce8e2f71ed5" || info.License != "Unlicense" ||
		info.UpstreamSHA256 != "5bb747e3fc658f095a60dcbf6d53c96627acdcc6ffb6fffde86f7e26995d40bf" {
		t.Fatalf("provenance = %+v", info)
	}
	entries, err := PublicLibraryEntries()
	if err != nil {
		t.Fatal(err)
	}
	cleanEntries, uncertainEntries := 0, 0
	for _, e := range entries {
		if e.Layer != LayerPublic || e.SourceID == "" || len(e.Muscles) == 0 || e.Muscles[0].Role != RolePrimary {
			t.Fatalf("entry %+v", e)
		}
		anyUncertain := false
		for _, m := range e.Muscles {
			switch {
			case m.Uncertain:
				anyUncertain = true
				if m.MuscleGroup != "" || len(m.Candidates) == 0 || m.SourceMuscle == "" {
					t.Fatalf("%s: uncertain muscle %+v", e.Name, m)
				}
			case !ValidMuscleGroup(m.MuscleGroup):
				t.Fatalf("%s: muscle %+v", e.Name, m)
			}
			if (m.Role == RolePrimary) != (m.Credit == PrimaryCredit) || (m.Role == RoleSecondary) != (m.Credit == SecondaryCredit) {
				t.Fatalf("%s: credit %+v", e.Name, m)
			}
		}
		if anyUncertain != e.Uncertain {
			t.Fatalf("%s: uncertain flag %v", e.Name, e.Uncertain)
		}
		if anyUncertain {
			uncertainEntries++
		} else {
			cleanEntries++
		}
	}
	if len(entries) != 876 || cleanEntries != 369 || uncertainEntries != 507 {
		t.Fatalf("entries = %d (%d clean, %d uncertain), want 876 (369, 507)", len(entries), cleanEntries, uncertainEntries)
	}
}

func TestNamesFoldCaseAccentsSpacesAndPunctuation(t *testing.T) {
	for in, want := range map[string]string{
		"  Press   BÁNCA ":           "press banca",
		"Jalón al pecho":             "jalon al pecho",
		"Extensión de cuádriceps":    "extension de cuadriceps",
		"Peso muerto rumano (barra)": "peso muerto rumano barra",
		"Bench Press - Powerlifting": "bench press powerlifting",
		"Pull-Up":                    "pull up",
		"ñ Ü ç":                      "n u c",
		"3/4 Sit-Up":                 "3 4 sit up",
		" - ":                        "",
	} {
		if got := FoldExerciseName(in); got != want {
			t.Errorf("FoldExerciseName(%q) = %q, want %q", in, got, want)
		}
	}
	if got := ExerciseName("  Jalón   AL pecho "); got != "jalón al pecho" {
		t.Fatalf("ExerciseName keeps accents and folds case and spaces: %q", got)
	}
}

func lookup(t *testing.T, svc *Service, name string) ExerciseLookup {
	t.Helper()
	got, err := svc.LookupExercise(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func wantFound(t *testing.T, got ExerciseLookup, layer, match, entry string) {
	t.Helper()
	if !got.Found || got.Entry == nil || got.Layer != layer || got.Match != match || got.Entry.Layer != layer || got.Entry.Name != entry || len(got.Candidates) != 0 {
		t.Fatalf("lookup = %+v (entry %+v), want %s %s %q", got, got.Entry, layer, match, entry)
	}
}

func groupsOf(e *LibraryEntry, role string) []string {
	var out []string
	for _, m := range e.Muscles {
		if m.Role == role && !m.Uncertain {
			out = append(out, m.MuscleGroup)
		}
	}
	return out
}

func TestLookupGoesAliasOwnCuratedPublicAndSaysHowItMatched(t *testing.T) {
	ctx := context.Background()
	svc, _ := newLibraryService()

	// Public layer, exact (case aside) and normalized.
	wantFound(t, lookup(t, svc, "Barbell Bench Press - Medium Grip"), LayerPublic, MatchExact, "Barbell Bench Press - Medium Grip")
	wantFound(t, lookup(t, svc, "  barbell  bench press - MEDIUM grip"), LayerPublic, MatchExact, "Barbell Bench Press - Medium Grip")
	public := lookup(t, svc, "barbell bench press medium grip")
	wantFound(t, public, LayerPublic, MatchNormalized, "Barbell Bench Press - Medium Grip")
	if public.Entry.SourceID != "Barbell_Bench_Press_-_Medium_Grip" || public.Entry.Equipment != "barbell" || !public.Entry.Uncertain {
		t.Fatalf("public entry = %+v", public.Entry)
	}
	// Curated wins over the public entry of the same name, which the issue
	// names as wrong (triceps primary).
	curated := lookup(t, svc, "Bench Press - Powerlifting")
	wantFound(t, curated, LayerCurated, MatchExact, "bench press - powerlifting")
	if got := groupsOf(curated.Entry, RolePrimary); !reflect.DeepEqual(got, []string{"pecho"}) || curated.Entry.Uncertain {
		t.Fatalf("curated primary = %v", got)
	}
	wantFound(t, lookup(t, svc, "bench press powerlifting"), LayerCurated, MatchNormalized, "bench press - powerlifting")

	// Own entry, in Spanish: accents, case and spacing.
	if _, err := svc.SaveExerciseEntry(ctx, LibraryEntryInput{Name: "Press Banca", Primary: []string{"pecho"}, Secondary: []string{"triceps", "hombro anterior"}}); err != nil {
		t.Fatal(err)
	}
	wantFound(t, lookup(t, svc, "PRESS   banca"), LayerOwn, MatchExact, "press banca")
	wantFound(t, lookup(t, svc, "press bánca"), LayerOwn, MatchNormalized, "press banca")

	// An alias wins over everything and names the entry's layer.
	if _, err := svc.SetExerciseAlias(ctx, "Press Plano", "barbell bench press - medium grip"); err != nil {
		t.Fatal(err)
	}
	aliased := lookup(t, svc, "press plano")
	wantFound(t, aliased, LayerPublic, MatchAlias, "Barbell Bench Press - Medium Grip")
	if aliased.Alias != "press plano" {
		t.Fatalf("alias = %q", aliased.Alias)
	}
	wantFound(t, lookup(t, svc, "Préss  Plano"), LayerPublic, MatchAlias, "Barbell Bench Press - Medium Grip")

	// Correcting the public entry (copy on write): the alias now reaches the own copy.
	if _, err := svc.SaveExerciseEntry(ctx, LibraryEntryInput{Name: "Barbell Bench Press - Medium Grip", Primary: []string{"pecho"}, Secondary: []string{"triceps", "hombro anterior"}}); err != nil {
		t.Fatal(err)
	}
	wantFound(t, lookup(t, svc, "press plano"), LayerOwn, MatchAlias, "barbell bench press - medium grip")
	wantFound(t, lookup(t, svc, "Barbell Bench Press - Medium Grip"), LayerOwn, MatchExact, "barbell bench press - medium grip")

	// A miss lists candidates, best first, and picks none.
	miss := lookup(t, svc, "press inclinado")
	if miss.Found || miss.Entry != nil || miss.Layer != "" || miss.Match != "" || len(miss.Candidates) == 0 || len(miss.Candidates) > 10 {
		t.Fatalf("miss = %+v", miss)
	}
	if miss.Candidates[0].Layer != LayerOwn || !strings.Contains(miss.Candidates[0].Name, "press") {
		t.Fatalf("own entries rank first among equal matches: %+v", miss.Candidates)
	}
	if none := lookup(t, svc, "zzzz qqqq"); none.Found || none.Candidates == nil || len(none.Candidates) != 0 {
		t.Fatalf("nothing alike = %+v", none)
	}
	if _, err := svc.LookupExercise(ctx, "  -  "); !errors.Is(err, ErrValidation) {
		t.Fatalf("blank name = %v", err)
	}
}

func TestCorrectingASharedEntryCopiesItIntoTheOwnLayer(t *testing.T) {
	ctx := context.Background()
	svc, fake := newLibraryService()
	curated, err := svc.SaveExerciseEntry(ctx, LibraryEntryInput{Name: "bench press - POWERLIFTING", Primary: []string{"Pecho"}, Secondary: []string{"triceps"}})
	if err != nil {
		t.Fatal(err)
	}
	if curated.Layer != LayerOwn || curated.BasedOn != "curated:bench press - powerlifting" || curated.Uncertain {
		t.Fatalf("curated copy = %+v", curated)
	}
	public, err := svc.SaveExerciseEntry(ctx, LibraryEntryInput{Name: "Bent Over Barbell Row", Primary: []string{"espalda"}, Secondary: []string{"trapecio", "biceps", "hombro posterior"}})
	if err != nil {
		t.Fatal(err)
	}
	if public.BasedOn != "public:Bent_Over_Barbell_Row" {
		t.Fatalf("public copy = %+v", public)
	}
	fresh, err := svc.SaveExerciseEntry(ctx, LibraryEntryInput{Name: "Remo Gironda", Primary: []string{"espalda"}})
	if err != nil || fresh.BasedOn != "" || len(fresh.Muscles) != 1 {
		t.Fatalf("new entry = %+v, %v", fresh, err)
	}
	stored := fake.entries["bent over barbell row"]
	if stored.Name != "bent over barbell row" || !reflect.DeepEqual(stored.Primary, []string{"espalda"}) ||
		!reflect.DeepEqual(stored.Secondary, []string{"trapecio", "biceps", "hombro posterior"}) || stored.BasedOn != "public:Bent_Over_Barbell_Row" {
		t.Fatalf("stored = %+v", stored)
	}
	// Saving again replaces the entry under the same folded name.
	if _, err := svc.SaveExerciseEntry(ctx, LibraryEntryInput{Name: "remo gironda", Primary: []string{"espalda"}, Secondary: []string{"biceps"}}); err != nil {
		t.Fatal(err)
	}
	if len(fake.entries) != 3 || !reflect.DeepEqual(fake.entries["remo gironda"].Secondary, []string{"biceps"}) {
		t.Fatalf("entries = %+v", fake.entries)
	}
	got := lookup(t, svc, "Remo Gironda")
	if !reflect.DeepEqual(groupsOf(got.Entry, RoleSecondary), []string{"biceps"}) {
		t.Fatalf("replaced entry = %+v", got.Entry)
	}
}

func TestLibraryWritesRefuseWhatTheyCannotStore(t *testing.T) {
	ctx := context.Background()
	svc, fake := newLibraryService()
	long := strings.Repeat("a", 121)
	for name, in := range map[string]LibraryEntryInput{
		"blank name":      {Name: " ", Primary: []string{"pecho"}},
		"long name":       {Name: long, Primary: []string{"pecho"}},
		"no primary":      {Name: "x", Secondary: []string{"pecho"}},
		"unknown group":   {Name: "x", Primary: []string{"chest"}},
		"twice":           {Name: "x", Primary: []string{"pecho", "pecho"}},
		"in both lists":   {Name: "x", Primary: []string{"pecho"}, Secondary: []string{"Pecho"}},
		"uncertain group": {Name: "x", Primary: []string{"hombro"}},
	} {
		if _, err := svc.SaveExerciseEntry(ctx, in); !errors.Is(err, ErrValidation) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if _, err := svc.SetExerciseAlias(ctx, "press plano", "no such exercise"); !errors.Is(err, ErrValidation) {
		t.Errorf("missing target: %v", err)
	}
	if _, err := svc.SetExerciseAlias(ctx, "bench press powerlifting", "Bench Press - Powerlifting"); !errors.Is(err, ErrValidation) {
		t.Errorf("alias naming its own target: %v", err)
	}
	if _, err := svc.SetExerciseAlias(ctx, " ", "Bench Press - Powerlifting"); !errors.Is(err, ErrValidation) {
		t.Errorf("blank alias: %v", err)
	}
	if len(fake.entries) != 0 || len(fake.aliases) != 0 {
		t.Fatalf("a refused write stored something: %+v %+v", fake.entries, fake.aliases)
	}
	// An alias never points at another alias: its target resolves without them.
	if _, err := svc.SetExerciseAlias(ctx, "press plano", "Bench Press - Powerlifting"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetExerciseAlias(ctx, "banca", "press plano"); !errors.Is(err, ErrValidation) {
		t.Fatalf("alias to an alias: %v", err)
	}
	if a := fake.aliases["press plano"]; a.Alias != "press plano" || a.Target != "bench press - powerlifting" {
		t.Fatalf("stored alias = %+v", a)
	}
}

// A store that keeps no library still answers lookups from the shared layers,
// refuses library writes, and adds no fractional view.
func TestAStoreWithoutALibraryOnlyReadsTheSharedLayers(t *testing.T) {
	ctx := context.Background()
	svc := NewService(&memoryFake{}, nil)
	wantFound(t, lookup(t, svc, "Bench Press - Powerlifting"), LayerCurated, MatchExact, "bench press - powerlifting")
	if _, err := svc.SaveExerciseEntry(ctx, LibraryEntryInput{Name: "x", Primary: []string{"pecho"}}); !errors.Is(err, ErrNoExerciseLibrary) {
		t.Fatalf("save = %v", err)
	}
	if _, err := svc.SetExerciseAlias(ctx, "x", "Bench Press - Powerlifting"); !errors.Is(err, ErrNoExerciseLibrary) {
		t.Fatalf("alias = %v", err)
	}
	if v, err := svc.FractionalVolume(ctx, ListFilter{}); v != nil || err != nil {
		t.Fatalf("fractional = %+v, %v", v, err)
	}
	if v, err := svc.FractionalWeeklyVolume(ctx, ListFilter{}); v != nil || err != nil {
		t.Fatalf("weekly fractional = %+v, %v", v, err)
	}
	if v, err := svc.ExercisesNeedingMapping(ctx); err != nil || v == nil || len(v) != 0 {
		t.Fatalf("needs mapping = %+v, %v", v, err)
	}
}

// fractionalRows: one exercise per layer and one with no entry, in two weeks.
func fractionalRows() []ExerciseVolume {
	return []ExerciseVolume{
		{WeekStart: "2026-11-16", Exercise: "press banca", MuscleGroup: "pecho", TotalSI: 3, Sets: 3},
		{WeekStart: "2026-11-16", Exercise: "bench press - powerlifting", MuscleGroup: "triceps", TotalSI: 2, Sets: 2},
		{WeekStart: "2026-11-16", Exercise: "barbell bench press - medium grip", MuscleGroup: "", TotalSI: 4, Sets: 4},
		{WeekStart: "2026-11-16", Exercise: "misterio", MuscleGroup: "", TotalSI: 0.4, Sets: 1},
		{WeekStart: "2026-11-23", Exercise: "bench press - powerlifting", MuscleGroup: "triceps", TotalSI: 1.4, Sets: 1},
	}
}

func TestFractionalVolumeCreditsPrimaryOneSecondaryHalfAndShowsUncertainMuscles(t *testing.T) {
	ctx := context.Background()
	svc, fake := newLibraryService()
	fake.volume = fractionalRows()
	filter := ListFilter{From: "2026-11-01", To: "2026-11-30"}
	total, err := svc.FractionalVolume(ctx, filter)
	if err != nil {
		t.Fatal(err)
	}
	if total.Method != FractionalMethod || total.Source != FractionalSource || !strings.Contains(total.Source, "10.1007/s40279-025-02344-w") {
		t.Fatalf("method/source = %q / %q", total.Method, total.Source)
	}
	// press banca: no entry, its group pecho at 1. bench press - powerlifting
	// (curated): pecho 1, triceps and hombro anterior 0.5. barbell bench press
	// (public): chest 1, triceps 0.5, shoulders uncertain. misterio: no entry
	// and no group.
	want := []FractionalGroup{
		{MuscleGroup: "pecho", Sets: 10, TotalSI: 10.4},
		{MuscleGroup: "triceps", Sets: 3.5, TotalSI: 3.7},
		{MuscleGroup: "hombro anterior", Sets: 1.5, TotalSI: 1.7},
		{MuscleGroup: "", Sets: 1, TotalSI: 0.4},
	}
	if !reflect.DeepEqual(total.Groups, want) {
		t.Fatalf("groups = %+v\nwant %+v", total.Groups, want)
	}
	wantUncertain := []UncertainCredit{{Exercise: "barbell bench press - medium grip", Entry: "Barbell Bench Press - Medium Grip", Layer: LayerPublic,
		SourceMuscle: "shoulders", Role: RoleSecondary, Candidates: []string{"hombro anterior", "hombro lateral", "hombro posterior"}, Sets: 2, TotalSI: 2}}
	if !reflect.DeepEqual(total.Uncertain, wantUncertain) {
		t.Fatalf("uncertain = %+v", total.Uncertain)
	}

	weekly, err := svc.FractionalWeeklyVolume(ctx, filter)
	if err != nil {
		t.Fatal(err)
	}
	if len(weekly.Weeks) != 2 || weekly.Weeks[0].WeekStart != "2026-11-23" || weekly.Weeks[1].WeekStart != "2026-11-16" || weekly.Method != FractionalMethod {
		t.Fatalf("weeks = %+v", weekly.Weeks)
	}
	latest := []FractionalGroup{{MuscleGroup: "pecho", Sets: 1, TotalSI: 1.4}, {MuscleGroup: "hombro anterior", Sets: 0.5, TotalSI: 0.7}, {MuscleGroup: "triceps", Sets: 0.5, TotalSI: 0.7}}
	if !reflect.DeepEqual(weekly.Weeks[0].Groups, latest) || len(weekly.Weeks[0].Uncertain) != 0 || weekly.Weeks[0].Uncertain == nil {
		t.Fatalf("latest week = %+v", weekly.Weeks[0])
	}
	if !reflect.DeepEqual(weekly.Weeks[1].Uncertain, wantUncertain) {
		t.Fatalf("first week uncertain = %+v", weekly.Weeks[1].Uncertain)
	}
	for _, f := range fake.filters {
		if f != filter {
			t.Fatalf("the range was not passed on: %+v", fake.filters)
		}
	}

	// An alias moves press banca onto its entry's muscles.
	if _, err := svc.SetExerciseAlias(ctx, "press banca", "bench press - powerlifting"); err != nil {
		t.Fatal(err)
	}
	total, err = svc.FractionalVolume(ctx, filter)
	if err != nil {
		t.Fatal(err)
	}
	if total.Groups[1] != (FractionalGroup{MuscleGroup: "triceps", Sets: 5, TotalSI: 5.2}) {
		t.Fatalf("after the alias = %+v", total.Groups)
	}
}

func TestExercisesNeedingMappingListsMissesThenUncertainEntries(t *testing.T) {
	ctx := context.Background()
	svc, fake := newLibraryService()
	fake.volume = fractionalRows()
	got, err := svc.ExercisesNeedingMapping(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, n := range got {
		names = append(names, n.Reason+":"+n.Exercise)
	}
	if want := []string{"not_found:press banca", "not_found:misterio", "uncertain:barbell bench press - medium grip"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("needs mapping = %v, want %v", names, want)
	}
	if got[0].Sets != 3 || got[0].MuscleGroup != "pecho" || got[0].Entry != nil || len(got[0].Candidates) == 0 || len(got[0].Candidates) > 3 {
		t.Fatalf("press banca = %+v", got[0])
	}
	if u := got[2]; u.Entry == nil || u.Entry.Layer != LayerPublic || u.Match != MatchExact || u.Sets != 4 {
		t.Fatalf("uncertain = %+v", u)
	}
	// Once aliased and corrected, nothing is left but misterio.
	if _, err := svc.SetExerciseAlias(ctx, "press banca", "bench press - powerlifting"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SaveExerciseEntry(ctx, LibraryEntryInput{Name: "barbell bench press - medium grip", Primary: []string{"pecho"}, Secondary: []string{"triceps"}}); err != nil {
		t.Fatal(err)
	}
	got, err = svc.ExercisesNeedingMapping(ctx)
	if err != nil || len(got) != 1 || got[0].Exercise != "misterio" {
		t.Fatalf("after mapping = %+v, %v", got, err)
	}
}

func TestCandidatesAreDeterministic(t *testing.T) {
	svc, _ := newLibraryService()
	first := lookup(t, svc, "curl")
	for i := 0; i < 5; i++ {
		if again := lookup(t, svc, "curl"); !reflect.DeepEqual(again.Candidates, first.Candidates) {
			t.Fatal("candidates changed between calls")
		}
	}
	names := make([]string, 0, len(first.Candidates))
	for _, c := range first.Candidates {
		names = append(names, c.Name)
	}
	if !sort.SliceIsSorted(first.Candidates, func(i, j int) bool { return len(first.Candidates[i].Name) < len(first.Candidates[j].Name) }) {
		t.Fatalf("equal scores rank shorter names first: %v", names)
	}
}
