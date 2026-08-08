# MCP tool reference

32 tools over Streamable HTTP at `/mcp`. Descriptions below are the ones the
model actually receives — they are extracted from `internal/mcpsrv/server.go`,
so this page cannot drift from the surface it documents.


## Logging sets

The everyday path. `log_set` is the one to reach for: it needs no session id, so recording a set is a single call.


| Tool | What it does |
|---|---|
| `log_set` | Record one or more identical sets into today's session, creating that session if it does not exist yet. This is the simplest way to log training: it needs no session id. Prefer it over start_session plus add_set. |
| `start_session` | Start a training session on an optional date, optionally following a plan. Omit date to use the server's current local date. |
| `add_set` | Add a set to an existing training session and return the set plus the session's recalculated total SI. |
| `update_set` | Update one or more fields of an existing set. Omitted fields remain unchanged; RPE changes recalculate SI. |
| `delete_set` | Permanently delete an existing set and compact the remaining set positions to a dense sequence. |
| `get_session` | Get an existing training session with its ordered sets and total SI. |
| `list_sessions` | List training sessions newest first, optionally filtered by inclusive date bounds. From must not be after to. |
| `delete_session` | Permanently delete a training session and every set in it. Irreversible; returns how many sets were destroyed. Use to remove an empty or mistaken session. |

## Plans

A plan is a reusable, ordered list of exercises with a target set count and an optional rep range and RPE. Load is deliberately not planned — the prescription is effort and reps; the weight that meets it is discovered at the gym.


| Tool | What it does |
|---|---|
| `create_plan` | Create a reusable workout plan: an ordered list of exercises with a target set count and an optional rep range and RPE. Load is not planned; it is filled in at the gym. |
| `list_plans` | List every saved workout plan with its total planned set count. |
| `get_plan` | Get one plan with its ordered exercises, target sets, rep ranges and RPEs. |
| `update_plan` | Edit a plan's name or notes without touching its exercises. Plan notes are free text describing the routine's intent, and are returned by get_plan and list_plans. |
| `set_plan_exercise` | Add an exercise to a saved plan, or replace its prescription. Editing a template never changes sessions already started from it. |
| `remove_plan_exercise` | Remove an exercise from a saved plan and close the gap in its order. |
| `move_plan_exercise` | Move an exercise one place up or down within a plan. The order is the order the session is performed in. |
| `delete_plan` | Permanently delete a plan. Sessions that followed it keep their recorded plan name. |

## Today's session, adjusted

Starting a session from a plan **copies** it. From then on the session owns its prescription: these tools change today only and never edit the template.


| Tool | What it does |
|---|---|
| `session_progress` | For a session following a plan: planned versus completed sets per exercise, with the prescribed rep range and RPE. Exercises done off-plan are listed with target_sets 0. |
| `add_session_exercise` | Add an exercise to today's session plan, or replace its prescription. Adjusting a session never edits the plan it was started from. |
| `adjust_session_exercise` | Change one exercise of today's session: its set count, rep range, RPE, or whether it is skipped. Omitted fields stay as they are. Use when the session does not go as planned. |
| `swap_session_exercise` | Substitute one exercise for another in today's session, keeping the same prescription. Use when the planned machine is taken or the movement does not feel right. |
| `remove_session_exercise` | Drop an exercise from today's session plan. Sets already logged against it are kept and reappear as off-plan work. |
| `save_session_as_plan` | Turn a session — including any in-session adjustments and off-plan work — into a new reusable plan. Skipped exercises are left out. |

## Progression and analysis

Judging progress in one call instead of reading back every session.


| Tool | What it does |
|---|---|
| `suggest_load` | Suggest the next working weight for an exercise, judged against the rep range and RPE it is prescribed at. Advisory only: it explains its reasoning and never changes a plan. Sets carrying an intensity technique are ignored, since they are not comparable to straight sets. |
| `exercise_history` | All recorded sets of one exercise, newest first, with each set's estimated 1RM, plus the best set ever recorded for it. Use this to judge progression instead of reading whole sessions. |
| `weekly_volume` | SI and set count per muscle group per training week, newest week first. Week start is the Monday of that week. Use this to see whether a muscle group's stimulus is rising or falling over time. |
| `volume_by_muscle` | Total SI and set count per muscle group, optionally within an inclusive date range. Sets whose exercise has no group yet are reported under an empty muscle_group. |

## Feedback loop

Rate a trained muscle group 0-3 on fatigue, pump and recovery, and get next week's set-count change. This is the part the source spreadsheet automated.


| Tool | What it does |
|---|---|
| `record_feedback` | Rate how one muscle group responded to a session: fatigue, pump and recovery, each 0 to 3. Only rate groups actually trained. This drives next week's set-count recommendation. |
| `volume_recommendation` | Set-count change per muscle group for next week. Weights recent feedback over older rather than reading only the last, reports how many rated sessions it is based on and the resulting confidence, declines to advise on a single rating, and never pushes volume past a recorded MEV or MRV. |
| `set_volume_landmarks` | Record a muscle group's personal weekly-set boundaries: MEV below which it stops growing, MRV above which fatigue outpaces recovery. These are individual, and `volume_recommendation` refuses to push past them. Leave a value at 0 if unknown. |
| `list_volume_landmarks` | List the muscle groups that have personal volume landmarks recorded. |

## Catalogue

Per-exercise metadata that persists across sessions.


| Tool | What it does |
|---|---|
| `set_exercise_group` | Assign an exercise to one muscle group so its sets count toward that group's volume. Re-assigning replaces the previous group. |
| `list_exercise_groups` | List every exercise that has a muscle group assigned, ordered by group. |
| `set_exercise_note` | Store a persistent setup reminder for an exercise, such as seat height or grip width. It is shown every time that exercise is logged. An empty note removes it. |
| `list_exercise_notes` | List every exercise that has a setup note. |

---

## Design notes

Why the surface looks like this. A reference table says what each tool
does; these are the decisions behind them.

A plan carries free-text **notes** at two levels: the routine as a whole (its intent, the phase of the block) and each exercise inside it (cues, tempo, whether to push the last set). Both are returned by `get_plan` and `list_plans`, and both are copied into the session, so `session_progress` exposes the intent behind the numbers while training — an assistant reading the MCP sees why, not just what. `update_plan` edits them without touching the exercises, and `set_plan_exercise`, `remove_plan_exercise` and `move_plan_exercise` edit a saved routine's exercises and their order — the order is the order the session is performed in.

Plan items can share a **superset label**, marking exercises done back to back. Logging never states it: a set is stamped with the label its exercise carries in the session's plan, so entry stays the same size. `set_exercise_note` stores a persistent setup reminder per exercise — seat height, grip width — shown every time that exercise comes up.

A set can carry an optional **intensity technique** (`drop set`, `rest-pause`, `myo-reps`, `sin parar`…). It is a property of the set, not a separate exercise, so the volume still counts toward the right muscle group while progression on the base movement stays comparable — a set with a technique never becomes the exercise's record.

`log_set` is the simplest way to record training: it takes an exercise, weight, reps and RPE, finds or creates today's session, and can record several identical sets at once. It needs no session id, so conversational logging is one call. `start_session`, `add_set`, `update_set`, `delete_set`, `get_session` and `list_sessions` remain for explicit control. Exercises are trimmed/lowercased, positions remain dense, and totals are recalculated from stored SI values.

`create_plan`, `list_plans`, `get_plan` and `delete_plan` manage reusable workout plans: an ordered list of exercises with a target set count and an optional rep range and RPE. Load is deliberately not planned — the prescription is effort and reps, and the weight that meets it is discovered at the gym. `start_session` takes an optional `plan_id`, which **copies** the plan into the session. From then on the session owns its prescription: `add_session_exercise`, `adjust_session_exercise` (sets, rep range, RPE, skip), `swap_session_exercise` and `remove_session_exercise` change today only and never edit the template, while editing a template never rewrites a past session. `session_progress` reports planned versus completed sets per exercise, listing anything done off-plan with `target_sets` 0 so it stays visible. `save_session_as_plan` promotes an adjusted session into a new reusable plan.

`delete_session` removes a session and every set in it, reporting how many were destroyed. `exercise_history` returns one exercise's sets newest first with each set's estimated 1RM plus its all-time best, so progression can be judged in one call instead of reading every session. `weekly_volume` buckets SI per muscle group by training week.

`suggest_load` advises the next working weight for an exercise, judged against the rep range and RPE it is prescribed at: hit the top of the range below target RPE and it says add weight; fall short or exceed the RPE and it says drop it. Sets carrying an intensity technique are ignored, since a drop set is not comparable to a straight set. Like every other recommendation here it is advisory and explains itself; it never edits a plan. With no previous set or no prescription to judge against it declines rather than guessing.

`record_feedback` and `volume_recommendation` close the loop the source spreadsheet automated: rate a trained muscle group 0-3 on fatigue, pump and recovery, and get next week's set-count change.

The recommendation is deliberately cautious about its own certainty:

- The magnitude weights the last three ratings **3:2:1**, so one bad night bends
  the advice instead of rewriting the week.
- Every result carries its sample count and a confidence reading, and on a
  **single rating it declines to advise at all** — one session is an anecdote.
- Personal **MEV and MRV** clamp the result, because the same 15 weekly sets can
  be under one lifter's minimum and over another's ceiling. Unknown landmarks
  clamp nothing rather than inventing a default, and a clamped answer says which
  boundary stopped it.
- The mapping from magnitude to sets lives in `training.RecommendSets`. Only two
  anchors were legible in the source sheet (magnitude 0 → "sube 3 series",
  7 → "mantén o reduce 1"); the bands between them are an interpolation, and the
  code says so.

`set_exercise_group`, `list_exercise_groups`, and `volume_by_muscle` add per-muscle-group volume. Each exercise maps to exactly one muscle group, so group SI is a true partition of session SI — every set counts once and the group totals add up to the session total. Sets whose exercise has no mapping are reported under an empty group rather than dropped, so gaps in the catalogue stay visible.
