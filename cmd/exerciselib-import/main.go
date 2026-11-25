// Command exerciselib-import writes the public layer of the exercise library
// from free-exercise-db's dist/exercises.json, trimmed to the fields the
// library reads, with the upstream commit and the file's SHA-256 recorded.
//
// The downloaded file is untrusted input: it is only parsed as JSON and
// checked, never run. Download it at a pinned commit into an empty directory
// outside the repository, then:
//
//	go run ./cmd/exerciselib-import -in /tmp/x/exercises.json -commit <sha> \
//	  -out internal/training/exerciselibdata/free-exercise-db.json
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/fullfran/training-mcp/internal/training"
)

const (
	repository   = "https://github.com/yuhonas/free-exercise-db"
	upstreamFile = "dist/exercises.json"
	maxNameLen   = 120
	maxUpstream  = 16 << 20
)

var commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

type upstreamExercise struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	PrimaryMuscles   []string `json:"primaryMuscles"`
	SecondaryMuscles []string `json:"secondaryMuscles"`
	Equipment        *string  `json:"equipment"`
	Category         *string  `json:"category"`
}

// trim turns the upstream file into the vendored one. It refuses an exercise
// without an id or a name, a repeated id, and a muscle name that
// training.PublicMuscleMap does not know, so a new upstream name is mapped by
// hand before it is imported.
func trim(upstream []byte, commit string) ([]byte, error) {
	if !commitPattern.MatchString(commit) {
		return nil, errors.New("commit must be a full 40-character lower-case SHA")
	}
	var in []upstreamExercise
	if err := json.Unmarshal(upstream, &in); err != nil {
		return nil, fmt.Errorf("upstream file: %w", err)
	}
	if len(in) == 0 {
		return nil, errors.New("upstream file has no exercises")
	}
	sum := sha256.Sum256(upstream)
	out := training.PublicDataset{
		Source: "free-exercise-db", Repository: repository, License: "Unlicense",
		Commit: commit, UpstreamFile: upstreamFile, UpstreamSHA256: hex.EncodeToString(sum[:]),
	}
	ids := map[string]bool{}
	for _, e := range in {
		if strings.TrimSpace(e.ID) == "" || strings.TrimSpace(e.Name) == "" || len(e.Name) > maxNameLen {
			return nil, fmt.Errorf("exercise %q: needs an id and a name of at most %d bytes", e.ID, maxNameLen)
		}
		if ids[e.ID] {
			return nil, fmt.Errorf("exercise id %q appears twice", e.ID)
		}
		ids[e.ID] = true
		for _, m := range append(append([]string{}, e.PrimaryMuscles...), e.SecondaryMuscles...) {
			if _, known := training.PublicMuscleMap[m]; !known {
				return nil, fmt.Errorf("exercise %q: muscle %q is not in training.PublicMuscleMap", e.ID, m)
			}
		}
		if len(e.PrimaryMuscles) == 0 {
			return nil, fmt.Errorf("exercise %q has no primary muscle", e.ID)
		}
		pe := training.PublicExercise{ID: e.ID, Name: e.Name, PrimaryMuscles: e.PrimaryMuscles, SecondaryMuscles: e.SecondaryMuscles}
		if pe.SecondaryMuscles == nil {
			pe.SecondaryMuscles = []string{}
		}
		if e.Equipment != nil {
			pe.Equipment = *e.Equipment
		}
		if e.Category != nil {
			pe.Category = *e.Category
		}
		out.Exercises = append(out.Exercises, pe)
	}
	sort.Slice(out.Exercises, func(i, j int) bool { return out.Exercises[i].ID < out.Exercises[j].ID })
	return encode(out)
}

// encode writes one exercise per line, so a re-import reviews as a diff of the
// exercises that changed.
func encode(d training.PublicDataset) ([]byte, error) {
	head, err := json.MarshalIndent(struct {
		Source         string `json:"source"`
		Repository     string `json:"repository"`
		License        string `json:"license"`
		Commit         string `json:"commit"`
		UpstreamFile   string `json:"upstream_file"`
		UpstreamSHA256 string `json:"upstream_sha256"`
	}{d.Source, d.Repository, d.License, d.Commit, d.UpstreamFile, d.UpstreamSHA256}, "", "  ")
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	b.Write(bytes.TrimSuffix(head, []byte("\n}")))
	b.WriteString(",\n  \"exercises\": [\n")
	for i, e := range d.Exercises {
		line, err := json.Marshal(e)
		if err != nil {
			return nil, err
		}
		b.WriteString("    ")
		b.Write(line)
		if i < len(d.Exercises)-1 {
			b.WriteString(",")
		}
		b.WriteString("\n")
	}
	b.WriteString("  ]\n}\n")
	if !json.Valid(b.Bytes()) {
		return nil, errors.New("encoded file is not valid JSON")
	}
	return b.Bytes(), nil
}

func main() {
	in := flag.String("in", "", "path to the downloaded dist/exercises.json")
	commit := flag.String("commit", "", "upstream commit the file was downloaded at")
	out := flag.String("out", "", "path of the vendored file to write")
	flag.Parse()
	if *in == "" || *out == "" {
		flag.Usage()
		os.Exit(2)
	}
	info, err := os.Stat(*in)
	if err != nil || info.Size() > maxUpstream {
		fmt.Fprintln(os.Stderr, "exerciselib-import: input missing or larger than 16 MiB")
		os.Exit(1)
	}
	upstream, err := os.ReadFile(*in)
	if err != nil {
		fmt.Fprintln(os.Stderr, "exerciselib-import:", err)
		os.Exit(1)
	}
	data, err := trim(upstream, *commit)
	if err != nil {
		fmt.Fprintln(os.Stderr, "exerciselib-import:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(*out, data, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "exerciselib-import:", err)
		os.Exit(1)
	}
}
