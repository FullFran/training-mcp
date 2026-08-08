package training

import "context"

type Store interface {
	Start(context.Context, Session) (Session, error)
	AddSet(context.Context, AddSetInput) (Set, float64, error)
	UpdateSet(context.Context, int64, SetPatch) (Set, float64, error)
	DeleteSet(context.Context, int64) (int64, float64, int, error)
	GetSession(context.Context, int64) (Session, error)
	ListSessions(context.Context, ListFilter) ([]SessionSummary, error)
	RecentExercises(context.Context, int) ([]ExerciseMemory, error)
	DeleteSession(context.Context, int64) (int, error)
	ExerciseHistory(context.Context, string, int) (ExerciseHistory, error)
	WeeklyVolume(context.Context, ListFilter) ([]WeeklyVolume, error)
	SetExerciseGroup(context.Context, ExerciseGroup) error
	ExerciseGroups(context.Context) ([]ExerciseGroup, error)
	VolumeByGroup(context.Context, ListFilter) ([]GroupVolume, error)
}
