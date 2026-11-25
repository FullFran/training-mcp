# MCP tool reference

35 tools over Streamable HTTP at `/mcp`. Descriptions below are the ones the
model actually receives — they are extracted from `internal/mcpsrv/server.go`,
so this page cannot drift from the surface it documents.


## Every write is a proposal

Agents read freely but never write directly. Each write tool records a
**proposal** (who, when, action type, payload and, once answered, the result)
and applies it only when a standing answer covers its action type. Every write
result therefore carries two extra fields:

- `status`: `accepted` when the change was applied (the other fields show it,
  exactly as before), `pending` when it waits for the person on `/review` or
  in the coach chat, or `rejected` when the person already rejected that action
  type for this session (`reject_always`). A pending or rejected change is not
  applied: its other fields are empty, a pending one is validated only when
  accepted, and the agent must not report either as done.
- `proposal_id`: the proposal that records the change.

Read tools are unchanged: they record nothing and return no `status`.

Answers apply per **action type**, never per tool name, so allowing `set.log`
covers `log_sets`, `log_set` and `add_set` alike, while removing a note
(`exercise.note.remove`) is a different action from setting one
(`exercise.note.set`). There is one plain confirmation for every action type,
destructive ones included.

### Answers (HCS-0005 section 3.2)

The person answers on `/review` or on the proposal's card in the coach chat,
with the four option kinds of [HCS-0005](https://github.com/FullFran/hcs)
section 3.2:

| Answer | Label in the app | Meaning |
|---|---|---|
| `allow_once` | Aceptar una vez | Apply this proposal only. Nothing is stored. |
| `reject_once` | Rechazar | Reject this proposal only. Nothing is stored. |
| `allow_always` | Aceptar siempre en esta sesión | Apply it and store a **grant** for its action type, limited to the session the proposal was asked in. |
| `reject_always` | Rechazar siempre en esta sesión | Reject it and store a **denial** for its action type, limited the same way. A denial wins over any grant and over every approval mode. |

The always answers are offered only for a proposal asked inside a session; one
asked outside every session can only be answered once. A stored grant or
denial also ends 12 hours after the answer at most, and `/review` lists each
one with its session so it can be forgotten (revoked) at any time.

### Sessions

A **session** is decided per channel by the server, never by the agent:

| Channel | Session |
|---|---|
| In-app coach | One stored chat conversation, `coach:conversation-<id>`. The coach's engine process reaches `/mcp` with a bearer minted for that process, and the coach resolves the bearer to the conversation that process serves, so answers given in a chat hold when it is reopened later (on a new process) and never in another chat. Conversation ids are never reused. A turn outside any stored conversation is its own session, derived from the bearer (`coach:` plus a hash; the bearer is never stored). |
| External MCP client (ChatGPT, Claude) | A time window. `/mcp` is stateless (no `Mcp-Session-Id`), and an OAuth access token rotates within the hour, so there is no MCP-native identifier for a conversation. Every external call shares the session `mcp`, which a grant or denial covers for 12 hours from the answer. |
| Anything else (no HTTP request) | No session. No stored grant or denial ever matches it (HCS-0005 R008). |

### Approval modes

Each account has one approval mode that decides the proposals no session answer
covers. It is modelled as an **account-level host grant**: it is set only from
the person's own settings on `/review` (a three-option selector), never by an
MCP tool or an agent, and every proposal it accepts is still recorded and can be
undone like any other. A session denial wins over every mode (HCS-0005 R007).
Changing the mode leaves the session grants and denials as they were. A button
to change it from the chat is tracked in #125.

| Mode | PWA label | What happens |
|---|---|---|
| `review_all` | Revisar todo | Every proposal waits for the person. |
| `accept_except_delete` | Aceptar todo salvo borrar | Every proposal is applied at once, except deleting action types, which wait (a session grant still applies them). The decision record names the `accept_except_delete` grant. |
| `accept_all` | Aceptar todo | Every proposal is applied at once, in every session and in none, including action types added later, like Claude Code's `--dangerously-skip-permissions`. The decision record names the `accept_all` grant. |

The deleting action types are `set.delete`, `session.delete`, `plan.delete`,
`memory.forget`, `plan.exercise.remove`, `session.exercise.remove`, `exercise.note.remove`,
`bodyweight.delete`, `session.notes.remove` and `block.shorten`
(`training.IsDeletingAction`).

Defaults: an account created from migration 027 on starts in
`accept_except_delete`. Every account that existed before keeps exactly its
behavior: accept-all on stays `accept_all`, and everything else (accept-all
off, or no stored setting) became an explicit `review_all`. No account was
loosened by the migration. Changes a person makes in the PWA apply directly and
are not proposals.

Migration 018 dropped every answer stored before answers became
session-limited (permanent "always" and 12-hour "session" answers): none had a
session or an origin, so none could be kept under HCS-0005 R006 and R008. Each
dropped answer has a `revoked` decision record (decider `policy`
`HCS-0005-R008`, channel `migration:018`), and the person is asked again.
Approval settings were not touched.

### Undo

The person can undo an accepted proposal from its entry in the history on
`/review` ("Deshacer"). The chat reads the ones it can offer from
`GET /review/undoable`: JSON `{"proposals":[{"id","label","summary","when","undo_url"}]}`,
the undoable proposals accepted in the last 12 hours, newest first, at most
five; `undo_url` is `POST /review/{id}/undo`. Undo is the person's own action:
no tool lets an agent undo, it applies at once, and it is recorded as its own
history entry (action type `proposal.undo`, payload `{"proposal_id":<id>}`)
with a decision record (`subject` `account`, channel `web`). A proposal is
undone once; the undo itself cannot be undone.

Accepting a proposal stores its **undo image**: every row it changed, as it was
before and right after, for every action type (sets, sessions, plans and their
exercises, session exercises, notes, groups, landmarks, feedback, the rest
target, memory facts with their revisions and relations, and the link from an
imported set to a deleted one). Undo writes the before rows back exactly, ids
and timestamps included, deletions too, in one transaction. A log_sets
idempotency key it added is removed, so the same key logs again.

**When later changes depend on it, undo is refused** with the reason, and
nothing changes:

- a row the proposal changed was changed again afterwards (a set edited or
  deleted, a memory rewritten, the rest target set again);
- something added later points at a row the undo would remove (a set logged
  in a session the proposal started, a session started from a plan it
  created);
- a row the undo would bring back lost its place to a later one (a set logged
  where a deleted set stood) or belongs to one removed since.

Later changes elsewhere do not stand in the way: undoing an added set leaves
the sets logged after it in place. Proposals answered before migration 025,
rejected or failed ones, and undos cannot be undone.

### Decision records (HCS-0005 section 3.3)

Every decision on a proposal writes a decision record before it takes effect:
`time`, `permission` (the action type), `outcome` (`allow`, `deny`, or
`revoked` when a grant or denial is forgotten), `operation` (`proposal:<id>`),
`session`, `decider` (`subject` `account` for the person's own answer, `grant`
`rule:<id>`, `accept_all` or `accept_except_delete`, `denial` `rule:<id>`, or `policy`), `channel`
(`web` for the person's answer, `grant-store` for a stored answer) and the
`answer` the person chose. A record never holds the proposal's payload or
result. A grant or denial carries the id of the record of the answer that
created it as its `origin`. If the record cannot be written the change is not
applied.

### Conformance with HCS-0005

| Rule | Status |
|---|---|
| R005 once answers never stored | Met. |
| R006 `allow_always` is a session-limited grant with `origin`, revocable | Met. Revoking is "Olvidar" on `/review`; every later proposal is decided without it (R003). |
| R007 `reject_always` is a session-limited denial that wins over a grant | Met, including over every approval mode. |
| R008 no match across sessions or without a session | Met. |
| R009 nothing outside the host remembers answers | Met: the agent never sees or answers a proposal, and the engine and clients only get `accepted`, `pending` or `rejected` results. |
| R014 fail closed | Met: a proposal is applied only on an explicit allow (person, grant or approval mode); a pending one waits with nothing applied, and a reject, an error or an unwritable decision record applies nothing. |
| R015 answers only from the person's channel | Met: answers come only from the signed-in, CSRF-protected web routes; no MCP tool answers a proposal. |

Remaining divergences: the external session is a 12-hour window shared by every
external client of the account (ChatGPT and Claude are one session) because
the stateless endpoint has no per-conversation identifier; the in-app coach
session is the stored conversation (`coach:conversation-<id>`), so a grant or
denial given in a chat holds there across engine processes and nowhere else;
connector-tool always answers for a conversation are held in memory, so a server
restart asks again; and a pending proposal has
no deadline (it waits, applying nothing, until answered), so no `timeout`
record is written. Decision records stay in the database (HCS-0005 OQ-0005-6
and OQ-0005-7).

| Action type | Tools |
|---|---|
| `session.start` | `start_session` |
| `session.delete` | `delete_session` |
| `set.log` | `log_sets`, `log_set`, `add_set` |
| `set.update` / `set.delete` | `update_set` / `delete_set` |
| `plan.create` | `create_plan`, `save_session_as_plan` |
| `plan.update` / `plan.delete` | `update_plan` / `delete_plan` |
| `plan.exercise.set` / `.remove` / `.move` | `set_plan_exercise` / `remove_plan_exercise` / `move_plan_exercise` |
| `session.exercise.set` / `.adjust` / `.swap` / `.remove` | `add_session_exercise` / `adjust_session_exercise` / `swap_session_exercise` / `remove_session_exercise` |
| `exercise.note.set` / `exercise.note.remove` | `set_exercise_note` (an empty note removes it) |
| `exercise.group.set` | `set_exercise_group` |
| `feedback.record` | `record_feedback` |
| `landmarks.set` | `set_volume_landmarks` |
| `rest.set` | `stop_rest` | Stop the rest between sets and store how long the person rested, on the set the rest followed. With no arguments it stops the rest running now at the seconds elapsed; `set_id` with `rest_seconds` (0 to 1200) records a rest the person reports. Stored once per set. |
| `set_rest_target` |
| `rest.stop` | `stop_rest` |
| `set.correct_lb` | `correct_lb_sets` |
| `memory.save` / `memory.forget` | `memory_save` / `memory_forget` |
| `memory.judge` / `memory.confirm` / `memory.consolidate` | `memory_judge` / `memory_confirm` / `memory_consolidate` |
| `exercise.library.save` / `exercise.alias.set` | `save_exercise_entry` / `set_exercise_alias` |
| `bodyweight.log` / `bodyweight.delete` | `log_bodyweight` / `delete_bodyweight` |
| `session.notes.set` / `session.notes.remove` | `set_session_notes` (empty notes remove them) |
| `goal.create` / `goal.update` | `set_goal` (without / with `goal_id`) |
| `block.create` / `block.update` / `block.shorten` | `save_block` (without `block_id` / with it / with a smaller `microcycle_count`) |
| `block.activate` / `block.microcycle.complete` / `block.microcycle.skip` / `block.close` | `advance_block` (`action` `activate` / `complete_microcycle` / `skip_microcycle` / `close`) |
| `session.block.assign` | `assign_session_block` |

A proposal is claimed before it is applied, so two answers cannot apply it
twice. If the process stops between applying and recording the outcome, the
proposal stays `applying` and shows in the history as such; check the data
before proposing it again.

Client handling (manual check, pending): ChatGPT and Claude must relay
`pending` as "waiting for your approval in the app". Record the result of that
check here once it has been done against production.

## Logging sets

The everyday path. `log_sets` is the one to reach for: it needs no session id, its sets may differ, and repeating a call with the same `idempotency_key` stores nothing new, so a client that lost the response can safely retry. While the call waits for the person's approval, repeating it with the same key and sets returns the proposal already waiting, never a second one.


| Tool | What it does |
|---|---|
| `log_sets` | Record the sets of one exercise into today's session in a single call, creating the session if needed. The sets may differ, as in '12, 12, 10 and 8 reps'. All are stored or none. Omit rpe unless the user stated it. Repeating a call with the same idempotency_key returns the original result with replayed true and stores nothing new, so retrying is always safe. Prefer this over log_set. |
| `log_set` | Record one or more identical sets into today's session, creating that session if it does not exist yet. It needs no session id but is not safe to retry, since repeating it records the sets again. Prefer log_sets, which is. |
| `start_session` | Start a training session on an optional date, optionally following a plan. Omit date to use the server's current local date. Inside an active block, the current microcycle's modifiers are applied to the session's copy of the routine and shown in `block_modifiers` (see [Periodization](#periodization)). |
| `add_set` | Add a set to an existing training session and return the set plus the session's recalculated total SI. |
| `update_set` | Update one or more fields of an existing set. Omitted fields remain unchanged; RPE changes recalculate SI. Set category to 'warmup' when the user says a logged set was a warm-up, or to 'working' to undo that. |
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
| `live_workout` | What is happening in today's workout right now: the session with its sets, planned versus done per exercise, the exercise planned next, the last set and when it was logged, and the rest since it against the person's target. Needs no session id. |
| `render_rest_timer` | The person's rest since their last logged set, as an interactive countdown in chats that support MCP Apps (`ui://vibetraining/rest-timer/v4`) and as one line of text elsewhere. The structured result lists today's exercises with their progress for the view's exercise picker. Optional `mode`: `compact` for the countdown alone (when the person asks just for the timer), `full` (the default) for the whole workout view. Reads only. See [MCP Apps](apps.md). |
| `set_rest_target` | Set the person's target rest between sets, 30 to 900 seconds (120 until set). The phone's rest timer uses the same value. |
| `session_progress` | For a session following a plan: planned versus completed sets per exercise, with the prescribed rep range and RPE. Exercises done off-plan are listed with target_sets 0. `block_modifiers` shows the microcycle modifiers the session got when it started, if any. |
| `add_session_exercise` | Add an exercise to today's session plan. If it is already there, target_sets is replaced and every optional field you omit (rep range, RPE, superset, notes) keeps its current value; sending either rep bound replaces the whole range. To clear a field, remove the exercise and add it again. Adjusting a session never edits the plan it was started from. |
| `adjust_session_exercise` | Change one exercise of today's session: its set count, rep range, RPE, or whether it is skipped. Omitted fields stay as they are. Use when the session does not go as planned. |
| `swap_session_exercise` | Substitute one exercise for another in today's session, keeping the same prescription. Use when the planned machine is taken or the movement does not feel right. |
| `remove_session_exercise` | Drop an exercise from today's session plan. Sets already logged against it are kept and reappear as off-plan work. |
| `save_session_as_plan` | Turn a session — including any in-session adjustments and off-plan work — into a new reusable plan. Skipped exercises are left out. |

### Live workout and rest

`live_workout` is what a coach watching the session would see. Rest is derived from the timestamp of the most recently logged set, never stored: `rest.state` is `none` (no set yet today), `resting` (with `remaining_seconds`), `ready` (target reached, with `over_by_seconds`), `superset` (the next exercise of the same round follows without rest; after the last exercise of a round the state is `resting` before the next round) `idle` (more than 20 minutes since the last set, so not a rest) or `stopped` (the person stopped the rest, with `rested_seconds`). Stopping the rest is the only way a rest is stored (migration 028 `sets.rest_after_seconds`, shown as the set's `rest_after_seconds`): with `stop_rest`, the phone's skip button or the chat's rest timer, on the set the rest followed, never inferred from the gap between sets. `stop_rest` with no arguments fixes the elapsed seconds when it is called, so a proposal accepted later stores what the timer showed. `next` is the exercise to do next: the next unfinished one of a superset round, else the exercise just worked while it has sets to go, else the first unfinished one in plan order. The target is one value per person, shared with the phone's ±30 s buttons. The session in `get_session` and `live_workout` carries `started_at` (RFC 3339, UTC), when it was started here; the app's workout clock and finish summary time the workout from it, so every device shows the same duration. Imported workouts omit it, as their start is in the source app's own clock.

## Weight units

Every weight in every tool is kilograms, as before: `weight_kg` inputs and outputs keep their meaning and are still required where they were. The person chooses how they read and type weights in the app (kg or lb, migration 026 `users.weight_unit`); `live_workout` and `memory_context` report it as `weight_unit` (`kg` until chosen), and a coach chat starts with a host line saying it, so the agent talks in the person's unit and converts (1 lb = 0.45359237 kg). An input in lb was not added because it would change which fields are required.

| Tool | Description |
|---|---|
| `preview_lb_correction` | Preview correcting past sets whose weight was typed in lb but stored as kg (135 lb logged as 135 kg). Give exactly one scope: session_id, exercise with from and to, or set_ids. Lists every set with old_kg (now) and new_kg (the number read as lb), marks sets already corrected, and counts what would change. Changes nothing; correct_lb_sets applies it. |
| `correct_lb_sets` | Correct past sets whose weight was typed in lb but stored as kg: each set's stored number is read as lb and stored in kg, the same as typing it in lb. All or nothing: a set that is not the person's, or one already corrected, refuses the whole correction, so it never applies twice. Only the weight changes; the person can undo it in the app. Preview first with preview_lb_correction and confirm with the person. |

A corrected set is marked (`sets.lb_corrected_at`), which is what refuses a second correction. The person does the same in the app (Tú › Datos › Corregir pesos en lb, or from a session's page): it is recorded as a proposal they decided at once, so it shows in the history and can be undone like any accepted proposal; `correct_lb_sets` payloads name their sets as `set_ids`, which undo uses to capture those sessions.

## Progression and analysis

Judging progress in one call instead of reading back every session.


| Tool | What it does |
|---|---|
| `suggest_load` | Suggest the next working weight for an exercise, judged against the rep range and RPE it is prescribed at. Advisory only: it explains its reasoning and never changes a plan. Sets carrying an intensity technique are ignored, since they are not comparable to straight sets, and so are warm-up sets. |
| `exercise_history` | All recorded sets of one exercise, newest first, with each set's estimated 1RM, plus the best set ever recorded for it. Use this to judge progression instead of reading whole sessions. |
| `performance_dropoff` | Apparent per-set performance drop within one session, per exercise, computed deterministically from load, reps and stated RPE with named estimators (epley_v1, rir_epley_v1, brzycki_v1). Lists which sets were comparable and why others were excluded (recorded warm-up, technique, no load, probable warm-up), the reference set, each model's drop and confidence, and abstains with fewer than two comparable sets. An unstated RPE never feeds the RIR model. A device-free proxy: not velocity, force or fatigue, and it changes nothing. |
| `weekly_volume` | SI and set count per muscle group per training week, newest week first. Warm-up sets are not counted. Week start is the Monday of that week. Use this to see whether a muscle group's stimulus is rising or falling over time. Beside it, fractional gives the same working sets counted by the exercise library: each primary muscle of an exercise's entry gets 1 set and each secondary muscle 0.5 (Pelland et al., Sports Medicine 2025), with SI weighted the same way; an exercise without an entry gives 1 set to its exercise group. Muscles the library marks uncertain are listed under uncertain and not counted. The existing fields are unchanged. |
| `volume_by_muscle` | Total SI and set count per muscle group, optionally within an inclusive date range. Warm-up sets are not counted. Sets whose exercise has no group yet are reported under an empty muscle_group. Beside it, fractional gives the same working sets counted by the exercise library: each primary muscle of an exercise's entry gets 1 set and each secondary muscle 0.5 (Pelland et al., Sports Medicine 2025), with SI weighted the same way; an exercise without an entry gives 1 set to its exercise group. Muscles the library marks uncertain are listed under uncertain and not counted. The existing fields are unchanged. |

`weekly_volume` returns `weeks` and `volume_by_muscle` returns `volume` exactly
as before. Both now also return an optional `fractional` object (#137), left
out only when the store keeps no exercise library:

- `method` and `source`: how the credit is counted and the study it follows.
- `weeks[]` (`weekly_volume`) with `week_start`, `groups[]` and `uncertain[]`,
  or `groups[]` and `uncertain[]` directly (`volume_by_muscle`).
- `groups[]`: `muscle_group`, fractional `sets` and `total_si`, rounded to 0.1,
  largest first. `muscle_group` is empty for sets with neither a library entry
  nor an exercise group, as in the existing totals.
- `uncertain[]`: credit not counted because a muscle has no clear group:
  `exercise`, `entry`, `layer`, `source_muscle`, `role`, `candidates`, `sets`
  and `total_si`.

The fractional sets of a group can differ from its existing count in both
directions: a bench press now also credits triceps 0.5, and an exercise whose
entry names another primary muscle than its exercise group moves there.

## Feedback loop

Rate a trained muscle group 0-3 on fatigue, pump and recovery, and get next week's set-count change. This is the part the source spreadsheet automated.


| Tool | What it does |
|---|---|
| `record_feedback` | Rate how one muscle group responded to a session: fatigue, pump and recovery, each 0 to 3. Only rate groups actually trained. This drives next week's set-count recommendation. |
| `volume_recommendation` | Set-count change per muscle group for next week. Weights recent feedback over older rather than reading only the last, reports how many rated sessions it is based on and the resulting confidence, declines to advise on a single rating, and never pushes volume past a recorded MEV or MRV. |
| `set_volume_landmarks` | Record a muscle group's personal weekly-set boundaries: MEV below which it stops growing, MRV above which fatigue outpaces recovery. These are individual, and `volume_recommendation` refuses to push past them. Leave a value at 0 if unknown. |
| `list_volume_landmarks` | List the muscle groups that have personal volume landmarks recorded. |
| `simulate_plan` | What a plan would do to a week of training, before it is created or changed. Give a saved plan_id or unsaved items, and sessions_per_week. Returns weekly working sets and SI per muscle group, each compared with the person's MEV and MRV (below_mev, within, above_mrv, no_landmarks), the weekly totals, and exercises with no muscle group. Deterministic and read-only: it stores nothing and creates no proposal. Call it before proposing create_plan or set_plan_exercise. |

## Catalogue

Per-exercise metadata that persists across sessions.


| Tool | What it does |
|---|---|
| `set_exercise_group` | Assign an exercise to one muscle group so its sets count toward that group's volume. Re-assigning replaces the previous group. |
| `list_exercise_groups` | List every exercise that has a muscle group assigned, ordered by group. |
| `set_exercise_note` | Store a persistent setup reminder for an exercise, such as seat height or grip width. It is shown every time that exercise is logged. An empty note removes it. |
| `list_exercise_notes` | List every exercise that has a setup note. |

## Exercise library

Which muscles an exercise trains, primary and secondary, for the fractional
volume (#137). Each person has their own library over two shared layers.

| Tool | What it does |
|---|---|
| `lookup_exercise` | Look an exercise name up in the exercise library and return its muscles: primary ones count 1 set, secondary ones 0.5. Lookup order: the person's alias, then their own entries, then the curated entries, then the public free-exercise-db dataset; the result names the layer and whether the match was by alias, exact name or normalized name (case, accents and punctuation ignored). A public muscle without one clear muscle group is marked uncertain with its candidate groups and is never counted. When nothing is found, found is false and candidates lists similar entries; pick none without asking the person. |
| `list_exercises_needing_mapping` | List the person's logged exercises (working sets) whose muscles the library cannot give: not_found (no entry; their sets still count toward their set_exercise_group group, with up to 3 candidates) and uncertain (the entry has a muscle without one clear group). Fix one with set_exercise_alias or save_exercise_entry. |
| `save_exercise_entry` | Save an exercise to the person's own library with its primary and secondary muscle groups. Their own entry wins over the curated and public ones from then on, for them only; correcting a curated or public entry copies it into their library and records which entry it was based on. Use it for an exercise lookup_exercise does not find or marks uncertain, after asking the person. |
| `set_exercise_alias` | Make a name the person logs mean a library entry, e.g. 'press banca' for 'Barbell Bench Press - Medium Grip', for them only. The alias follows the entry: if they later correct it, the alias reaches the correction. It does not rename logged sets or change set_exercise_group. |

The two writes are proposals like every other write, and undo restores the
entry or alias as it was.

**Lookup order.** The first layer that has the name wins:

1. `alias`: the person's alias for the name, resolved to the entry it names;
2. `own`: the person's own entries (`exercise_library_entries`, migration 031);
3. `curated`: hand-checked entries in
   `internal/training/exerciselibdata/curated.json`, changed only by pull
   request, each with the reason it was checked;
4. `public`: free-exercise-db, 876 exercises, vendored and pinned (see
   [Sources and licenses](#sources-and-licenses));
5. not found: `found` is false and `candidates` lists up to 10 entries that
   share words with the name, own entries first among equals. Nothing is
   picked: the coach asks, then sets an alias.

`match` says how: `alias`, `exact` (the same name after trimming, lower case
and collapsed spaces) or `normalized` (also ignoring accents and punctuation,
so `Jalón al pecho` matches `jalon al pecho` and `Bench Press - Powerlifting`
matches `bench press powerlifting`). An entry's `layer` is where it came from.

**Copy on write.** `save_exercise_entry` with the name of a curated or public
entry stores the person's own copy with `based_on` (`curated:<name>` or
`public:<id>`); from then on it wins for them. Nobody else sees it: own entries
and aliases are per person.

**Aliases** point at an entry name and are resolved on every read, so an alias
to a public entry reaches the person's correction once they save one. An alias
target must be found without aliases, so aliases never chain.

**Muscle mapping.** free-exercise-db names 17 muscles. Twelve map to one muscle
group (`abdominals` abdomen, `adductors` aductor, `biceps`, `calves` gemelo,
`chest` pecho, `forearms` antebrazo, `glutes` gluteo, `hamstrings` isquios,
`lats` espalda, `quadriceps` cuadriceps, `traps` trapecio, `triceps`). Five do
not, and are marked `uncertain` with their candidate groups instead of guessed:
`shoulders` does not tell the heads apart (hombro anterior, lateral or
posterior); `middle back` is rhomboids and middle trapezius (espalda or
trapecio); `lower back` (spinal erectors), `abductors` and `neck` have no group
of their own (nearest: espalda, gluteo, trapecio). Of the 876 public entries, 369 map
cleanly and 507 carry at least one uncertain muscle. An uncertain muscle never
counts toward a group; `list_exercises_needing_mapping` lists the logged
exercises it affects.

**Per exercise, not per machine.** The library describes the movement. The
set's `equipment` stays the machine identity, and the library never reads or
changes it. `set_exercise_group` keeps its meaning: it is the one group an
exercise without a library entry counts toward.

## Coach memory

Durable facts about the person that outlive chats. Chats are deleted after 90
days; memory stays until the person deletes it in the app (`/memory`, linked
from the review page). Each fact has a type (`preference`, `limitation`,
`goal`, `equipment`, `worked`, `note`), a title, a body, dates and a **topic
key** that names it. Storage, search, candidates, review dates and merges are
deterministic SQLite (FTS5); the coach spends tokens only on judgement. Every
memory write is its own action type, so letting the coach remember without
asking never lets it forget, retire or merge facts without asking.

| Tool | What it does |
|---|---|
| `memory_save` | Save a fact, or update the one with the same `topic_key`. Changed text keeps the previous version as a revision; the same text again only restarts the review cycle. Saving a forgotten or superseded key brings the fact back. The result lists `candidates`, similar active facts not judged yet, and `judgment_required`. A proposal. |
| `memory_judge` | Record how a saved fact relates to one candidate: `supersedes`, `conflicts_with`, `related`, `compatible`, `scoped` or `not_conflict`. Each relation is judged once. A proposal. |
| `memory_confirm` | The person confirmed a fact still holds: restart its review cycle. A proposal. |
| `memory_forget` | Forget a fact: it leaves context and search but is kept, for the person to see or delete for good. A proposal. |
| `memory_consolidate_preview` | What merging 2 to 10 facts into one would do, with a `fingerprint`. Writes nothing. |
| `memory_consolidate` | Merge exactly as previewed, with the preview's `fingerprint`. The sources are forgotten, pointing to the survivor. A fact changed since the preview refuses it. A proposal. |
| `memory_search` | Find facts by words (every word must match as a prefix, accents ignored), optionally of one type, in a `view`: `active` (default), `due` or `superseded`. With no words it lists them. Forgotten facts are never returned. |
| `memory_get` | One fact by `memory_id`, whole, with its earlier versions. |
| `memory_context` | What to know at the start of a chat: up to 20 active facts, pinned first then most recent, bodies cut to 280 characters; `due`, up to 5 facts to check with the person now; and `conflicts`, how many conflicts wait in the app. |

### How memory evolves

- **Revisions.** A topic key keeps one row and a revision count; each earlier
  text is kept, numbered from 1.
- **Conflict surfacing.** Saving looks for active facts sharing a word of three
  or more letters with the title, common Spanish and English stopwords aside
  (at most 5), and returns them as candidates.
  `supersedes` retires the old fact from context and search at once.
  `conflicts_with` keeps both and lists them in the app's Conflicts view,
  where the person keeps the newer, the older or both.
- **Review cycle.** Saving, editing or confirming sets the next review date by
  type: limitation 14 days, goal 30, worked 60, note 90, equipment and
  preference 180. `memory_context` lists the facts past it, so the coach asks
  ("¿sigue molestando el hombro?") and confirms, updates or forgets them. The
  app lists them under "Por revisar".
- **Forget.** The coach only forgets softly. Deleting for good, one fact or
  everything (typing «borrar»), is the person's, in the app.
- **Consolidate.** Preview, then confirm with the fingerprint. The survivor may
  take one source's key, which then stays as that fact's new revision; never
  the key of another active fact.

No memory tool takes a user id: whose memory it is comes only from the
authenticated request. A `user_id` sent as an argument is never read.

The in-app coach also gets memory without calling a tool: the host writes
`memory_context` into a fresh engine's first turn, proposes a deterministic
summary when a workout ends, and proposes the items of a reply's closing
`## Aprendizajes` block. All three are `memory_save` proposals, never direct
writes; see [Memory hooks](coach.md#memory-hooks).

## Skills

Training-theory guidance the agent loads on demand instead of carrying it in
every prompt. Skills ship inside the binary, are the same for every person and
are read-only. Loading one registers no tool, hides none and grants nothing:
the tool list is the same before and after. Format, review status and how to
add one: [skills.md](skills.md).

| Tool | What it does |
|---|---|
| `list_skills` | List the training-theory skills you can load: id, version, one-line summary, the situations each is for and its review status. Metadata only; call load_skill for one skill's instructions. The same for every person. |
| `load_skill` | Load one skill whole: its instructions (sections Evidence, Heuristics, Person-specific assumptions and Decisions), limitations, the read tools it relies on and its references. Guidance only: it changes no tool and no data. An unknown `id` is a not-found error that points back to `list_skills`. |

## Session context

Context the person chooses to record (#6): their bodyweight, one entry a day,
and free notes on a session. Both are optional, neither is needed to log a set,
and no tool judges them: there is no target, goal or verdict.

| Tool | What it does |
|---|---|
| `log_bodyweight` | Record the person's bodyweight for a day, only when they say it. One entry per day: logging a day again replaces its entry (the result shows the one replaced). Give the number and unit exactly as the person said them; it is stored in kg and shown in the person's unit. The weight must be 20 to 400 kg. |
| `bodyweight_history` | Get the person's bodyweight entries in a date range (default the last 90 days, at most 366), oldest first, with plain numbers about them: count, first, last, min, max, change (last minus first) and the mean of the 7 days ending on the last entry. Weights are in kg and in the person's unit. Report the numbers; they carry no target or verdict. |
| `delete_bodyweight` | Delete the person's bodyweight entry of one day, for an entry recorded by mistake. A day with no entry is not found. |
| `set_session_notes` | Set the free notes of a session: context the person gives, such as how they slept, a niggle or a packed gym. Replaces the notes it had; empty notes clear them. get_session returns them. |

Bodyweight is stored per person and day (migration 032, table `bodyweight`,
primary key `user_id, date`), always in kg rounded to 2 decimals, with an
optional note of at most 500 characters. `log_bodyweight` takes `unit` (`kg` or
`lb`) because the person may say either; a missing `date` is fixed to the
server's today when the proposal is made, so accepting it later still records
the day it was said. 20 to 400 kg is only a guard against typos. Every summary
in `bodyweight_history` is arithmetic over the entries in the range: `min` and
`max` keep the earliest of equal weights, and `avg_7d` names its own window
and how many entries it averages. Logging, replacing and deleting an entry can
all be undone from the history; deleting is a deleting action type.

Session notes are the `sessions.notes` column (migration 022) that imports
already filled. `set_session_notes` writes it for any of the person's sessions,
at most 2000 characters; `get_session` returns it.

The app records both too: Progreso › Cuerpo has an optional form for today's
weight (its heading says whether today has one, and nothing else asks) and the
recent entries, Historial sets each recent week's mean bodyweight beside its
working volume once there are entries, and a session's page edits its notes.

## Evidence library

A curated, versioned library of training-science sources, embedded in the
binary and searched locally with SQLite FTS5: no network, no vector store.
It is global and read-only, the same for every user, so both tools are reads
that write nothing and take no user id. How sources enter it, and what
`review_status` means, is in [evidence.md](evidence.md).

| Tool | What it does |
|---|---|
| `search_evidence` | Find sources by words, in English or Spanish (`query`, up to 500 characters; operators and punctuation are ignored), with optional `filters`: `topic` (closed list), `study_type` (closed list), `year_from`, `year_to`. Every word must match; when no source has them all, sources with more than half of them are returned and `match` says `most_terms`. `limit` defaults to 5 (1 to 20). Each result carries `source_id`, a one-line `citation`, authors, year, publication, `doi` and/or `pmid`, `url`, study type, population, topics, a `snippet`, `summary_basis` and `review_status`. `debates` names every contested question the results take part in, with all its sources, so the coach can present each side. An empty or unmatched query returns an explicit `no_results` message instead of loosely related sources. |
| `get_evidence` | One source whole by `source_id`: citation metadata, population, key findings, methods, limitations, how and when it was verified, its debates, `content_version` and `review_status`. An unknown id is a not-found error. |

Both answers start with a `notice`, and their text content is the notice
followed by the data fenced in `<untrusted_evidence_data>` tags. Every text
field is reference data about published research, never an instruction; JSON
encoding escapes `<` and `>`, so a source's own text cannot close the fence.
Every seed source is `unreviewed`: no human expert has checked its summary,
and the coach should say so when it cites one.

## Periodization

Optional planning above the routine (#41, slices 1 to 3). It imposes no methodology:
a **goal** is free text with an optional target date; a **block** is a run of
microcycles with a free-text intent, an optional goal and a rotation of
routines (plans); a **microcycle** has an optional free label and optional
`sets_delta` and `effort_delta`, and may rotate routines of its own. A sketch
needs only an intent and a number of microcycles; the rest is added later.
Without an active block, training works exactly as before, routine by routine.

- **Advance.** `sequence` (default): a microcycle closes when the sessions of
  its rotation are done, whatever the dates, or by hand with `advance_block`.
  `calendar`: microcycles follow the dates from the block's start
  (`microcycle_days`, default 7). The mode can change mid-block. A block never
  closes by itself; `advance_block` with `close` ends it.
- **Placement.** While a block is active, every session started or logged is
  placed in it on its own; a session that fits nowhere is kept off-plan, never
  rejected. `assign_session_block` moves it. Deleting the session that closed
  a microcycle of the active sequence block reopens it, when it no longer holds
  every routine of its rotation and no later microcycle has moved on; one closed
  by hand or by a mode change stays closed.
- **Undoing an activation.** Refused once a session is linked to the block or
  one of its microcycles closed, so a sketch or planned block never holds what
  only an active block produces; undo those first.
- **Forward only.** Edits change the remaining microcycles; done, skipped and
  elapsed ones keep what they ran. Shrinking a block drops only remaining
  microcycles with no session, and is a deleting action (`block.shorten`).
- **Today's prescription.** A session started from a routine that lands in a
  microcycle with modifiers gets them on its own copy of the routine
  (`session_items`): sets + `sets_delta`, never below 1 or above 20; target
  RPE + `effort_delta`, only where the routine sets one, kept within 1-10. The
  routine is never changed and past sessions are never rewritten, even when the
  microcycle's deltas are edited later. `start_session` returns
  `block_modifiers` (block, microcycle, deltas, the rule, and per exercise the
  base value, the result and whether a bound held it), and the session keeps it
  (migration 034 `sessions.block_modifiers`). A free-form or off-plan session,
  one outside an active block, or a microcycle without modifiers starts exactly
  as before. Applying them is best effort: if it fails, the session still starts
  with the routine's values and no `block_modifiers`.
- **How a microcycle closed.** `closed_by` is `placement` (its last routine was
  done), `hand` (`complete_microcycle` or `skip_microcycle`) or `mode_change`
  (its dates passed when the block went back to sequence); absent for
  microcycles closed before migration 034.
- **Not yet.** There is no progression rule.

| Tool | What it does |
|---|---|
| `training_status` | Today's planning: active goals, the active block and its current microcycle, with routines done and due and its modifiers, and `next_prescription`: the first routine due with those modifiers applied to the routine as it is today, base and result per exercise. Reads only. |
| `list_blocks` | Every goal and block with its progress: microcycles done, skipped and remaining, the current one, and sessions off-plan. |
| `get_block` | One block in full: rotation, every microcycle with its state (`done`, `skipped`, `current`, `upcoming`, `elapsed`), dates in calendar mode, sessions placed and off-plan. |
| `set_goal` | Create or edit a goal (text, `target_date`, status `active`, `achieved` or `dropped`). A proposal. |
| `save_block` | Create or edit a block: intent, goal, advance mode, `microcycle_days`, `microcycle_count`, status `sketch` or `planned`, rotation, and per-microcycle label, deltas and rotation. A proposal; shrinking waits for the person under `accept_except_delete`. |
| `advance_block` | `activate` a block (one active at a time, from `start_date` or today), `complete_microcycle` or `skip_microcycle` on a sequence block, or `close` it. A proposal. |
| `assign_session_block` | Put a session in a microcycle of any block, or off-plan. Never closes or reopens a microcycle. A proposal. |

---

## Design notes

Why the surface looks like this. A reference table says what each tool
does; these are the decisions behind them.

A plan carries free-text **notes** at two levels: the routine as a whole (its intent, the phase of the block) and each exercise inside it (cues, tempo, whether to push the last set). Both are returned by `get_plan` and `list_plans`, and both are copied into the session, so `session_progress` exposes the intent behind the numbers while training — an assistant reading the MCP sees why, not just what. `update_plan` edits them without touching the exercises, and `set_plan_exercise`, `remove_plan_exercise` and `move_plan_exercise` edit a saved routine's exercises and their order — the order is the order the session is performed in.

Plan items can share a **superset label**, marking exercises done back to back. Logging never states it: a set is stamped with the label its exercise carries in the session's plan, so entry stays the same size. `set_exercise_note` stores a persistent setup reminder per exercise — seat height, grip width — shown every time that exercise comes up.

Every set has a **category**: `working` (the default) or `warmup`. `log_sets` (per set), `log_set`, `add_set` and `update_set` accept `category`, and every set they return reports it. A warm-up is recorded and shown (marked W in the app), but it never counts: its SI is 0, and it is left out of set counts (`list_sessions`, `weekly_volume`, `volume_by_muscle`), planned-versus-done progress (`session_progress`, `live_workout`), the best set and est. 1RM in `exercise_history` (a warm-up there has `est_1rm` 0), `suggest_load`, `volume_recommendation` and `performance_dropoff` (where it is excluded with reason `warmup`). The agent should use `category`, never the technique, for warm-ups; a warm-up word sent as the technique (`warmup`, `warm-up`, `calentamiento`) is still turned into the category. Migration 023 converted sets recorded that way before the category existed. These are agent writes like any other, so they go through proposals unchanged.

Weights are stored to 2 decimals: every write rounds them, the importer rounds after converting pounds, and migration 023 rounded older stored weights (moving each by under 0.005 kg), so the app never shows or pre-fills values like `65.77089365`.

A set can carry an optional **intensity technique** (`drop set`, `rest-pause`, `myo-reps`, `sin parar`…). It is a property of the set, not a separate exercise, so the volume still counts toward the right muscle group while progression on the base movement stays comparable — a set with a technique never becomes the exercise's record.

A set can also carry an optional **equipment**: the machine or equipment it was done on, as free text (`technogym pec fly`, `hammer row`). `log_sets` (per set), `log_set`, `add_set` and `update_set` accept it; `update_set` with an empty string clears it. It is normalized like exercise names (trimmed, inner spaces collapsed, lower case), at most 80 characters, and stored as NULL when not stated; past sets are never backfilled. Set outputs, `get_session` and `exercise_history` return it, and the phone shows it as a tag on the set, so a typo that splits one machine in two is visible and can be fixed with `update_set`. `performance_dropoff` compares a set only with sets on the same equipment as the reference (`excluded_reason` `different_equipment`); `exercise_history`'s best set and `suggest_load` still rank all equipment together.

`log_set` is the simplest way to record training: it takes an exercise, weight, reps and RPE, finds or creates today's session, and can record several identical sets at once. It needs no session id, so conversational logging is one call. `start_session`, `add_set`, `update_set`, `delete_set`, `get_session` and `list_sessions` remain for explicit control. Exercises are trimmed/lowercased, positions remain dense, and totals are recalculated from stored SI values.

`create_plan`, `list_plans`, `get_plan` and `delete_plan` manage reusable workout plans: an ordered list of exercises with a target set count and an optional rep range and RPE. Load is deliberately not planned — the prescription is effort and reps, and the weight that meets it is discovered at the gym. `start_session` takes an optional `plan_id`, which **copies** the plan into the session. From then on the session owns its prescription: `add_session_exercise`, `adjust_session_exercise` (sets, rep range, RPE, skip), `swap_session_exercise` and `remove_session_exercise` change today only and never edit the template, while editing a template never rewrites a past session. `session_progress` reports planned versus completed sets per exercise, listing anything done off-plan with `target_sets` 0 so it stays visible. `save_session_as_plan` promotes an adjusted session into a new reusable plan.

`delete_session` removes a session and every set in it, reporting how many were destroyed. `exercise_history` returns one exercise's sets newest first with each set's estimated 1RM plus its all-time best, so progression can be judged in one call instead of reading every session. The estimate is Epley, `w*(1+reps/30)` rounded to 0.1 kg, except that a single (1 rep) is the lifted weight itself rather than the formula's 1.033x; the all-time best is ranked the same way. It is computed on read and never stored. `weekly_volume` buckets SI and working-set count per muscle group by training week.

`suggest_load` advises the next working weight for an exercise, judged against the rep range and RPE it is prescribed at: hit the top of the range below target RPE and it says add weight; fall short or exceed the RPE and it says drop it. Sets carrying an intensity technique are ignored, since a drop set is not comparable to a straight set, and so are warm-ups. Like every other recommendation here it is advisory and explains itself; it never edits a plan. With no previous set or no prescription to judge against it declines rather than guessing.

`performance_dropoff` reads one session and, per exercise, reports the apparent drop of each set against the first comparable one, with every assumption stated. It is pure arithmetic in `internal/training`, so it costs no model tokens; the contract (comparable sets, estimators, confidence, abstention) is in [performance-engine.md](performance-engine.md).

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

`simulate_plan` shows what a plan would prescribe before anyone accepts it (#46), the way Liftosaur's `run_playground` lets an agent try a program. It is arithmetic in `training.SimulatePlan`, so it costs no model tokens and writes nothing:

- Every plan item is working sets (a plan prescribes no warm-ups), counted one
  per target set exactly as `weekly_volume` counts logged working sets, and
  weighted with the same `SetSI` of its target RPE, or of the imputed RPE 8 when
  none is prescribed.
- A plan is one session; `sessions_per_week` (1 to 7, default 1) multiplies it
  into a week. Only muscle groups the plan trains are reported, in catalogue
  order, through the person's own exercise groups. Exercises with no group still
  count in the totals and are listed in `unmapped_exercises`.
- Each group's weekly sets are compared with that person's landmarks using the
  same boundaries `volume_recommendation` clamps to: under MEV is `below_mev`,
  over MRV is `above_mrv`, on or between them is `within`, and a group with no
  landmarks is `no_landmarks`. There is no MAV landmark stored, so none is
  compared.
- The app shows the same simulation, computed by the server, on pending
  `create_plan`, `set_plan_exercise` and `remove_plan_exercise` proposals: on
  the review page, and on the routine draft in Planificar. An edit is simulated
  on top of the saved plan without changing it.

`set_exercise_group`, `list_exercise_groups`, and `volume_by_muscle` add per-muscle-group volume. Each exercise maps to exactly one muscle group, so group SI is a true partition of session SI — every set counts once and the group totals add up to the session total. Sets whose exercise has no mapping are reported under an empty group rather than dropped, so gaps in the catalogue stay visible.

The [exercise library](#exercise-library) adds the fractional view beside that partition instead of replacing it: an exercise trains more than one muscle, and the per-group count stays so the two can be compared (#137). The fractional credit is not a partition: a set can credit several groups, and uncertain muscles credit none.

## Sources and licenses

| What | Source | License | Pinned version |
|---|---|---|---|
| Public layer of the exercise library | [free-exercise-db](https://github.com/yuhonas/free-exercise-db) `dist/exercises.json`, 876 exercises, trimmed to `id`, `name`, `primaryMuscles`, `secondaryMuscles`, `equipment` and `category` | [Unlicense](https://unlicense.org) (public domain); the license text is vendored as `internal/training/exerciselibdata/LICENSE-free-exercise-db.md` | commit `f00c92c7dcf1216a928a52c3706c7ce8e2f71ed5`; upstream file SHA-256 `5bb747e3fc658f095a60dcbf6d53c96627acdcc6ffb6fffde86f7e26995d40bf` |
| Fractional volume (primary 1, secondary 0.5) | Pelland et al., *Sports Medicine* 2025, DOI [10.1007/s40279-025-02344-w](https://doi.org/10.1007/s40279-025-02344-w) (preprint DOI 10.51224/SRXIV.460) | cited, nothing copied | n/a |

- The vendored file is written only by `cmd/exerciselib-import`, which
  records the commit and the upstream SHA-256 inside it; a test checks the
  file is in exactly the importer's form. How to re-import is in
  `internal/training/exerciselibdata/README.md`. The download is treated as
  untrusted input: parsed as JSON and checked, never run.
- Pelland et al. count a direct set as 1 and an indirect set as 0.5, the
  method with the strongest relative evidence in their meta-regressions, and
  give no finer per-exercise weights. They weigh sets; weighting SI the same
  way (`total_si`) is this server's extension, and `method` says so.
- wger (CC-BY-SA) and RepDB (its terms forbid republishing the data) were
  considered in #137 and not used.
