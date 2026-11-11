package importer

import (
	"context"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/fullfran/training-mcp/internal/training"
)

const (
	maxNameLen  = 120
	maxNotesLen = 4000
)

// NameChoice is how one of the app's exercise names will be recorded.
type NameChoice struct {
	// Source is the app's name; Exercise the name it gets here.
	Source   string
	Exercise string
	// Known means Exercise is a name the person already uses.
	Known bool
	// Remembered means Exercise comes from an earlier import's choice.
	Remembered bool
	// Suggestion is a close name the person already uses, offered when the
	// name is not one of theirs.
	Suggestion string
	// Sets is how many of the file's sets carry the name.
	Sets int
}

// Preview is what an import would do, computed without writing anything.
type Preview struct {
	Source, Hash, FileName string
	// Sessions, Sets and Exercises count only what is new; Imputed counts the new
	// sets whose RPE was not stated and will be stored as a placeholder.
	Sessions, Sets, Imputed, Exercises int
	// Unmapped counts names that would be recorded as new exercises.
	Unmapped int
	// AlreadyImported are sets an earlier import recorded; InHistory are sets
	// the history already holds, logged some other way. Neither is imported.
	AlreadyImported, InHistory int
	Skipped                    map[string]int
	// From and To are the dates the file covers.
	From, To string
	Names    []NameChoice
	// Catalogue is every name the person uses, offered when choosing.
	Catalogue []string
	// Record is what confirming would store.
	Record training.ImportRecord
}

// Plan previews importing f for the person svc belongs to. chosen holds the
// person's own name for some of the app's names; the rest are resolved from an
// earlier import's choice, then an exact match with a name already in use, then
// the app's name itself.
func Plan(ctx context.Context, svc *training.Service, f File, chosen map[string]string, fileName string) (Preview, error) {
	p := Preview{Source: f.Source, Hash: f.Hash, FileName: fileName, Skipped: f.Skipped}
	catalogue, err := svc.ExerciseNames(ctx)
	if err != nil {
		return Preview{}, err
	}
	p.Catalogue = append([]string{}, catalogue...)
	remembered, err := svc.ImportMappings(ctx, f.Source)
	if err != nil {
		return Preview{}, err
	}
	known := map[string]string{}
	for _, name := range catalogue {
		known[fold(name)] = name
	}

	var keys []string
	counts := map[string]int{}
	for _, w := range f.Workouts {
		if p.From == "" || w.Date < p.From {
			p.From = w.Date
		}
		if w.Date > p.To {
			p.To = w.Date
		}
		for _, s := range w.Sets {
			keys = append(keys, s.Key)
			counts[s.Exercise]++
		}
	}
	resolved := map[string]string{}
	for source, n := range counts {
		c := NameChoice{Source: source, Sets: n}
		switch pick := clip(strings.ToLower(strings.TrimSpace(chosen[source])), maxNameLen); {
		case pick != "":
			c.Exercise = pick
		case remembered[source] != "":
			c.Exercise, c.Remembered = remembered[source], true
		default:
			c.Exercise = clip(strings.ToLower(strings.TrimSpace(source)), maxNameLen)
			if name, ok := known[fold(c.Exercise)]; ok {
				c.Exercise = name
			}
		}
		_, c.Known = known[fold(c.Exercise)]
		if !c.Known {
			c.Suggestion = suggest(c.Exercise, catalogue)
			p.Unmapped++
		}
		resolved[source] = c.Exercise
		p.Names = append(p.Names, c)
	}
	sort.Slice(p.Names, func(i, j int) bool { return p.Names[i].Source < p.Names[j].Source })

	imported, err := svc.ImportedSetKeys(ctx, keys)
	if err != nil {
		return Preview{}, err
	}
	history := map[string]int{}
	if p.From != "" {
		existing, err := svc.DaySets(ctx, p.From, p.To)
		if err != nil {
			return Preview{}, err
		}
		for _, d := range existing {
			history[setID(d.Date, d.Exercise, d.WeightKG, d.Reps)]++
		}
	}

	p.Record = training.ImportRecord{Source: f.Source, FileName: fileName, ContentHash: f.Hash, Mappings: resolved}
	exercises := map[string]bool{}
	for _, w := range f.Workouts {
		out := training.ImportedWorkout{Date: w.Date, StartedAt: w.StartedAt, Notes: workoutNotes(w)}
		for _, s := range w.Sets {
			exercise := resolved[s.Exercise]
			id := setID(w.Date, exercise, s.WeightKG, s.Reps)
			// A set an earlier import recorded is in the history too: it uses up its
			// match there, so it cannot hide a genuinely new set just like it.
			matched := history[id] > 0
			if matched {
				history[id]--
			}
			switch {
			case imported[s.Key]:
				p.AlreadyImported++
				continue
			case matched:
				p.InHistory++
				continue
			}
			set := training.ImportedSet{Key: s.Key, Exercise: exercise, WeightKG: s.WeightKG, Reps: s.Reps, RPE: s.RPE, Technique: s.Technique}
			if set.RPE == 0 {
				set.RPE, set.RPEImputed = training.ImputedRPE, true
				p.Imputed++
			}
			out.Sets = append(out.Sets, set)
			exercises[exercise] = true
		}
		if len(out.Sets) > 0 {
			p.Record.Workouts = append(p.Record.Workouts, out)
			p.Sessions++
			p.Sets += len(out.Sets)
		}
	}
	p.Exercises = len(exercises)
	return p, nil
}

// Apply plans the import again, so what is stored is exactly what the plan
// says now, and stores it all or nothing. A plan with nothing new stores nothing.
func Apply(ctx context.Context, svc *training.Service, f File, chosen map[string]string, fileName string) (Preview, training.ImportSummary, error) {
	p, err := Plan(ctx, svc, f, chosen, fileName)
	if err != nil || p.Sets == 0 {
		return p, training.ImportSummary{}, err
	}
	summary, err := svc.ApplyImport(ctx, p.Record)
	return p, summary, err
}

// workoutNotes are the workout's title and notes, as the session's notes.
func workoutNotes(w Workout) string {
	notes := strings.TrimSpace(w.Title)
	if w.Notes != "" {
		if notes != "" {
			notes += "\n"
		}
		notes += w.Notes
	}
	return clip(notes, maxNotesLen)
}

// setID is what makes two recorded sets the same set: day, exercise, load and reps.
func setID(date, exercise string, weight float64, reps int) string {
	return date + "\x00" + exercise + "\x00" + strconv.FormatFloat(math.Round(weight*100)/100, 'f', 2, 64) + "\x00" + strconv.Itoa(reps)
}

// clip cuts v to at most n bytes without splitting a character.
func clip(v string, n int) string {
	if len(v) <= n {
		return v
	}
	v = v[:n]
	for !utf8.ValidString(v) {
		v = v[:len(v)-1]
	}
	return strings.TrimSpace(v)
}

// fold compares names regardless of case and accents: "Remo máquina" matches "remo maquina".
func fold(v string) string {
	return strings.Join(strings.Fields(accents.Replace(strings.ToLower(v))), " ")
}

// accents are the marked letters of Spanish and its neighbours, without them.
var accents = strings.NewReplacer("á", "a", "à", "a", "â", "a", "ä", "a", "é", "e", "è", "e", "ê", "e", "ë", "e",
	"í", "i", "ì", "i", "î", "i", "ï", "i", "ó", "o", "ò", "o", "ô", "o", "ö", "o", "ú", "u", "ù", "u", "û", "u", "ü", "u",
	"ñ", "n", "ç", "c")

// tokens are a name's words without its equipment in brackets or punctuation.
func tokens(v string) []string {
	v = fold(v)
	if i := strings.IndexByte(v, '('); i >= 0 {
		v = v[:i]
	}
	return strings.FieldsFunc(v, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}

// suggest is the name in use sharing most words with name, when they share at
// least half of them; empty when none is that close.
func suggest(name string, catalogue []string) string {
	want := tokens(name)
	if len(want) == 0 {
		return ""
	}
	best, bestScore := "", 0.0
	for _, candidate := range catalogue {
		have := tokens(candidate)
		if len(have) == 0 {
			continue
		}
		shared := 0
		for _, w := range want {
			for _, h := range have {
				if w == h {
					shared++
					break
				}
			}
		}
		score := float64(shared) / float64(len(want)+len(have)-shared)
		if score >= 0.5 && score > bestScore {
			best, bestScore = candidate, score
		}
	}
	return best
}
