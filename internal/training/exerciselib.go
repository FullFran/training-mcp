package training

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode"
)

// Exercise library (#137). An exercise name resolves to an entry with primary
// and secondary muscles, looked up in four places, first match wins:
//
//  1. the person's aliases, each pointing a logged name at an entry;
//  2. the person's own entries;
//  3. the curated layer, hand-checked entries kept in the repository;
//  4. the public layer, free-exercise-db vendored at a pinned commit.
//
// Aliases and own entries belong to one person, so one person's corrections
// never change what another sees. Correcting a curated or public entry writes
// an own entry under the same name (copy on write), which wins from then on.
// The set's equipment is not part of it: the library describes the exercise,
// not the machine.

// Layers an entry comes from.
const (
	LayerOwn     = "own"
	LayerCurated = "curated"
	LayerPublic  = "public"
)

// How a name matched: through an alias, as the entry's name (case and spacing
// aside), or only once accents and punctuation are folded as well.
const (
	MatchAlias      = "alias"
	MatchExact      = "exact"
	MatchNormalized = "normalized"
)

// Muscle roles and the credit a working set gives each (Pelland et al. 2025).
const (
	RolePrimary     = "primary"
	RoleSecondary   = "secondary"
	PrimaryCredit   = 1.0
	SecondaryCredit = 0.5
)

// Why an exercise needs mapping.
const (
	NeedNotFound  = "not_found"
	NeedUncertain = "uncertain"
)

const (
	maxLibraryNameLen  = 120
	maxLibraryMuscles  = 15 // len(MuscleGroups): every group at most once
	maxLookupCandidate = 10
	needCandidates     = 3
)

// ErrNoExerciseLibrary is a library write on a store that keeps no own
// entries or aliases.
var ErrNoExerciseLibrary = errors.New("this store keeps no exercise library")

// LibraryMuscle is one muscle of an entry. An uncertain muscle comes from a
// public name with no one-to-one group: MuscleGroup is empty, SourceMuscle is
// the public name and Candidates the groups it could mean.
type LibraryMuscle struct {
	MuscleGroup  string   `json:"muscle_group,omitempty"`
	Role         string   `json:"role"`
	Credit       float64  `json:"credit"`
	Uncertain    bool     `json:"uncertain,omitempty"`
	SourceMuscle string   `json:"source_muscle,omitempty"`
	Candidates   []string `json:"candidates,omitempty"`
}

// LibraryEntry is what the library knows about one exercise.
type LibraryEntry struct {
	Name  string `json:"name"`
	Layer string `json:"layer"`
	// SourceID is the public dataset's id of a public entry.
	SourceID string `json:"source_id,omitempty"`
	// BasedOn names the curated or public entry an own entry corrected, as
	// "curated:<name>" or "public:<id>".
	BasedOn   string          `json:"based_on,omitempty"`
	Equipment string          `json:"equipment,omitempty"`
	Category  string          `json:"category,omitempty"`
	Muscles   []LibraryMuscle `json:"muscles"`
	// Uncertain says at least one muscle is uncertain.
	Uncertain bool `json:"uncertain"`
}

// LibraryCandidate is an entry a missed name may mean.
type LibraryCandidate struct {
	Name     string `json:"name"`
	Layer    string `json:"layer"`
	SourceID string `json:"source_id,omitempty"`
}

// ExerciseLookup is the answer to one name.
type ExerciseLookup struct {
	Query string        `json:"query"`
	Found bool          `json:"found"`
	Entry *LibraryEntry `json:"entry,omitempty"`
	Layer string        `json:"layer,omitempty"`
	Match string        `json:"match,omitempty"`
	// Alias is the person's alias that matched, when Match is alias.
	Alias string `json:"alias,omitempty"`
	// Candidates are entries a missed name may mean, best first.
	Candidates []LibraryCandidate `json:"candidates"`
}

// OwnExerciseEntry is one person's entry as stored. Key is the folded name,
// unique per person.
type OwnExerciseEntry struct {
	Key       string
	Name      string
	Primary   []string
	Secondary []string
	BasedOn   string
}

// ExerciseAlias points a logged name at an entry. Target is the entry's name,
// resolved through the own, curated and public layers each time, so an alias
// to a public entry follows it once the person corrects it. Key is the folded
// alias, unique per person.
type ExerciseAlias struct {
	Key    string `json:"-"`
	Alias  string `json:"alias"`
	Target string `json:"target"`
}

// ExerciseVolume is the working sets of one exercise in one training week,
// with the single group exercise_groups gives it ("" for none).
type ExerciseVolume struct {
	WeekStart   string
	Exercise    string
	MuscleGroup string
	TotalSI     float64
	Sets        int
}

// ExerciseLibraryStore is the part of a store that keeps a person's library.
// A store without it still looks names up in the shared layers; it cannot
// save entries or aliases, and its volume reads carry no fractional view.
type ExerciseLibraryStore interface {
	OwnExerciseEntries(context.Context) ([]OwnExerciseEntry, error)
	// SaveOwnExerciseEntry adds the entry or replaces the one with its Key.
	SaveOwnExerciseEntry(context.Context, OwnExerciseEntry) error
	ExerciseAliases(context.Context) ([]ExerciseAlias, error)
	// SetExerciseAlias adds the alias or replaces the one with its Key.
	SetExerciseAlias(context.Context, ExerciseAlias) error
	// ExerciseVolume is the person's working sets per week and exercise.
	ExerciseVolume(context.Context, ListFilter) ([]ExerciseVolume, error)
}

// ExerciseName is how the library spells a name: lower case, spaces trimmed
// and collapsed. Two names equal after it match exactly.
func ExerciseName(v string) string {
	return strings.Join(strings.Fields(strings.ToLower(v)), " ")
}

// accentFold maps the accented Latin letters of Spanish and its neighbours to
// their base letter, so "Press Banca", "press bánca" and "PRESS  BANCA" fold
// to one name.
var accentFold = map[rune]rune{
	'á': 'a', 'à': 'a', 'â': 'a', 'ä': 'a', 'ã': 'a', 'å': 'a',
	'é': 'e', 'è': 'e', 'ê': 'e', 'ë': 'e',
	'í': 'i', 'ì': 'i', 'î': 'i', 'ï': 'i',
	'ó': 'o', 'ò': 'o', 'ô': 'o', 'ö': 'o', 'õ': 'o',
	'ú': 'u', 'ù': 'u', 'û': 'u', 'ü': 'u',
	'ñ': 'n', 'ç': 'c', 'ý': 'y', 'ÿ': 'y',
}

// FoldExerciseName is the key names are matched by: ExerciseName with accents
// folded and every character that is not a letter or digit read as a space.
func FoldExerciseName(v string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(v) {
		if base, ok := accentFold[r]; ok {
			r = base
		}
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			r = ' '
		}
		b.WriteRune(r)
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// personalLibrary is one person's aliases and own entries beside the shared
// layers, read once per call.
type personalLibrary struct {
	own     map[string]LibraryEntry
	aliases map[string]ExerciseAlias
	shared  *sharedLibrary
}

func (s *Service) library(ctx context.Context) (personalLibrary, error) {
	shared := sharedLayers()
	if shared.err != nil {
		return personalLibrary{}, shared.err
	}
	lib := personalLibrary{own: map[string]LibraryEntry{}, aliases: map[string]ExerciseAlias{}, shared: shared}
	ls, ok := s.store.(ExerciseLibraryStore)
	if !ok {
		return lib, nil
	}
	entries, err := ls.OwnExerciseEntries(ctx)
	if err != nil {
		return lib, err
	}
	for _, e := range entries {
		entry := groupEntry(e.Name, LayerOwn, e.Primary, e.Secondary)
		entry.BasedOn = e.BasedOn
		lib.own[e.Key] = entry
	}
	aliases, err := ls.ExerciseAliases(ctx)
	if err != nil {
		return lib, err
	}
	for _, a := range aliases {
		lib.aliases[a.Key] = a
	}
	return lib, nil
}

// entry resolves a name in the own, curated and public layers, aliases aside.
func (l personalLibrary) entry(name string) (LibraryEntry, string, bool) {
	key := FoldExerciseName(name)
	match := func(e LibraryEntry) string {
		if ExerciseName(e.Name) == ExerciseName(name) {
			return MatchExact
		}
		return MatchNormalized
	}
	if e, ok := l.own[key]; ok {
		return e, match(e), true
	}
	if e, ok := l.shared.curated.find(key); ok {
		return e, match(e), true
	}
	if e, ok := l.shared.public.find(key); ok {
		return e, match(e), true
	}
	return LibraryEntry{}, "", false
}

// sharedEntry resolves a name in the curated and public layers only: the
// entry an own entry of that name corrects.
func (l personalLibrary) sharedEntry(name string) (LibraryEntry, bool) {
	key := FoldExerciseName(name)
	if e, ok := l.shared.curated.find(key); ok {
		return e, true
	}
	return l.shared.public.find(key)
}

// resolve finds a name through every layer, an alias first, with no
// candidates on a miss.
func (l personalLibrary) resolve(name string) ExerciseLookup {
	out := ExerciseLookup{Query: ExerciseName(name), Candidates: []LibraryCandidate{}}
	if a, ok := l.aliases[FoldExerciseName(name)]; ok {
		// An alias whose entry no longer resolves is skipped, not trusted.
		if e, _, found := l.entry(a.Target); found {
			out.Found, out.Entry, out.Layer, out.Match, out.Alias = true, &e, e.Layer, MatchAlias, a.Alias
			return out
		}
	}
	if e, match, found := l.entry(name); found {
		out.Found, out.Entry, out.Layer, out.Match = true, &e, e.Layer, match
	}
	return out
}

// lookup is resolve with candidates on a miss.
func (l personalLibrary) lookup(name string) ExerciseLookup {
	out := l.resolve(name)
	if !out.Found {
		out.Candidates = l.candidates(name, maxLookupCandidate)
	}
	return out
}

// candidates ranks entries by how many words of the name they share, as whole
// words or prefixes of at least three letters. Ties go to the earlier layer,
// then the shorter name. It never picks one: the person confirms a match.
func (l personalLibrary) candidates(name string, limit int) []LibraryCandidate {
	words := strings.Fields(FoldExerciseName(name))
	type scored struct {
		c     LibraryCandidate
		score int
		layer int
	}
	var all []scored
	consider := func(e LibraryEntry, layer int) {
		entryWords := strings.Fields(FoldExerciseName(e.Name))
		score := 0
		for _, w := range words {
			for _, ew := range entryWords {
				if w == ew || (len(w) >= 3 && strings.HasPrefix(ew, w)) || (len(ew) >= 3 && strings.HasPrefix(w, ew)) {
					score++
					break
				}
			}
		}
		if score > 0 {
			all = append(all, scored{LibraryCandidate{Name: e.Name, Layer: e.Layer, SourceID: e.SourceID}, score, layer})
		}
	}
	for _, e := range l.own {
		consider(e, 0)
	}
	for _, e := range l.shared.curated.entries {
		consider(e, 1)
	}
	for _, e := range l.shared.public.entries {
		consider(e, 2)
	}
	sort.Slice(all, func(i, j int) bool {
		a, b := all[i], all[j]
		if a.score != b.score {
			return a.score > b.score
		}
		if a.layer != b.layer {
			return a.layer < b.layer
		}
		if len(a.c.Name) != len(b.c.Name) {
			return len(a.c.Name) < len(b.c.Name)
		}
		return a.c.Name < b.c.Name
	})
	out := []LibraryCandidate{}
	for i := 0; i < len(all) && i < limit; i++ {
		out = append(out, all[i].c)
	}
	return out
}

func validLibraryName(name string) (string, bool) {
	name = ExerciseName(name)
	return name, FoldExerciseName(name) != "" && len(name) <= maxLibraryNameLen
}

// LookupExercise resolves a name: the person's alias, their own entry, the
// curated layer, then the public one. A miss lists candidates instead.
func (s *Service) LookupExercise(ctx context.Context, name string) (ExerciseLookup, error) {
	if _, ok := validLibraryName(name); !ok {
		return ExerciseLookup{}, ErrValidation
	}
	lib, err := s.library(ctx)
	if err != nil {
		return ExerciseLookup{}, err
	}
	return lib.lookup(name), nil
}

// LibraryEntryInput is an own entry to save: muscles as MuscleGroups.
type LibraryEntryInput struct {
	Name      string
	Primary   []string
	Secondary []string
}

// validMuscleLists checks an entry's muscles: at least one primary, every one
// a MuscleGroup, none twice and none in both lists.
func validMuscleLists(primary, secondary []string) ([]string, []string, error) {
	if len(primary) == 0 || len(primary)+len(secondary) > maxLibraryMuscles {
		return nil, nil, fmt.Errorf("%w: give 1 to %d muscle groups, at least one primary", ErrValidation, maxLibraryMuscles)
	}
	seen := map[string]bool{}
	clean := func(groups []string) ([]string, error) {
		out := make([]string, 0, len(groups))
		for _, g := range groups {
			g = strings.ToLower(strings.TrimSpace(g))
			if !ValidMuscleGroup(g) {
				return nil, fmt.Errorf("%w: %q is not a muscle group", ErrValidation, g)
			}
			if seen[g] {
				return nil, fmt.Errorf("%w: %q is listed twice", ErrValidation, g)
			}
			seen[g] = true
			out = append(out, g)
		}
		return out, nil
	}
	p, err := clean(primary)
	if err != nil {
		return nil, nil, err
	}
	sec, err := clean(secondary)
	if err != nil {
		return nil, nil, err
	}
	return p, sec, nil
}

// SaveExerciseEntry writes the person's own entry for a name, replacing any
// they had under it. Saved under the name of a curated or public entry, it is
// that entry corrected (copy on write) and wins over it from then on, for this
// person only.
func (s *Service) SaveExerciseEntry(ctx context.Context, in LibraryEntryInput) (LibraryEntry, error) {
	name, ok := validLibraryName(in.Name)
	if !ok {
		return LibraryEntry{}, ErrValidation
	}
	primary, secondary, err := validMuscleLists(in.Primary, in.Secondary)
	if err != nil {
		return LibraryEntry{}, err
	}
	ls, ok := s.store.(ExerciseLibraryStore)
	if !ok {
		return LibraryEntry{}, ErrNoExerciseLibrary
	}
	lib, err := s.library(ctx)
	if err != nil {
		return LibraryEntry{}, err
	}
	stored := OwnExerciseEntry{Key: FoldExerciseName(name), Name: name, Primary: primary, Secondary: secondary}
	if base, found := lib.sharedEntry(name); found {
		stored.BasedOn = base.Layer + ":" + base.Name
		if base.SourceID != "" {
			stored.BasedOn = base.Layer + ":" + base.SourceID
		}
	}
	if err := ls.SaveOwnExerciseEntry(ctx, stored); err != nil {
		return LibraryEntry{}, err
	}
	entry := groupEntry(name, LayerOwn, primary, secondary)
	entry.BasedOn = stored.BasedOn
	return entry, nil
}

// AliasResult is an alias as saved, with the entry it resolves to now.
type AliasResult struct {
	Alias string       `json:"alias"`
	Entry LibraryEntry `json:"entry"`
	Match string       `json:"match"`
}

// SetExerciseAlias points a logged name at an entry of the own, curated or
// public layer, replacing any alias the person had for that name. The target
// must resolve now; an alias never points at another alias.
func (s *Service) SetExerciseAlias(ctx context.Context, alias, target string) (AliasResult, error) {
	alias, ok := validLibraryName(alias)
	if !ok {
		return AliasResult{}, ErrValidation
	}
	if _, ok := validLibraryName(target); !ok {
		return AliasResult{}, ErrValidation
	}
	ls, ok := s.store.(ExerciseLibraryStore)
	if !ok {
		return AliasResult{}, ErrNoExerciseLibrary
	}
	lib, err := s.library(ctx)
	if err != nil {
		return AliasResult{}, err
	}
	entry, match, found := lib.entry(target)
	if !found {
		return AliasResult{}, fmt.Errorf("%w: no library entry is named %q; look it up first", ErrValidation, ExerciseName(target))
	}
	if FoldExerciseName(entry.Name) == FoldExerciseName(alias) {
		return AliasResult{}, fmt.Errorf("%w: %q already names that entry, so it needs no alias", ErrValidation, alias)
	}
	if err := ls.SetExerciseAlias(ctx, ExerciseAlias{Key: FoldExerciseName(alias), Alias: alias, Target: entry.Name}); err != nil {
		return AliasResult{}, err
	}
	return AliasResult{Alias: alias, Entry: entry, Match: match}, nil
}

// MappingNeed is a logged exercise the library cannot count by muscle role:
// none of its layers has it (not_found), or its entry has uncertain muscles.
// Until mapped it keeps counting through its exercise group, MuscleGroup ("" for
// none), exactly as before.
type MappingNeed struct {
	Exercise    string             `json:"exercise"`
	Sets        int                `json:"sets"`
	MuscleGroup string             `json:"muscle_group"`
	Reason      string             `json:"reason"`
	Entry       *LibraryEntry      `json:"entry,omitempty"`
	Match       string             `json:"match,omitempty"`
	Candidates  []LibraryCandidate `json:"candidates"`
}

// ExercisesNeedingMapping lists the person's logged exercises (working sets)
// with no library entry, then those whose entry has uncertain muscles, each
// most-trained first.
func (s *Service) ExercisesNeedingMapping(ctx context.Context) ([]MappingNeed, error) {
	out := []MappingNeed{}
	ls, ok := s.store.(ExerciseLibraryStore)
	if !ok {
		return out, nil
	}
	lib, err := s.library(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := ls.ExerciseVolume(ctx, ListFilter{})
	if err != nil {
		return nil, err
	}
	sets := map[string]int{}
	groups := map[string]string{}
	for _, r := range rows {
		sets[r.Exercise] += r.Sets
		groups[r.Exercise] = r.MuscleGroup
	}
	for exercise, n := range sets {
		found := lib.resolve(exercise)
		need := MappingNeed{Exercise: exercise, Sets: n, MuscleGroup: groups[exercise], Candidates: []LibraryCandidate{}}
		switch {
		case !found.Found:
			need.Reason = NeedNotFound
			need.Candidates = lib.candidates(exercise, needCandidates)
		case found.Entry.Uncertain:
			need.Reason, need.Entry, need.Match = NeedUncertain, found.Entry, found.Match
		default:
			continue
		}
		out = append(out, need)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Reason != b.Reason {
			return a.Reason == NeedNotFound
		}
		if a.Sets != b.Sets {
			return a.Sets > b.Sets
		}
		return a.Exercise < b.Exercise
	})
	return out, nil
}
