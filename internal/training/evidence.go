package training

import (
	"context"
	"errors"
	"fmt"

	"github.com/fullfran/training-mcp/internal/evidence"
)

// errNoEvidence is a Service built without the library (NewService alone).
var errNoEvidence = errors.New("this service has no evidence library")

// SearchEvidence searches the training-science library. The library is global
// and read-only: the answer is the same for every user and nothing is stored.
func (s *Service) SearchEvidence(ctx context.Context, in evidence.SearchInput) (evidence.Result, error) {
	if s.evidence == nil {
		return evidence.Result{}, errNoEvidence
	}
	r, err := s.evidence.Search(ctx, in)
	return r, evidenceError(err)
}

// GetEvidence returns one source of the library whole.
func (s *Service) GetEvidence(ctx context.Context, id string) (evidence.Entry, error) {
	if s.evidence == nil {
		return evidence.Entry{}, errNoEvidence
	}
	e, err := s.evidence.Get(ctx, id)
	return e, evidenceError(err)
}

// evidenceError keeps the library's message and gives it the service error a
// driving adapter knows how to show.
func evidenceError(err error) error {
	switch {
	case errors.Is(err, evidence.ErrInvalid):
		return fmt.Errorf("%w: %v", ErrValidation, err)
	case errors.Is(err, evidence.ErrUnknownSource):
		return fmt.Errorf("%w: %v", ErrNotFound, err)
	}
	return err
}
