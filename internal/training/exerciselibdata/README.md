# Exercise library data

The two shared layers of the exercise library (#137), embedded in the binary by
`internal/training/exerciselib_data.go`. Neither holds anyone's data; a
person's own entries and aliases live in the database (migration 031).

| File | What it is | Edited by |
|---|---|---|
| `free-exercise-db.json` | Public layer: [free-exercise-db](https://github.com/yuhonas/free-exercise-db), trimmed to `id`, `name`, `primaryMuscles`, `secondaryMuscles`, `equipment` and `category` | `cmd/exerciselib-import` only |
| `LICENSE-free-exercise-db.md` | free-exercise-db's license (the Unlicense), copied unchanged from the pinned commit | never |
| `curated.json` | Curated layer: hand-checked entries in `training.MuscleGroups`, each with the reason it was checked | pull request |

## Pinned version

- Repository: https://github.com/yuhonas/free-exercise-db (license: Unlicense, checked on the repository's license API and the `LICENSE.md` file at the pinned commit)
- Commit: `f00c92c7dcf1216a928a52c3706c7ce8e2f71ed5` (2026-09-27)
- Upstream file: `dist/exercises.json`, 876 exercises, SHA-256 `5bb747e3fc658f095a60dcbf6d53c96627acdcc6ffb6fffde86f7e26995d40bf`
- Upstream `LICENSE.md` SHA-256: `6b0382b16279f26ff69014300541967a356a666eb0b91b422f6862f6b7dad17e`

The commit and the upstream SHA-256 are also recorded inside
`free-exercise-db.json`.

## Re-importing

Treat the download as untrusted input: it is parsed as JSON and checked, never
run.

1. Download `dist/exercises.json` and `LICENSE.md` at a pinned commit into a new,
   empty directory outside the repository.
2. Check the repository is still under the Unlicense.
3. Run, from the repository root:

   ```sh
   go run ./cmd/exerciselib-import -in /path/to/exercises.json \
     -commit <40-character sha> \
     -out internal/training/exerciselibdata/free-exercise-db.json
   ```

The importer refuses a muscle name that `training.PublicMuscleMap` does not
know, so a new upstream name is mapped by hand first. Update the pinned version
above and in the "Sources and licenses" section of `docs/tools.md`.

## Muscle mapping

`training.PublicMuscleMap` maps the 17 public muscle names to
`training.MuscleGroups`. Twelve have one target; five (`shoulders`,
`middle back`, `lower back`, `abductors`, `neck`) do not and are flagged
`uncertain` with the groups they could mean. An uncertain muscle never counts
toward a group; a person corrects the entry with `save_exercise_entry`.
