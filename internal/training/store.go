package training

import "context"

// Store is the persistence port. Everything that belongs to a person is reached
// through ForUser, so a data method cannot be called without saying whose data it is.
type Store interface {
	ForUser(userID int64) UserStore
	// UserBySubject finds the account of an identity provider's subject.
	UserBySubject(ctx context.Context, subject string) (userID int64, found bool, err error)
	// ProvisionUser returns the subject's account, creating it if needed. Concurrent
	// calls for one subject get one account.
	ProvisionUser(ctx context.Context, subject string) (int64, error)
	// Snapshot copies the whole database, every user's data included.
	Snapshot(context.Context, string) error
}

// UserStore is the data of one user. Another user's id behaves exactly as a missing
// id does: the same error, so its existence cannot be inferred.
type UserStore interface {
	Start(context.Context, Session) (Session, error)
	AddSet(context.Context, AddSetInput) (Set, float64, error)
	LogSets(context.Context, LogSetsRecord) (LogSetsResult, error)
	UpdateSet(context.Context, int64, SetPatch) (Set, float64, error)
	DeleteSet(context.Context, int64) (int64, float64, int, error)
	GetSession(context.Context, int64) (Session, error)
	ListSessions(context.Context, ListFilter) ([]SessionSummary, error)
	RecentExercises(context.Context, int) ([]ExerciseMemory, error)
	DeleteSession(context.Context, int64) (int, error)
	CreatePlan(context.Context, Plan) (Plan, error)
	ListPlans(context.Context) ([]Plan, error)
	GetPlan(context.Context, int64) (Plan, error)
	UpdatePlan(context.Context, int64, PlanPatch) error
	SetPlanItem(context.Context, int64, PlanItem) error
	RemovePlanItem(context.Context, int64, string) error
	MovePlanItem(context.Context, int64, string, int) error
	DeletePlan(context.Context, int64) error
	SessionProgress(context.Context, int64) ([]PlanProgress, error)
	SetSessionItem(context.Context, int64, PlanItem) error
	PatchSessionItem(context.Context, int64, string, SessionItemPatch) error
	SwapSessionItem(context.Context, int64, string, string) error
	RemoveSessionItem(context.Context, int64, string) error
	SetFeedback(context.Context, int64, Feedback) error
	SessionFeedback(context.Context, int64) ([]Feedback, error)
	LatestFeedback(context.Context) (map[string]Feedback, error)
	RecentFeedback(context.Context, int) (map[string][]Feedback, error)
	SetLandmarks(context.Context, Landmarks) error
	AllLandmarks(context.Context) (map[string]Landmarks, error)
	ExerciseHistory(context.Context, string, int) (ExerciseHistory, error)
	WeeklyVolume(context.Context, ListFilter) ([]WeeklyVolume, error)
	SetExerciseGroup(context.Context, ExerciseGroup) error
	ExerciseGroups(context.Context) ([]ExerciseGroup, error)
	VolumeByGroup(context.Context, ListFilter) ([]GroupVolume, error)
	SetExerciseNote(context.Context, ExerciseNote) error
	ExerciseNotes(context.Context) ([]ExerciseNote, error)
	SupersetFor(context.Context, int64, string) (string, error)
}
