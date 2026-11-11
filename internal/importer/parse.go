// Package importer reads a training history exported by another logger (Hevy,
// Strong) and plans its import. Everything here is deterministic: no model reads
// the file, and the same bytes always give the same sets and keys.
package importer

import (
	"bytes"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/fullfran/training-mcp/internal/training"
)

const (
	// MaxBytes keeps a file, with the form around it, under the server's 1 MiB
	// request limit. Around eight thousand sets fit.
	MaxBytes = 960 << 10
	// MaxRows caps the rows read from one file, sets or not.
	MaxRows = training.MaxImportSets
)

var (
	// ErrFormat means the file is not a Hevy or Strong workout export.
	ErrFormat = errors.New("not a Hevy or Strong workout export")
	// ErrTooLarge means the file has more bytes or rows than one import takes.
	ErrTooLarge = errors.New("file too large to import at once")
)

// Why a row of the file was not taken as a set.
const (
	SkipWarmup     = "warmup"
	SkipNoLoad     = "no_load"
	SkipNotASet    = "not_a_set"
	SkipUnreadable = "unreadable"
)

const lbToKG = 0.45359237

// Options are what the file cannot say for itself.
type Options struct {
	// StrongUnit is the unit Strong was set to: "kg" (the default) or "lb".
	// Strong's classic export writes weights without one.
	StrongUnit string
}

// Set is one working set as the app recorded it. RPE is 0 when none was stated.
type Set struct {
	// Exercise is the app's own name for it, before mapping.
	Exercise  string
	WeightKG  float64
	Reps      int
	RPE       float64
	Technique string
	// Key identifies the set across exports of the same history.
	Key string
}

// Workout is one session of the source app.
type Workout struct {
	Date      string
	StartedAt string
	Title     string
	Notes     string
	Sets      []Set
}

// File is a parsed export: its workouts oldest first, each with at least one set.
type File struct {
	Source   string
	Hash     string
	Rows     int
	Workouts []Workout
	// Skipped counts the rows not taken as sets, by reason.
	Skipped map[string]int
}

// Parse reads a Hevy or Strong workout export.
func Parse(data []byte, opts Options) (File, error) {
	if len(data) > MaxBytes {
		return File{}, ErrTooLarge
	}
	unit := opts.StrongUnit
	if unit == "" {
		unit = "kg"
	}
	if unit != "kg" && unit != "lb" {
		return File{}, fmt.Errorf("%w: unit %q", ErrFormat, unit)
	}
	sum := sha256.Sum256(data)
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	r := csv.NewReader(bytes.NewReader(data))
	r.Comma = delimiter(data)
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	r.ReuseRecord = false
	header, err := r.Read()
	if err != nil {
		return File{}, fmt.Errorf("%w: no header", ErrFormat)
	}
	cols := map[string]int{}
	for i, h := range header {
		cols[headerKey(h)] = i
	}
	var reader rowReader
	switch {
	case has(cols, "exercisetitle", "starttime"):
		reader, err = hevyReader(cols)
	case has(cols, "exercisename", "setorder", "date"):
		reader, err = strongReader(cols, unit)
	default:
		err = ErrFormat
	}
	if err != nil {
		return File{}, err
	}

	f := File{Source: reader.source, Hash: hex.EncodeToString(sum[:]), Skipped: map[string]int{}}
	b := newBuilder(reader.source)
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return File{}, fmt.Errorf("%w: %v", ErrFormat, err)
		}
		if blank(rec) {
			continue
		}
		f.Rows++
		if f.Rows > MaxRows {
			return File{}, ErrTooLarge
		}
		row, skip := reader.read(rec)
		if skip == SkipUnreadable {
			f.Skipped[skip]++
			continue
		}
		b.add(row, skip, f.Skipped)
	}
	if f.Rows == 0 {
		return File{}, fmt.Errorf("%w: no rows", ErrFormat)
	}
	f.Workouts = b.workouts()
	return f, nil
}

// row is one line of either export, read into common terms.
type row struct {
	started     time.Time
	title       string
	workoutNote string
	exercise    string
	setNote     string
	set         Set
}

type rowReader struct {
	source string
	// read returns the row and, when it is not a set to import, why.
	read func([]string) (row, string)
}

func hevyReader(cols map[string]int) (rowReader, error) {
	weight, factor := "weightkg", 1.0
	if _, ok := cols["weightlbs"]; ok {
		weight, factor = "weightlbs", lbToKG
	}
	if !has(cols, "title", "settype", weight, "reps") {
		return rowReader{}, fmt.Errorf("%w: missing Hevy columns", ErrFormat)
	}
	return rowReader{source: training.ImportSourceHevy, read: func(rec []string) (row, string) {
		get := getter(cols, rec)
		started, ok := parseWhen(get("starttime"))
		if !ok {
			return row{}, SkipUnreadable
		}
		out := row{started: started, title: get("title"), workoutNote: unescape(get("description")),
			exercise: get("exercisetitle"), setNote: unescape(get("exercisenotes"))}
		set, ok := measure(get(weight), get("reps"), get("rpe"), factor)
		if !ok {
			return out, SkipUnreadable
		}
		switch strings.ToLower(get("settype")) {
		case "warmup":
			return out, SkipWarmup
		case "dropset":
			set.Technique = "drop set"
		}
		out.set = set
		return out, loadSkip(set)
	}}, nil
}

func strongReader(cols map[string]int, unit string) (rowReader, error) {
	factor := 1.0
	if unit == "lb" {
		factor = lbToKG
	}
	weight := "weight"
	switch {
	case has(cols, "weightkg"):
		weight, factor = "weightkg", 1
	case has(cols, "weightlbs"):
		weight, factor = "weightlbs", lbToKG
	case has(cols, "weightlb"):
		weight, factor = "weightlb", lbToKG
	}
	if !has(cols, weight, "reps") {
		return rowReader{}, fmt.Errorf("%w: missing Strong columns", ErrFormat)
	}
	return rowReader{source: training.ImportSourceStrong, read: func(rec []string) (row, string) {
		get := getter(cols, rec)
		started, ok := parseWhen(get("date"))
		if !ok {
			return row{}, SkipUnreadable
		}
		out := row{started: started, title: get("workoutname"), workoutNote: get("workoutnotes"),
			exercise: get("exercisename"), setNote: get("notes")}
		order := strings.ToLower(get("setorder"))
		switch order {
		case "rest timer", "note":
			return out, SkipNotASet
		case "w":
			return out, SkipWarmup
		}
		set, ok := measure(get(weight), get("reps"), get("rpe"), factor)
		if !ok {
			return out, SkipUnreadable
		}
		if order == "d" {
			set.Technique = "drop set"
		}
		out.set = set
		return out, loadSkip(set)
	}}, nil
}

// measure reads a set's numbers. Empty cells are zero; an RPE outside 1 to 10
// was not a real rating and counts as none.
func measure(weight, reps, rpe string, factor float64) (Set, bool) {
	var s Set
	var err error
	if s.WeightKG, err = number(weight); err != nil {
		return s, false
	}
	r, err := number(reps)
	if err != nil || r != math.Trunc(r) {
		return s, false
	}
	s.Reps = int(r)
	s.WeightKG = math.Round(s.WeightKG*factor*100) / 100
	if s.RPE, err = number(rpe); err != nil || s.RPE < 1 || s.RPE > 10 {
		s.RPE = 0
	}
	return s, true
}

func loadSkip(s Set) string {
	if s.WeightKG <= 0 || s.Reps <= 0 {
		return SkipNoLoad
	}
	return ""
}

// number reads a cell, accepting a decimal comma.
func number(v string) (float64, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, nil
	}
	f, err := strconv.ParseFloat(strings.Replace(v, ",", ".", 1), 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f < 0 {
		return 0, errors.New("not a number")
	}
	return f, nil
}

// spanishMonths lets a Hevy app set to Spanish be read like one set to English.
var spanishMonths = strings.NewReplacer(" ene ", " Jan ", " abr ", " Apr ", " ago ", " Aug ", " dic ", " Dec ",
	" ene. ", " Jan ", " feb. ", " Feb ", " mar. ", " Mar ", " abr. ", " Apr ", " may. ", " May ", " jun. ", " Jun ",
	" jul. ", " Jul ", " ago. ", " Aug ", " sept. ", " Sep ", " sept ", " Sep ", " sep. ", " Sep ", " oct. ", " Oct ", " nov. ", " Nov ", " dic. ", " Dec ")

var whenLayouts = []string{
	"2 Jan 2006, 15:04", "2 Jan 2006 15:04", "2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02T15:04:05Z07:00", "2006-01-02T15:04:05", "2006-01-02",
}

// parseWhen reads a workout's start in the app's own clock, which is kept as is.
func parseWhen(v string) (time.Time, bool) {
	v = strings.TrimSpace(v)
	v = strings.TrimSpace(spanishMonths.Replace(" " + v + " "))
	for _, layout := range whenLayouts {
		if t, err := time.Parse(layout, v); err == nil {
			if t.Year() < 1970 || t.Year() > 2200 {
				return time.Time{}, false
			}
			return t, true
		}
	}
	return time.Time{}, false
}

// unescape turns Hevy's written "\n" into a line break.
func unescape(v string) string { return strings.ReplaceAll(strings.TrimSpace(v), `\n`, "\n") }

// headerKey reduces a header to lower-case letters and digits: "Weight (lbs)"
// and "weight_lbs" both become "weightlbs".
func headerKey(h string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(h) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func has(cols map[string]int, keys ...string) bool {
	for _, k := range keys {
		if _, ok := cols[k]; !ok {
			return false
		}
	}
	return true
}

func getter(cols map[string]int, rec []string) func(string) string {
	return func(key string) string {
		i, ok := cols[key]
		if !ok || i >= len(rec) {
			return ""
		}
		return strings.TrimSpace(rec[i])
	}
}

func blank(rec []string) bool {
	for _, v := range rec {
		if strings.TrimSpace(v) != "" {
			return false
		}
	}
	return true
}

// delimiter is whichever of ';' and ',' the header line uses more, quotes aside.
func delimiter(data []byte) rune {
	line := data
	if i := bytes.IndexAny(data, "\r\n"); i >= 0 {
		line = data[:i]
	}
	unquoted := []byte{}
	in := false
	for _, c := range line {
		if c == '"' {
			in = !in
			continue
		}
		if !in {
			unquoted = append(unquoted, c)
		}
	}
	if bytes.Count(unquoted, []byte(";")) > bytes.Count(unquoted, []byte(",")) {
		return ';'
	}
	return ','
}

// builder groups rows into workouts and gives each set its key.
type builder struct {
	source string
	byID   map[string]*Workout
	order  []string
	// seen counts the rows of each exercise of a workout, sets or not, so a
	// set's ordinal does not change when what counts as a set does.
	seen  map[string]int
	notes map[string]map[string]bool
}

func newBuilder(source string) *builder {
	return &builder{source: source, byID: map[string]*Workout{}, seen: map[string]int{}, notes: map[string]map[string]bool{}}
}

func (b *builder) add(r row, skip string, skipped map[string]int) {
	started := r.started.Format("2006-01-02 15:04:05")
	id := started + "\x00" + r.title
	w, ok := b.byID[id]
	if !ok {
		w = &Workout{Date: r.started.Format("2006-01-02"), StartedAt: started, Title: r.title}
		b.byID[id] = w
		b.order = append(b.order, id)
		b.notes[id] = map[string]bool{}
	}
	b.note(id, w, r.workoutNote)
	if r.setNote != "" && r.exercise != "" {
		b.note(id, w, r.exercise+": "+r.setNote)
	}
	ordinal := b.seen[id+"\x00"+r.exercise]
	b.seen[id+"\x00"+r.exercise]++
	if skip == "" && r.exercise == "" {
		skip = SkipUnreadable
	}
	if skip != "" {
		skipped[skip]++
		return
	}
	set := r.set
	set.Exercise = r.exercise
	sum := sha256.Sum256([]byte(strings.Join([]string{b.source, started, r.title, r.exercise, strconv.Itoa(ordinal)}, "\x00")))
	set.Key = b.source + ":" + hex.EncodeToString(sum[:])
	w.Sets = append(w.Sets, set)
}

// note adds a line to a workout's notes once.
func (b *builder) note(id string, w *Workout, line string) {
	if line == "" || b.notes[id][line] {
		return
	}
	b.notes[id][line] = true
	if w.Notes != "" {
		w.Notes += "\n"
	}
	w.Notes += line
}

func (b *builder) workouts() []Workout {
	out := []Workout{}
	for _, id := range b.order {
		if w := b.byID[id]; len(w.Sets) > 0 {
			out = append(out, *w)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].StartedAt < out[j].StartedAt })
	return out
}
