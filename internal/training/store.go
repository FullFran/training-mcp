package training

import (
	"context"
	"time"
)

// Store is the persistence port. Everything that belongs to a person is reached
// through ForUser, so a data method cannot be called without saying whose data it is.
type Store interface {
	ForUser(userID int64) UserStore
	// ImportsFor is one user's imported history, a port of its own so the many
	// UserStore doubles need not grow with it.
	ImportsFor(userID int64) ImportStore
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
	ProposalStore
	MemoryStore
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
	// LastSetAt is the most recently logged set of a session and when it was
	// logged. A session with no sets, or not the user's, is ErrNotFound.
	LastSetAt(context.Context, int64) (int64, time.Time, error)
	// ClaimWorkoutSummary records that the summary of a session was proposed:
	// true the first time, false once it already was. A session that is not the
	// user's is ErrNotFound.
	ClaimWorkoutSummary(context.Context, int64, time.Time) (bool, error)
	// RestTarget is the stored target rest in seconds; 0 when never set.
	RestTarget(context.Context) (int, error)
	SetRestTarget(context.Context, int) error
	// RecordRest stores the seconds rested after one of the user's sets, once:
	// a set that is not the user's is ErrNotFound, one already recorded is
	// ErrRestRecorded.
	RecordRest(ctx context.Context, setID int64, seconds int) error
	// WeightUnit is the stored default unit, "" when never chosen (kg).
	WeightUnit(context.Context) (string, error)
	SetWeightUnit(context.Context, string) error
	// LbSets reads the user's sets in a normalized scope, oldest first, with
	// OldKG and Corrected set.
	LbSets(context.Context, LbScope) ([]LbSet, error)
	// CorrectLbSets writes each set's NewKG and marks it corrected at the time,
	// in one transaction, only while every set is still the user's, not yet
	// corrected and still weighs OldKG; otherwise it changes nothing and
	// returns ErrNotFound or ErrLbCorrected.
	CorrectLbSets(context.Context, []LbSet, time.Time) error
}

// ProposalStore keeps one user's agent proposals and standing answers.
type ProposalStore interface {
	CreateProposal(context.Context, Proposal) (Proposal, error)
	GetProposal(context.Context, int64) (Proposal, error)
	ListProposals(context.Context, ProposalFilter) ([]Proposal, error)
	// ClaimProposal moves a pending proposal to applying; ErrNotPending if it is not pending.
	ClaimProposal(context.Context, int64) error
	// FinishProposal records how applying a claimed proposal ended.
	FinishProposal(context.Context, int64, ProposalOutcome) (Proposal, error)
	// RejectProposal answers a pending proposal with no, decided by decidedBy;
	// ErrNotPending if it is not pending.
	RejectProposal(ctx context.Context, id int64, decidedBy string, at time.Time) (Proposal, error)
	// Permissions are the standing answers. An account without a stored
	// setting accepts everything except deletes (ApprovalAcceptExceptDelete).
	Permissions(context.Context) (Permissions, error)
	SetAcceptAll(context.Context, bool) error
	// SetApprovalMode stores the account's approval mode, one of the
	// Approval* constants.
	SetApprovalMode(context.Context, string) error
	// AddRule stores a session-limited grant or denial and returns its id.
	AddRule(context.Context, AnswerRule) (int64, error)
	// ForgetRule removes a grant or denial and returns it; ErrNotFound if this
	// account has none with that id.
	ForgetRule(context.Context, int64) (AnswerRule, error)
	// RecordDecision writes a decision record and returns its id.
	RecordDecision(context.Context, DecisionRecord) (int64, error)
	// Decisions lists decision records newest first.
	Decisions(ctx context.Context, limit int) ([]DecisionRecord, error)
}
