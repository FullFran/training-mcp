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
| `reject_always` | Rechazar siempre en esta sesión | Reject it and store a **denial** for its action type, limited the same way. A denial wins over any grant, accept-all included. |

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

### Accept-all

A per-account switch that accepts every proposal, in every session and in
none, including action types added later, like Claude Code's
`--dangerously-skip-permissions`. It is modelled as an **account-level host
grant**: it is set only from the person's own settings on `/review`, never by an
agent, and every proposal it accepts is still recorded, with a decision record
naming the `accept_all` grant. A session denial still wins over it. Turning it
off leaves the session grants and denials as they were.

Defaults: every account that existed when migration 014 ran keeps accept-all
on, so its agents behave as before plus the two fields. Accounts created later
start asking for everything. Changes a person makes in the PWA apply directly
and are not proposals.

Migration 018 dropped every answer stored before answers became
session-limited (permanent "always" and 12-hour "session" answers): none had a
session or an origin, so none could be kept under HCS-0005 R006 and R008. Each
dropped answer has a `revoked` decision record (decider `policy`
`HCS-0005-R008`, channel `migration:018`), and the person is asked again.
Accept-all values were not touched.

### Decision records (HCS-0005 section 3.3)

Every decision on a proposal writes a decision record before it takes effect:
`time`, `permission` (the action type), `outcome` (`allow`, `deny`, or
`revoked` when a grant or denial is forgotten), `operation` (`proposal:<id>`),
`session`, `decider` (`subject` `account` for the person's own answer, `grant`
`rule:<id>` or `accept_all`, `denial` `rule:<id>`, or `policy`), `channel`
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
| R007 `reject_always` is a session-limited denial that wins over a grant | Met, including over accept-all. |
| R008 no match across sessions or without a session | Met. |
| R009 nothing outside the host remembers answers | Met: the agent never sees or answers a proposal, and the engine and clients only get `accepted`, `pending` or `rejected` results. |
| R014 fail closed | Met: a proposal is applied only on an explicit allow (person, grant or accept-all); a pending one waits with nothing applied, and a reject, an error or an unwritable decision record applies nothing. |
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
| `rest.set` | `set_rest_target` |
| `memory.save` / `memory.forget` | `memory_save` / `memory_forget` |
| `memory.judge` / `memory.confirm` / `memory.consolidate` | `memory_judge` / `memory_confirm` / `memory_consolidate` |

A proposal is claimed before it is applied, so two answers cannot apply it
twice. If the process stops between applying and recording the outcome, the
proposal stays `applying` and shows in the history as such; check the data
before proposing it again.

Client handling (manual check, pending): ChatGPT and Claude must relay
`pending` as "waiting for your approval in the app". Record the result of that
check here once it has been done against production.

## Logging sets

The everyday path. `log_sets` is the one to reach for: it needs no session id, its sets may differ, and repeating a call with the same `idempotency_key` stores nothing new, so a client that lost the response can safely retry.


| Tool | What it does |
|---|---|
| `log_sets` | Record the sets of one exercise into today's session in a single call, creating the session if needed. The sets may differ, as in '12, 12, 10 and 8 reps'. All are stored or none. Omit rpe unless the user stated it. Repeating a call with the same idempotency_key returns the original result with replayed true and stores nothing new, so retrying is always safe. Prefer this over log_set. |
| `log_set` | Record one or more identical sets into today's session, creating that session if it does not exist yet. It needs no session id but is not safe to retry, since repeating it records the sets again. Prefer log_sets, which is. |
| `start_session` | Start a training session on an optional date, optionally following a plan. Omit date to use the server's current local date. |
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
| `set_rest_target` | Set the person's target rest between sets, 30 to 900 seconds (120 until set). The phone's rest timer uses the same value. |
| `session_progress` | For a session following a plan: planned versus completed sets per exercise, with the prescribed rep range and RPE. Exercises done off-plan are listed with target_sets 0. |
| `add_session_exercise` | Add an exercise to today's session plan. If it is already there, target_sets is replaced and every optional field you omit (rep range, RPE, superset, notes) keeps its current value; sending either rep bound replaces the whole range. To clear a field, remove the exercise and add it again. Adjusting a session never edits the plan it was started from. |
| `adjust_session_exercise` | Change one exercise of today's session: its set count, rep range, RPE, or whether it is skipped. Omitted fields stay as they are. Use when the session does not go as planned. |
| `swap_session_exercise` | Substitute one exercise for another in today's session, keeping the same prescription. Use when the planned machine is taken or the movement does not feel right. |
| `remove_session_exercise` | Drop an exercise from today's session plan. Sets already logged against it are kept and reappear as off-plan work. |
| `save_session_as_plan` | Turn a session — including any in-session adjustments and off-plan work — into a new reusable plan. Skipped exercises are left out. |

### Live workout and rest

`live_workout` is what a coach watching the session would see. Rest is derived from the timestamp of the most recently logged set, never stored: `rest.state` is `none` (no set yet today), `resting` (with `remaining_seconds`), `ready` (target reached, with `over_by_seconds`), `superset` (the next exercise of the same round follows without rest; after the last exercise of a round the state is `resting` before the next round) or `idle` (more than 20 minutes since the last set, so not a rest). `next` is the exercise to do next: the next unfinished one of a superset round, else the exercise just worked while it has sets to go, else the first unfinished one in plan order. The target is one value per person, shared with the phone's ±30 s buttons.

## Progression and analysis

Judging progress in one call instead of reading back every session.


| Tool | What it does |
|---|---|
| `suggest_load` | Suggest the next working weight for an exercise, judged against the rep range and RPE it is prescribed at. Advisory only: it explains its reasoning and never changes a plan. Sets carrying an intensity technique are ignored, since they are not comparable to straight sets, and so are warm-up sets. |
| `exercise_history` | All recorded sets of one exercise, newest first, with each set's estimated 1RM, plus the best set ever recorded for it. Use this to judge progression instead of reading whole sessions. |
| `performance_dropoff` | Apparent per-set performance drop within one session, per exercise, computed deterministically from load, reps and stated RPE with named estimators (epley_v1, rir_epley_v1, brzycki_v1). Lists which sets were comparable and why others were excluded (recorded warm-up, technique, no load, probable warm-up), the reference set, each model's drop and confidence, and abstains with fewer than two comparable sets. An unstated RPE never feeds the RIR model. A device-free proxy: not velocity, force or fatigue, and it changes nothing. |
| `weekly_volume` | SI and set count per muscle group per training week, newest week first. Warm-up sets are not counted. Week start is the Monday of that week. Use this to see whether a muscle group's stimulus is rising or falling over time. |
| `volume_by_muscle` | Total SI and set count per muscle group, optionally within an inclusive date range. Warm-up sets are not counted. Sets whose exercise has no group yet are reported under an empty muscle_group. |

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

---

## Design notes

Why the surface looks like this. A reference table says what each tool
does; these are the decisions behind them.

A plan carries free-text **notes** at two levels: the routine as a whole (its intent, the phase of the block) and each exercise inside it (cues, tempo, whether to push the last set). Both are returned by `get_plan` and `list_plans`, and both are copied into the session, so `session_progress` exposes the intent behind the numbers while training — an assistant reading the MCP sees why, not just what. `update_plan` edits them without touching the exercises, and `set_plan_exercise`, `remove_plan_exercise` and `move_plan_exercise` edit a saved routine's exercises and their order — the order is the order the session is performed in.

Plan items can share a **superset label**, marking exercises done back to back. Logging never states it: a set is stamped with the label its exercise carries in the session's plan, so entry stays the same size. `set_exercise_note` stores a persistent setup reminder per exercise — seat height, grip width — shown every time that exercise comes up.

Every set has a **category**: `working` (the default) or `warmup`. `log_sets` (per set), `log_set`, `add_set` and `update_set` accept `category`, and every set they return reports it. A warm-up is recorded and shown (marked W in the app), but it never counts: its SI is 0, and it is left out of set counts (`list_sessions`, `weekly_volume`, `volume_by_muscle`), planned-versus-done progress (`session_progress`, `live_workout`), the best set and est. 1RM in `exercise_history` (a warm-up there has `est_1rm` 0), `suggest_load`, `volume_recommendation` and `performance_dropoff` (where it is excluded with reason `warmup`). The agent should use `category`, never the technique, for warm-ups; a warm-up word sent as the technique (`warmup`, `warm-up`, `calentamiento`) is still turned into the category. Migration 023 converted sets recorded that way before the category existed. These are agent writes like any other, so they go through proposals unchanged.

Weights are stored to 2 decimals: every write rounds them, the importer rounds after converting pounds, and migration 023 rounded older stored weights (moving each by under 0.005 kg), so the app never shows or pre-fills values like `65.77089365`.

A set can carry an optional **intensity technique** (`drop set`, `rest-pause`, `myo-reps`, `sin parar`…). It is a property of the set, not a separate exercise, so the volume still counts toward the right muscle group while progression on the base movement stays comparable — a set with a technique never becomes the exercise's record.

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

`set_exercise_group`, `list_exercise_groups`, and `volume_by_muscle` add per-muscle-group volume. Each exercise maps to exactly one muscle group, so group SI is a true partition of session SI — every set counts once and the group totals add up to the session total. Sets whose exercise has no mapping are reported under an empty group rather than dropped, so gaps in the catalogue stay visible.
