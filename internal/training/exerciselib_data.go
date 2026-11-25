package training

import (
	"embed"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
)

// The library's two shared layers ship inside the binary. Neither is any one
// person's, and neither can be changed at run time: the curated layer is edited
// by pull request, and the public layer is re-imported with
// cmd/exerciselib-import from a pinned upstream commit.
//
//go:embed exerciselibdata/free-exercise-db.json exerciselibdata/curated.json
var exerciseLibFS embed.FS

// PublicDataset is the vendored free-exercise-db file: its provenance and the
// fields the library uses. cmd/exerciselib-import writes it.
type PublicDataset struct {
	Source         string           `json:"source"`
	Repository     string           `json:"repository"`
	License        string           `json:"license"`
	Commit         string           `json:"commit"`
	UpstreamFile   string           `json:"upstream_file"`
	UpstreamSHA256 string           `json:"upstream_sha256"`
	Exercises      []PublicExercise `json:"exercises"`
}

// PublicExercise is one upstream exercise trimmed to what the library reads.
// Muscle names are upstream's own; PublicMuscleMap turns them into
// MuscleGroups.
type PublicExercise struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	PrimaryMuscles   []string `json:"primaryMuscles"`
	SecondaryMuscles []string `json:"secondaryMuscles"`
	Equipment        string   `json:"equipment,omitempty"`
	Category         string   `json:"category,omitempty"`
}

// curatedExercise is one hand-checked entry of the repository's own layer,
// in MuscleGroups. Reason says what was checked and against what.
type curatedExercise struct {
	Name      string   `json:"name"`
	Primary   []string `json:"primary"`
	Secondary []string `json:"secondary"`
	Reason    string   `json:"reason"`
}

// PublicMuscle is how one upstream muscle name maps onto MuscleGroups. A name
// with exactly one target has MuscleGroup set; any other is Uncertain, with the
// groups it could mean as Candidates, and is never counted toward a group.
type PublicMuscle struct {
	MuscleGroup string
	Uncertain   bool
	Candidates  []string
}

// PublicMuscleMap is the fixed table from free-exercise-db's 17 muscle names
// to MuscleGroups. Five names have no one-to-one target:
//   - shoulders does not tell the anterior, lateral and posterior heads apart;
//   - middle back is rhomboids and middle trapezius, between espalda and trapecio;
//   - lower back (spinal erectors), abductors (hip abductors) and neck have
//     no group of their own.
var PublicMuscleMap = map[string]PublicMuscle{
	"abdominals":  {MuscleGroup: "abdomen"},
	"adductors":   {MuscleGroup: "aductor"},
	"biceps":      {MuscleGroup: "biceps"},
	"calves":      {MuscleGroup: "gemelo"},
	"chest":       {MuscleGroup: "pecho"},
	"forearms":    {MuscleGroup: "antebrazo"},
	"glutes":      {MuscleGroup: "gluteo"},
	"hamstrings":  {MuscleGroup: "isquios"},
	"lats":        {MuscleGroup: "espalda"},
	"quadriceps":  {MuscleGroup: "cuadriceps"},
	"traps":       {MuscleGroup: "trapecio"},
	"triceps":     {MuscleGroup: "triceps"},
	"shoulders":   {Uncertain: true, Candidates: []string{"hombro anterior", "hombro lateral", "hombro posterior"}},
	"middle back": {Uncertain: true, Candidates: []string{"espalda", "trapecio"}},
	"lower back":  {Uncertain: true, Candidates: []string{"espalda"}},
	"abductors":   {Uncertain: true, Candidates: []string{"gluteo"}},
	"neck":        {Uncertain: true, Candidates: []string{"trapecio"}},
}

// sharedLayer is one read-only layer, indexed by folded name.
type sharedLayer struct {
	entries []LibraryEntry
	byKey   map[string]int
}

func (l *sharedLayer) find(key string) (LibraryEntry, bool) {
	i, ok := l.byKey[key]
	if !ok {
		return LibraryEntry{}, false
	}
	return l.entries[i], true
}

type sharedLibrary struct {
	curated, public sharedLayer
	dataset         PublicDataset
	err             error
}

var (
	sharedOnce sync.Once
	shared     sharedLibrary
)

// sharedLayers loads the curated and public layers once. A broken embedded
// file is a build mistake that the tests catch; at run time it leaves both
// layers empty and is reported by every lookup.
func sharedLayers() *sharedLibrary {
	sharedOnce.Do(func() {
		shared.err = shared.load()
	})
	return &shared
}

func (l *sharedLibrary) load() error {
	raw, err := exerciseLibFS.ReadFile("exerciselibdata/free-exercise-db.json")
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, &l.dataset); err != nil {
		return fmt.Errorf("exercise library: public data: %w", err)
	}
	public := sharedLayer{byKey: map[string]int{}}
	for _, e := range l.dataset.Exercises {
		entry := publicEntry(e)
		key := FoldExerciseName(entry.Name)
		if key == "" {
			return fmt.Errorf("exercise library: public exercise %q has no name", e.ID)
		}
		if _, dup := public.byKey[key]; dup {
			return fmt.Errorf("exercise library: public name %q appears twice", e.Name)
		}
		public.byKey[key] = len(public.entries)
		public.entries = append(public.entries, entry)
	}
	raw, err = exerciseLibFS.ReadFile("exerciselibdata/curated.json")
	if err != nil {
		return err
	}
	var curated struct {
		Exercises []curatedExercise `json:"exercises"`
	}
	if err := json.Unmarshal(raw, &curated); err != nil {
		return fmt.Errorf("exercise library: curated data: %w", err)
	}
	layer := sharedLayer{byKey: map[string]int{}}
	for _, c := range curated.Exercises {
		primary, secondary, err := validMuscleLists(c.Primary, c.Secondary)
		if err != nil {
			return fmt.Errorf("exercise library: curated %q: %w", c.Name, err)
		}
		name := ExerciseName(c.Name)
		key := FoldExerciseName(name)
		if key == "" || c.Reason == "" {
			return fmt.Errorf("exercise library: curated entry %q needs a name and a reason", c.Name)
		}
		if _, dup := layer.byKey[key]; dup {
			return fmt.Errorf("exercise library: curated name %q appears twice", c.Name)
		}
		layer.byKey[key] = len(layer.entries)
		layer.entries = append(layer.entries, groupEntry(name, LayerCurated, primary, secondary))
	}
	l.public, l.curated = public, layer
	return nil
}

// publicEntry turns an upstream exercise into an entry. A group reached by two
// upstream names keeps its higher credit; an uncertain name is kept as is.
func publicEntry(e PublicExercise) LibraryEntry {
	entry := LibraryEntry{Name: e.Name, Layer: LayerPublic, SourceID: e.ID, Equipment: e.Equipment, Category: e.Category, Muscles: []LibraryMuscle{}}
	seen := map[string]bool{}
	add := func(names []string, role string) {
		for _, name := range names {
			m, known := PublicMuscleMap[name]
			if !known {
				// A name the table does not know is never guessed.
				m = PublicMuscle{Uncertain: true}
			}
			if !m.Uncertain {
				if seen[m.MuscleGroup] {
					continue
				}
				seen[m.MuscleGroup] = true
			}
			lm := LibraryMuscle{MuscleGroup: m.MuscleGroup, Role: role, Credit: roleCredit(role), Uncertain: m.Uncertain, SourceMuscle: name}
			if m.Uncertain {
				lm.Candidates = append([]string{}, m.Candidates...)
				entry.Uncertain = true
			}
			entry.Muscles = append(entry.Muscles, lm)
		}
	}
	add(e.PrimaryMuscles, RolePrimary)
	add(e.SecondaryMuscles, RoleSecondary)
	return entry
}

// groupEntry is an entry whose muscles are already MuscleGroups: an own or a
// curated one. Nothing in it is uncertain.
func groupEntry(name, layer string, primary, secondary []string) LibraryEntry {
	entry := LibraryEntry{Name: name, Layer: layer, Muscles: make([]LibraryMuscle, 0, len(primary)+len(secondary))}
	for _, g := range primary {
		entry.Muscles = append(entry.Muscles, LibraryMuscle{MuscleGroup: g, Role: RolePrimary, Credit: PrimaryCredit})
	}
	for _, g := range secondary {
		entry.Muscles = append(entry.Muscles, LibraryMuscle{MuscleGroup: g, Role: RoleSecondary, Credit: SecondaryCredit})
	}
	return entry
}

func roleCredit(role string) float64 {
	if role == RolePrimary {
		return PrimaryCredit
	}
	return SecondaryCredit
}

// PublicLibraryInfo is the vendored dataset's provenance and size, for docs
// and tests.
func PublicLibraryInfo() (PublicDataset, error) {
	l := sharedLayers()
	info := l.dataset
	info.Exercises = nil
	return info, l.err
}

// PublicLibraryEntries is every public entry as the library resolves it,
// sorted by source id.
func PublicLibraryEntries() ([]LibraryEntry, error) {
	l := sharedLayers()
	out := append([]LibraryEntry{}, l.public.entries...)
	sort.Slice(out, func(i, j int) bool { return out[i].SourceID < out[j].SourceID })
	return out, l.err
}
