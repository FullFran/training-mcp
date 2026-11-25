package mcpsrv

import (
	"context"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/fullfran/training-mcp/internal/training"
)

const datePattern = `^\d{4}-\d{2}-\d{2}$`

// LogBodyweightInput is one day's bodyweight as the person said it.
type LogBodyweightInput struct {
	Date   string  `json:"date,omitempty" jsonschema:"Day of the weight, YYYY-MM-DD. Omit for today."`
	Weight float64 `json:"weight" jsonschema:"The weight as the person said it, in unit."`
	Unit   string  `json:"unit" jsonschema:"The unit the person said the weight in: kg or lb. Stored in kg either way."`
	Note   string  `json:"note,omitempty" jsonschema:"Optional context the person gave, such as fasted or after travel; at most 500 characters."`
}

// resolve fixes a dateless entry to today when it is proposed, so accepting it
// another day still records the day it was said.
func (in *LogBodyweightInput) resolve(_ context.Context, svc *training.Service) error {
	if in.Date == "" {
		in.Date = svc.Today()
	}
	return nil
}

// BodyweightEntry is a stored entry with its weight also in the person's unit.
type BodyweightEntry struct {
	Date       string  `json:"date"`
	WeightKG   float64 `json:"weight_kg"`
	Weight     float64 `json:"weight" jsonschema:"weight_kg in weight_unit."`
	WeightUnit string  `json:"weight_unit" jsonschema:"The person's unit: kg or lb."`
	Note       string  `json:"note,omitempty"`
}

type LogBodyweightOut struct {
	Entry *BodyweightEntry `json:"entry,omitempty" jsonschema:"The entry as stored."`
	// Replaced is the entry the day had before, which this one overwrote.
	Replaced *BodyweightEntry `json:"replaced,omitempty" jsonschema:"The entry this one replaced: one entry per day."`
	ProposalRef
}

type BodyweightHistoryInput struct {
	From string `json:"from,omitempty" jsonschema:"First day, YYYY-MM-DD, inclusive. Default: 89 days before to."`
	To   string `json:"to,omitempty" jsonschema:"Last day, YYYY-MM-DD, inclusive. Default: today. The range is at most 366 days."`
}

type BodyweightAverageOut struct {
	From     string  `json:"from"`
	To       string  `json:"to"`
	Entries  int     `json:"entries" jsonschema:"How many entries the average is over."`
	WeightKG float64 `json:"weight_kg"`
	Weight   float64 `json:"weight" jsonschema:"weight_kg in weight_unit."`
}

type BodyweightSummaryOut struct {
	Count    int                  `json:"count"`
	First    BodyweightEntry      `json:"first"`
	Last     BodyweightEntry      `json:"last"`
	Min      BodyweightEntry      `json:"min" jsonschema:"The lowest entry; the earliest of equal ones."`
	Max      BodyweightEntry      `json:"max" jsonschema:"The highest entry; the earliest of equal ones."`
	ChangeKG float64              `json:"change_kg" jsonschema:"last minus first, in kg."`
	Change   float64              `json:"change" jsonschema:"change_kg in weight_unit."`
	Avg7     BodyweightAverageOut `json:"avg_7d" jsonschema:"Mean of the entries in the 7 days ending on the last entry, both included."`
}

type BodyweightHistoryOut struct {
	From       string                `json:"from"`
	To         string                `json:"to"`
	WeightUnit string                `json:"weight_unit"`
	Entries    []BodyweightEntry     `json:"entries" jsonschema:"Oldest first."`
	Summary    *BodyweightSummaryOut `json:"summary,omitempty" jsonschema:"Absent when the range has no entry."`
}

type DeleteBodyweightInput struct {
	Date string `json:"date" jsonschema:"The day whose entry to delete, YYYY-MM-DD."`
}

type DeleteBodyweightOut struct {
	Date string `json:"date,omitempty"`
	ProposalRef
}

type SessionNotesInput struct {
	SessionID int64  `json:"session_id" jsonschema:"ID of the session."`
	Notes     string `json:"notes" jsonschema:"Free notes on the session in the person's words, such as sleep, a niggle or a packed gym; at most 2000 characters. Replaces the notes it had. Empty clears them."`
}

type SessionNotesOut struct {
	SessionID int64  `json:"session_id,omitempty"`
	Notes     string `json:"notes"`
	ProposalRef
}

// addBodyweightTools are the session context the person chooses to record:
// one bodyweight entry a day, and free notes on a session. Neither is needed to
// log a set, and none of them judges what is recorded.
func addBodyweightTools(s *mcp.Server, w *writes, service training.ServiceFor) {
	logSchema := mustInputSchema[LogBodyweightInput]()
	logSchema.Properties["date"].Pattern = datePattern
	logSchema.Properties["weight"].ExclusiveMinimum = jsonschema.Ptr(0.0)
	logSchema.Properties["unit"].Enum = []any{training.UnitKG, training.UnitLB}
	logSchema.Properties["note"].MaxLength = jsonschema.Ptr(training.MaxBodyweightNote)
	addWrite(w, &mcp.Tool{Name: "log_bodyweight", Description: "Record the person's bodyweight for a day, only when they say it. One entry per day: logging a day again replaces its entry (the result shows the one replaced). Give the number and unit exactly as the person said them; it is stored in kg and shown in the person's unit. The weight must be 20 to 400 kg.", InputSchema: logSchema}, "bodyweight.log", func(ctx context.Context, svc *training.Service, in LogBodyweightInput) (LogBodyweightOut, error) {
		unit, err := svc.WeightUnit(ctx)
		if err != nil {
			return LogBodyweightOut{}, err
		}
		var replaced *BodyweightEntry
		if in.Date != "" {
			if prior, err := svc.BodyweightHistory(ctx, in.Date, in.Date); err == nil && len(prior.Entries) == 1 {
				e := bodyweightEntry(prior.Entries[0], unit)
				replaced = &e
			}
		}
		b, err := svc.LogBodyweight(ctx, training.BodyweightInput{Date: in.Date, Weight: in.Weight, Unit: in.Unit, Note: in.Note})
		if err != nil {
			return LogBodyweightOut{}, err
		}
		e := bodyweightEntry(b, unit)
		return LogBodyweightOut{Entry: &e, Replaced: replaced}, nil
	})

	historySchema := mustInputSchema[BodyweightHistoryInput]()
	historySchema.Properties["from"].Pattern = datePattern
	historySchema.Properties["to"].Pattern = datePattern
	addTool(s, &mcp.Tool{Name: "bodyweight_history", Description: "Get the person's bodyweight entries in a date range (default the last 90 days, at most 366), oldest first, with plain numbers about them: count, first, last, min, max, change (last minus first) and the mean of the 7 days ending on the last entry. Weights are in kg and in the person's unit. Report the numbers; they carry no target or verdict.", InputSchema: historySchema}, func(ctx context.Context, _ *mcp.CallToolRequest, in BodyweightHistoryInput) (*mcp.CallToolResult, BodyweightHistoryOut, error) {
		svc := service(ctx)
		unit, err := svc.WeightUnit(ctx)
		if err != nil {
			return nil, BodyweightHistoryOut{}, toolError(err)
		}
		h, err := svc.BodyweightHistory(ctx, in.From, in.To)
		if err != nil {
			return nil, BodyweightHistoryOut{}, toolError(err)
		}
		return nil, bodyweightHistoryOut(h, unit), nil
	})

	deleteSchema := mustInputSchema[DeleteBodyweightInput]()
	deleteSchema.Properties["date"].Pattern = datePattern
	addWrite(w, &mcp.Tool{Name: "delete_bodyweight", Description: "Delete the person's bodyweight entry of one day, for an entry recorded by mistake. A day with no entry is not found.", InputSchema: deleteSchema}, "bodyweight.delete", func(ctx context.Context, svc *training.Service, in DeleteBodyweightInput) (DeleteBodyweightOut, error) {
		if err := svc.DeleteBodyweight(ctx, in.Date); err != nil {
			return DeleteBodyweightOut{}, err
		}
		return DeleteBodyweightOut{Date: in.Date}, nil
	})

	notesSchema := mustInputSchema[SessionNotesInput]()
	setPositiveID(notesSchema.Properties["session_id"])
	notesSchema.Properties["notes"].MaxLength = jsonschema.Ptr(training.MaxSessionNotes)
	addWriteBy(w, &mcp.Tool{Name: "set_session_notes", Description: "Set the free notes of a session: context the person gives, such as how they slept, a niggle or a packed gym. Replaces the notes it had; empty notes clear them. get_session returns them.", InputSchema: notesSchema}, sessionNotesAction, func(ctx context.Context, svc *training.Service, in SessionNotesInput) (SessionNotesOut, error) {
		session, err := svc.SetSessionNotes(ctx, in.SessionID, in.Notes)
		if err != nil {
			return SessionNotesOut{}, err
		}
		return SessionNotesOut{SessionID: session.ID, Notes: session.Notes}, nil
	})
}

// sessionNotesAction tells setting notes from clearing them: clearing deletes.
func sessionNotesAction(in SessionNotesInput) string {
	if strings.TrimSpace(in.Notes) == "" {
		return "session.notes.remove"
	}
	return "session.notes.set"
}

func bodyweightEntry(b training.Bodyweight, unit string) BodyweightEntry {
	return BodyweightEntry{Date: b.Date, WeightKG: b.WeightKG, Weight: training.FromKG(b.WeightKG, unit), WeightUnit: unit, Note: b.Note}
}

func bodyweightHistoryOut(h training.BodyweightHistory, unit string) BodyweightHistoryOut {
	out := BodyweightHistoryOut{From: h.From, To: h.To, WeightUnit: unit, Entries: make([]BodyweightEntry, 0, len(h.Entries))}
	for _, b := range h.Entries {
		out.Entries = append(out.Entries, bodyweightEntry(b, unit))
	}
	if s := h.Summary; s != nil {
		out.Summary = &BodyweightSummaryOut{
			Count: s.Count, First: bodyweightEntry(s.First, unit), Last: bodyweightEntry(s.Last, unit),
			Min: bodyweightEntry(s.Min, unit), Max: bodyweightEntry(s.Max, unit),
			ChangeKG: s.ChangeKG, Change: training.FromKG(s.ChangeKG, unit),
			Avg7: BodyweightAverageOut{From: s.Avg7.From, To: s.Avg7.To, Entries: s.Avg7.Entries,
				WeightKG: s.Avg7.WeightKG, Weight: training.FromKG(s.Avg7.WeightKG, unit)},
		}
	}
	return out
}
