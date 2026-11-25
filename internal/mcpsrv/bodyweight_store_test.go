package mcpsrv

import (
	"context"

	"github.com/fullfran/training-mcp/internal/training"
)

// testStore keeps no bodyweight and no session notes.
func (testStore) PutBodyweight(context.Context, training.Bodyweight) error { return nil }
func (testStore) Bodyweights(context.Context, string, string) ([]training.Bodyweight, error) {
	return nil, nil
}
func (testStore) DeleteBodyweight(context.Context, string) error { return training.ErrNotFound }
func (testStore) SetSessionNotes(context.Context, int64, string) error {
	return training.ErrNotFound
}
