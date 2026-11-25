package mcpsrv

import (
	"context"

	"github.com/fullfran/training-mcp/internal/training"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The exercise library tools (#137): one read to look a name up, one to list
// what still needs mapping, and two writes, each its own action type.
const (
	exerciseEntrySaveAction = "exercise.library.save"
	exerciseAliasSetAction  = "exercise.alias.set"
)

// fractionalNote ends the descriptions of the two volume reads, which return
// the fractional view beside their unchanged per-group totals.
const fractionalNote = " Beside it, fractional gives the same working sets counted by the exercise library: each primary muscle of an exercise's entry gets 1 set and each secondary muscle 0.5 (Pelland et al., Sports Medicine 2025), with SI weighted the same way; an exercise without an entry gives 1 set to its exercise group. Muscles the library marks uncertain are listed under uncertain and not counted. The existing fields are unchanged."

type ExerciseLookupInput struct {
	Name string `json:"name" jsonschema:"Exercise name as the person says or logs it, in any language, case or accents; up to 120 characters."`
}
type ExerciseLookupOut struct {
	training.ExerciseLookup
}
type ExerciseEntryInput struct {
	Name      string   `json:"name" jsonschema:"Exercise name. Saving the name of a curated or public entry corrects it for this person only; saving it again replaces the person's entry."`
	Primary   []string `json:"primary" jsonschema:"Muscle groups the exercise mainly trains, at least one. Each counts a whole set toward its group in the fractional volume."`
	Secondary []string `json:"secondary,omitempty" jsonschema:"Muscle groups it also trains. Each counts half a set toward its group in the fractional volume. A group is in one list at most."`
}
type ExerciseEntryOut struct {
	Entry training.LibraryEntry `json:"entry"`
	ProposalRef
}
type ExerciseAliasInput struct {
	Alias  string `json:"alias" jsonschema:"Name the person logs, e.g. 'press banca'. Setting it again points it at another entry."`
	Target string `json:"target" jsonschema:"Name of the library entry it means, as lookup_exercise returns it. It must be found without aliases."`
}
type ExerciseAliasOut struct {
	training.AliasResult
	ProposalRef
}
type MappingNeedsOut struct {
	Exercises []training.MappingNeed `json:"exercises"`
}

func addExerciseLibraryTools(s *mcp.Server, w *writes, service training.ServiceFor) {
	lookupSchema := mustInputSchema[ExerciseLookupInput]()
	lookupSchema.Properties["name"].Pattern = `.*\S.*`
	lookupSchema.Properties["name"].MaxLength = jsonschema.Ptr(120)
	addTool(s, &mcp.Tool{Name: "lookup_exercise", Description: "Look an exercise name up in the exercise library and return its muscles: primary ones count 1 set, secondary ones 0.5. Lookup order: the person's alias, then their own entries, then the curated entries, then the public free-exercise-db dataset; the result names the layer and whether the match was by alias, exact name or normalized name (case, accents and punctuation ignored). A public muscle without one clear muscle group is marked uncertain with its candidate groups and is never counted. When nothing is found, found is false and candidates lists similar entries; pick none without asking the person.", InputSchema: lookupSchema}, func(ctx context.Context, _ *mcp.CallToolRequest, in ExerciseLookupInput) (*mcp.CallToolResult, ExerciseLookupOut, error) {
		v, err := service(ctx).LookupExercise(ctx, in.Name)
		return nil, ExerciseLookupOut{ExerciseLookup: v}, toolError(err)
	})
	addTool(s, &mcp.Tool{Name: "list_exercises_needing_mapping", Description: "List the person's logged exercises (working sets) whose muscles the library cannot give: not_found (no entry; their sets still count toward their set_exercise_group group, with up to 3 candidates) and uncertain (the entry has a muscle without one clear group). Fix one with set_exercise_alias or save_exercise_entry."}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, MappingNeedsOut, error) {
		v, err := service(ctx).ExercisesNeedingMapping(ctx)
		if v == nil {
			v = []training.MappingNeed{}
		}
		return nil, MappingNeedsOut{Exercises: v}, toolError(err)
	})

	entrySchema := mustInputSchema[ExerciseEntryInput]()
	entrySchema.Properties["name"].Pattern = `.*\S.*`
	entrySchema.Properties["name"].MaxLength = jsonschema.Ptr(120)
	for _, list := range []string{"primary", "secondary"} {
		setNonNullableType(entrySchema.Properties[list], "array")
		entrySchema.Properties[list].Items.Enum = muscleGroupEnum()
		entrySchema.Properties[list].MaxItems = jsonschema.Ptr(len(training.MuscleGroups))
	}
	entrySchema.Properties["primary"].MinItems = jsonschema.Ptr(1)
	addWrite(w, &mcp.Tool{Name: "save_exercise_entry", Description: "Save an exercise to the person's own library with its primary and secondary muscle groups. Their own entry wins over the curated and public ones from then on, for them only; correcting a curated or public entry copies it into their library and records which entry it was based on. Use it for an exercise lookup_exercise does not find or marks uncertain, after asking the person.", InputSchema: entrySchema}, exerciseEntrySaveAction, func(ctx context.Context, svc *training.Service, in ExerciseEntryInput) (ExerciseEntryOut, error) {
		v, err := svc.SaveExerciseEntry(ctx, training.LibraryEntryInput{Name: in.Name, Primary: in.Primary, Secondary: in.Secondary})
		return ExerciseEntryOut{Entry: v}, err
	})
	aliasSchema := mustInputSchema[ExerciseAliasInput]()
	for _, field := range []string{"alias", "target"} {
		aliasSchema.Properties[field].Pattern = `.*\S.*`
		aliasSchema.Properties[field].MaxLength = jsonschema.Ptr(120)
	}
	addWrite(w, &mcp.Tool{Name: "set_exercise_alias", Description: "Make a name the person logs mean a library entry, e.g. 'press banca' for 'Barbell Bench Press - Medium Grip', for them only. The alias follows the entry: if they later correct it, the alias reaches the correction. It does not rename logged sets or change set_exercise_group.", InputSchema: aliasSchema}, exerciseAliasSetAction, func(ctx context.Context, svc *training.Service, in ExerciseAliasInput) (ExerciseAliasOut, error) {
		v, err := svc.SetExerciseAlias(ctx, in.Alias, in.Target)
		return ExerciseAliasOut{AliasResult: v}, err
	})
}
