package training

import (
	"context"
	"errors"
	"testing"

	"github.com/fullfran/training-mcp/internal/evidence"
)

// Evidence gives perUserStore an empty library; these tests never search it.
func (p *perUserStore) Evidence() evidence.Index { return emptyEvidence{} }

type emptyEvidence struct{}

func (emptyEvidence) Search(context.Context, evidence.IndexQuery) ([]evidence.Hit, error) {
	return nil, nil
}
func (emptyEvidence) Source(context.Context, string) (evidence.Source, error) {
	return evidence.Source{}, evidence.ErrUnknownSource
}
func (emptyEvidence) Loaded(context.Context) (evidence.LoadedCorpus, error) {
	return evidence.LoadedCorpus{Version: "empty"}, nil
}

func TestEvidenceErrorsMapToServiceErrors(t *testing.T) {
	svc := NewServices(&perUserStore{}, nil).ForUser(1)
	ctx := context.Background()
	r, err := svc.SearchEvidence(ctx, evidence.SearchInput{Query: "rest"})
	if err != nil || r.NoResults == "" || r.CorpusVersion != "empty" {
		t.Fatalf("%+v %v", r, err)
	}
	if _, err = svc.SearchEvidence(ctx, evidence.SearchInput{Query: "rest", Filters: evidence.Filters{Topic: "cardio"}}); !errors.Is(err, ErrValidation) {
		t.Fatalf("bad filter: %v, want ErrValidation", err)
	}
	if _, err = svc.GetEvidence(ctx, "missing-source"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown source: %v, want ErrNotFound", err)
	}
}

func TestEvidenceNeedsALibrary(t *testing.T) {
	svc := NewService(newMemoryStore(), nil)
	if _, err := svc.SearchEvidence(context.Background(), evidence.SearchInput{Query: "rest"}); err == nil {
		t.Fatal("a service without a library searched")
	}
	if _, err := svc.GetEvidence(context.Background(), "x"); err == nil {
		t.Fatal("a service without a library answered")
	}
}
