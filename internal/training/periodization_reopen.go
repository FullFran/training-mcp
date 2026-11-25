package training

import (
	"context"
	"log"
)

// linkBeforeDelete is where a session about to be deleted is placed, read
// before the delete takes it away. Best effort: a zero link when unknown.
func (s *Service) linkBeforeDelete(ctx context.Context, sessionID int64) SessionLink {
	ps, ok := s.store.(PeriodizationStore)
	if !ok {
		return SessionLink{}
	}
	link, err := ps.SessionLink(ctx, sessionID)
	if err != nil {
		return SessionLink{}
	}
	return link
}

// reopenAfterDelete reopens the microcycle a deleted session had closed. Only
// a microcycle of the active sequence block that its last routine closed
// reopens, when it no longer holds every routine of its rotation and no later
// microcycle has moved on; one closed by hand or by a mode change stays
// closed. Best effort: a failure is logged and the delete stands.
func (s *Service) reopenAfterDelete(ctx context.Context, link SessionLink) {
	if err := s.reopen(ctx, link); err != nil {
		log.Printf("periodization: microcycle left closed after a delete: %v", err)
	}
}

func (s *Service) reopen(ctx context.Context, link SessionLink) error {
	if !link.Placed || link.OffPlan || link.MicrocycleID == 0 {
		return nil
	}
	ps, ok := s.store.(PeriodizationStore)
	if !ok {
		return nil
	}
	b, err := ps.GetBlock(ctx, link.BlockID)
	if err != nil || b.Status != BlockActive || b.Advance != AdvanceSequence {
		return err
	}
	at := -1
	for i, m := range b.Microcycles {
		if m.ID == link.MicrocycleID {
			at = i
		}
	}
	if at < 0 || b.Microcycles[at].Status != MicrocycleDone || b.Microcycles[at].ClosedBy != ClosedByPlacement {
		return nil
	}
	placed, err := ps.BlockSessions(ctx, b.ID)
	if err != nil {
		return err
	}
	count := map[int64]int{}
	for _, l := range placed {
		count[l.MicrocycleID]++
	}
	m := &b.Microcycles[at]
	if count[m.ID] >= len(effectiveRotation(b, *m)) {
		return nil
	}
	for _, later := range b.Microcycles[at+1:] {
		if later.Status != MicrocyclePending || count[later.ID] > 0 {
			return nil
		}
	}
	m.Status, m.ClosedOn, m.ClosedBy = MicrocyclePending, "", ""
	_, err = ps.SaveBlock(ctx, b)
	return err
}
