package websrv

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/fullfran/training-mcp/internal/training"
)

// Workout memory hooks (issue #69). They are product features, never guards:
// a failure is logged, without the person's data, and the workout goes on.

// logHook records a failed memory hook.
func (s *Server) logHook(hook string) {
	logger := s.opts.Logger
	if logger == nil {
		logger = log.Default()
	}
	line, _ := json.Marshal(map[string]string{"event": "memory.hook", "action": "failed", "hook": hook})
	logger.Print(string(line))
}

// finishWorkout is Terminar: the session's summary is proposed as a memory
// note, once per session. The answer is the same whatever became of it.
func (s *Server) finishWorkout(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(strings.TrimSpace(r.FormValue("session_id")), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "bad session_id", http.StatusBadRequest)
		return
	}
	if s.opts.Apply != nil {
		// Another person's session is missing to this one: nothing to propose.
		if _, _, err := s.service(r.Context()).ProposeWorkoutSummary(r.Context(), id, s.opts.Apply); err != nil && !errors.Is(err, training.ErrNotFound) {
			s.logHook("workout_summary")
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// proposeIdleSummaries proposes the summary of a recent workout that went idle
// without Terminar, when the app is opened.
func (s *Server) proposeIdleSummaries(ctx context.Context) {
	if s.opts.Apply == nil {
		return
	}
	if _, err := s.service(ctx).ProposeIdleWorkoutSummaries(ctx, s.opts.Apply); err != nil {
		s.logHook("idle_workout_summary")
	}
}
