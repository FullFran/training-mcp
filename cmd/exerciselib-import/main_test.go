package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/fullfran/training-mcp/internal/training"
)

const testCommit = "f00c92c7dcf1216a928a52c3706c7ce8e2f71ed5"

const upstreamFixture = `[
 {"id":"B_Row","name":"Bent Over Row","force":"pull","primaryMuscles":["middle back"],"secondaryMuscles":["lats","biceps"],"equipment":"barbell","category":"strength","instructions":["Pull."],"images":["b.jpg"]},
 {"id":"A_Curl","name":"Curl","primaryMuscles":["biceps"],"secondaryMuscles":[],"equipment":null,"category":"strength"}
]`

func TestTrimKeepsOnlyTheUsedFieldsSortedByIDWithProvenance(t *testing.T) {
	out, err := trim([]byte(upstreamFixture), testCommit)
	if err != nil {
		t.Fatal(err)
	}
	var got training.PublicDataset
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("output is not a dataset: %v\n%s", err, out)
	}
	sum := sha256.Sum256([]byte(upstreamFixture))
	if got.Commit != testCommit || got.UpstreamSHA256 != hex.EncodeToString(sum[:]) || got.License != "Unlicense" ||
		got.Repository != repository || got.UpstreamFile != upstreamFile || got.Source != "free-exercise-db" {
		t.Fatalf("provenance = %+v", got)
	}
	if len(got.Exercises) != 2 || got.Exercises[0].ID != "A_Curl" || got.Exercises[1].ID != "B_Row" {
		t.Fatalf("exercises = %+v", got.Exercises)
	}
	row := got.Exercises[1]
	if row.Name != "Bent Over Row" || strings.Join(row.PrimaryMuscles, ",") != "middle back" || strings.Join(row.SecondaryMuscles, ",") != "lats,biceps" ||
		row.Equipment != "barbell" || row.Category != "strength" {
		t.Fatalf("row = %+v", row)
	}
	if curl := got.Exercises[0]; curl.Equipment != "" || curl.SecondaryMuscles == nil {
		t.Fatalf("curl = %+v", curl)
	}
	if n := strings.Count(string(out), `"exercises"`); n != 1 {
		t.Fatalf("exercises key appears %d times:\n%s", n, out)
	}
	for _, dropped := range []string{"instructions", "images", "force"} {
		if strings.Contains(string(out), dropped) {
			t.Fatalf("output keeps %q:\n%s", dropped, out)
		}
	}
	again, err := trim([]byte(upstreamFixture), testCommit)
	if err != nil || string(again) != string(out) {
		t.Fatal("the import is not reproducible")
	}
}

func TestTrimRefusesWhatItCannotMap(t *testing.T) {
	cases := map[string]string{
		"unknown muscle":  `[{"id":"x","name":"X","primaryMuscles":["pecs"],"secondaryMuscles":[]}]`,
		"no primary":      `[{"id":"x","name":"X","primaryMuscles":[],"secondaryMuscles":[]}]`,
		"no id":           `[{"id":"","name":"X","primaryMuscles":["chest"]}]`,
		"no name":         `[{"id":"x","name":" ","primaryMuscles":["chest"]}]`,
		"repeated id":     `[{"id":"x","name":"X","primaryMuscles":["chest"]},{"id":"x","name":"Y","primaryMuscles":["chest"]}]`,
		"not json":        `{"exercises":`,
		"empty":           `[]`,
		"executable-like": `[{"id":"x","name":"X","primaryMuscles":["$(rm -rf /)"]}]`,
	}
	for name, input := range cases {
		if _, err := trim([]byte(input), testCommit); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := trim([]byte(upstreamFixture), "main"); err == nil {
		t.Error("a branch name was accepted as the pinned commit")
	}
}

// The vendored file is in exactly the form the importer writes, pinned to the
// commit the docs name.
func TestVendoredFileIsInTheImportersForm(t *testing.T) {
	raw, err := os.ReadFile("../../internal/training/exerciselibdata/free-exercise-db.json")
	if err != nil {
		t.Fatal(err)
	}
	var vendored training.PublicDataset
	if err := json.Unmarshal(raw, &vendored); err != nil {
		t.Fatal(err)
	}
	again, err := encode(vendored)
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != string(raw) {
		t.Fatal("the vendored file differs from the importer's encoding of it: re-import it instead of editing it")
	}
	if vendored.Commit != testCommit || vendored.License != "Unlicense" || len(vendored.Exercises) != 876 {
		t.Fatalf("vendored provenance = %s %s, %d exercises", vendored.Commit, vendored.License, len(vendored.Exercises))
	}
}
