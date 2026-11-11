# Importing history from Hevy and Strong

Switching from another logger should not cost the history. The web app's
`/import` page (linked from the history page) takes a workout CSV exported from
Hevy or Strong, shows what it would add, and imports it when the person confirms.
It is the person's own action, so it applies directly instead of becoming a
proposal. Nothing is sent to a model: parsing, name matching and duplicate
detection are deterministic code in `internal/importer`.

## Flow

1. **Upload.** `POST /import/preview` reads the file. It writes nothing.
2. **Preview.** The page shows the new sessions, sets and exercises, how many
   sets have no RPE, which rows were left out and why, the sets already
   imported or already in the history, and one row per exercise name in the
   file with the name it will get here.
3. **Map names.** Each of the app's names can be given a name the person
   already uses (the field autocompletes from them). "Actualizar vista previa"
   recomputes the preview with the new names.
4. **Confirm.** `POST /import/apply` plans the import again from the same file
   and stores it in one transaction: all of it or nothing.

Between the preview and the confirmation the server keeps nothing: the file
travels back in the form, gzip-compressed and base64-encoded. The confirmation
parses and plans it again, so what is stored is what the plan says at that
moment, and a tampered payload is just another file the same person uploaded.

## What is imported

| Source data | Here |
|---|---|
| A workout (start time and title) | One session on that date, `started_at` set to the workout's start in the app's own clock |
| Workout title, description or workout notes, per-exercise notes | The session's `notes`, shown on its page |
| Exercise name (at most 120 bytes; a longer one is clipped and not remembered) | The chosen name; otherwise the name remembered from an earlier import; otherwise a name already in use that matches ignoring case and accents; otherwise the app's name in lower case, as a new exercise |
| Weight in kg or lb | Kilograms, rounded to 0.01 (1 lb = 0.45359237 kg) |
| Reps | Reps |
| RPE 1 to 10 | RPE, as measured |
| No RPE | RPE 8 with `rpe_imputed` set, exactly like a set logged without one ([SI formula](si-formula.md)): one plain SI unit, shown as `@?`, never read as a measurement |
| Drop set (Hevy `dropset`, Strong `D`) | Technique `drop set` |

Left out, and counted in the preview: warm-up sets (Hevy `warmup`, Strong
`W`), rows without weight or reps (cardio, timed holds, bodyweight sets, which
the app cannot record as sets yet), Strong's `Rest Timer` and `Note` rows, and
rows whose date or numbers cannot be read. Neither app exports RIR; an RPE
outside 1 to 10 is treated as missing.

## Idempotency and duplicates

- Every set gets a natural key: the app, the workout's start, its title, the
  exercise as the app named it, and the set's order within that exercise in
  that workout. Warm-ups count in that order, so changing what is skipped never
  renumbers a set. `imported_sets` keeps the keys per user; a set whose key is
  there is never imported again, even if the set it created was deleted later.
  Importing the same file twice therefore adds nothing, and a later export of
  the whole history adds only the new workouts.
- The file's SHA-256 is stored with each import (`imports.content_hash`); the
  preview says when the very same file was imported before, and when.
- A set the history already holds (same day, exercise, weight and reps), logged
  some other way, is not imported either. This covers history entered by hand
  before this feature, such as the 2025 Hevy backfill whose unrated sets were
  marked imputed by migrations 011 and 012. Sets from earlier imports use up
  their match, so they never hide a new set that happens to repeat them.
- An import with nothing new stores nothing, not even an empty `imports` row.

## Limits and safety

- One file is at most 960 KB and 10,000 rows: the whole request must fit the
  server's 1 MiB body limit. A longer history is exported and imported in parts.
- The routes sit behind the same login and CSRF check as every other web route,
  and every read and write is bound to the signed-in user: another user's
  imports, keys, names and history never shape the preview or the import.
- Agents cannot trigger an import. There is no MCP tool for it.

## Formats

The official help pages confirm both exports exist but do not document their
columns:

- Hevy, export and Strong import: <https://help.hevyapp.com/hc/en-us/articles/38001424401943-How-to-Import-Strong-App-CSV-Files-and-Export-Your-Data-in-Hevy>
  (Profile › Settings › Export & Import Data › Export Data › Export Workouts).
- Strong, export: <https://help.strongapp.io/article/235-export-strong-data>
  (Settings › Export Strong Data).

The columns below come from real exports published by third parties, not from
the vendors. No sample from the owner's own account was available when this was
written; a real file from each app is the next thing to test against.

- Hevy, from published exports such as
  <https://raw.githubusercontent.com/matanabudy/workout-data-sync/main/examples/hevy_export_sample.csv>:
  `title,start_time,end_time,description,exercise_title,superset_id,exercise_notes,set_index,set_type,weight_kg,reps,distance_km,duration_seconds,rpe`,
  with `weight_lbs` and `distance_miles` when the app is set to imperial, and
  dates like `22 Dec 2025, 08:00` (Spanish month abbreviations are read too).
- Strong, classic export, as documented in
  <https://raw.githubusercontent.com/smyrick/sugarwod-to-hevy/main/docs/STRONG_FORMAT.md>:
  `Date,Workout Name,Duration,Exercise Name,Set Order,Weight,Reps,Distance,Seconds,Notes,Workout Notes,RPE`,
  dates like `2024-03-07 18:05:00`, and no weight unit: the page asks which
  unit Strong was set to.
- Strong, newer export: semicolon-separated, with a byte-order mark, extra
  columns such as `Workout #` and the unit in the weight header, as handled by
  <https://raw.githubusercontent.com/DuarteSantos8/openGym/main/frontend/src/lib/import-csv.js>.
  The test fixture for it is a reconstruction, not a real file. Columns are
  matched by name, so their order and extra columns do not matter.

## Storage

Migration 022 adds `imports` (one row per confirmed import), `imported_sets`
(the natural keys, each pointing at the set it created), `import_exercise_map`
(the person's name for each of a source's exercise names) and `sessions.notes`.
`training.ImportStore` is a port of its own, reached through
`Store.ImportsFor(userID)`, with its own two-user isolation tests.
