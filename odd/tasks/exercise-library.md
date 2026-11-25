# Exercise library

Objective: an exercise library per person, over a curated layer and a public fallback, that gives each exercise its primary and secondary muscles, so weekly volume can also be counted fractionally (primary 1, secondary 0.5) beside the existing per-group count.
Branch: `feat/exercise-library` from `main` (293e4abc). Delivery: one PR. Test runner: `go test ./internal/... ./cmd/...`. Refs #137.

## Specs

- S1 (owner, verbatim, 2026-11-22): "por ahora no creo que podamos contar credito fraccional porque no tenemos una biblioteca de datos no?... o si? investiga si podemos hacer biblioteca de ejercicios seria increible..."
- S2 (owner, verbatim, 2026-11-22): "pues... biblioteca propia pero cogiendo los datos de otros para hacerlo simple vale? o sea partimos de base de datos publica y modificamos los ejercicios poco a poco conforme vayamos haciendo vale? es decir: base de datos propia, si falla propia pasamos a una publica (fallar == no encontrar ejercicio)"
- S3 (owner, verbatim, 2026-11-25): "Quiero avanzar lo máximo posible en este proyecto. [...] esta tiene que ser la mejor app para planificar el entrenamiento de forma no opinada que existe."

Assumptions: lookup order is the person's alias, their own entries, a curated layer embedded in the binary (changed only by pull request), then free-exercise-db (Unlicense, 876 exercises) vendored at a pinned commit; "fail" is "not found in that layer". Names match exactly (case and spaces folded) or normalized (accents and punctuation folded too); a miss returns candidates and picks none. Public muscle names map to `training.MuscleGroups` through a fixed table; names without one target are `uncertain` and never counted. Correcting a curated or public entry copies it into the person's own layer. Exercises without an entry keep counting through `exercise_groups`. The fractional view is added beside the existing volume fields, which do not change. The library is per exercise; the set's `equipment` stays the machine identity.

## Tasks

- [x] T1 (S1, S2) inline: domain in `internal/training/exerciselib*.go` (layers, matching, copy on write, aliases, needs-mapping, fractional volume after Pelland et al. 2025); vendored dataset, upstream license, curated layer and `cmd/exerciselib-import` that writes the dataset with its commit and SHA-256. Risk: none (new code and embedded data). Commit: `d945ba88`.
- [x] T2 (S2) inline: migration 031 `exercise_library_entries` and `exercise_aliases`, per person; store methods; both tables captured for undo. Risk: item 1 (migration). Commit: `bb6e7d4f`.
- [x] T3 (S1, S2, S3) inline: MCP `lookup_exercise`, `list_exercises_needing_mapping` (reads), `save_exercise_entry`, `set_exercise_alias` (proposals); optional `fractional` on `weekly_volume` and `volume_by_muscle`; isolation, proposal and undo cases; Spanish labels in the coach and review pages. Risk: none (additive optional field and new tools). Commit: `a2b31f92`.
- [x] T4 (S3) inline: `docs/tools.md` (tools, outputs, sources and licenses), README, this file. Risk: none. Commit: the docs commit of the PR.

Route evidence: sequential units sharing one domain (T2 and T3 build on T1); delegated writer with an explicit file-conflict plan (migration 030 belongs to another writer). Verification trigger: T2 names item 1, so an independent verifier checks migration 031 before merge.

## Log

- L1 (2026-11-22, owner, verbatim): S1, then S2 after the research in #137.
- L2 (2026-11-22): #137 research: free-exercise-db is the only candidate dataset bundlable without attribution, share-alike or commercial limits; Pelland et al. 2025 is the source for counting a direct set 1 and an indirect set 0.5.
- L3 (2026-11-25, owner, verbatim): S3.
- L4 (2026-11-25): free-exercise-db pinned at `f00c92c7dcf1216a928a52c3706c7ce8e2f71ed5`, `dist/exercises.json` SHA-256 `5bb747e3fc658f095a60dcbf6d53c96627acdcc6ffb6fffde86f7e26995d40bf`, Unlicense. Of its 17 muscle names, 12 map to one group and 5 are uncertain (`shoulders`, `middle back`, `lower back`, `abductors`, `neck`); of 876 entries, 369 map cleanly and 507 carry at least one uncertain muscle. One curated entry: "Bench Press - Powerlifting", which the dataset lists with triceps primary.
- L5 (2026-11-25): scope notes: an alias may point at an entry in any layer, not only the own layer as #137 design item 1 words it, so a confirmed match to a public entry costs no copy; the coach still proposes the match itself (no automatic matching on a miss); the per-group counts, `simulate_plan` and `volume_recommendation` still use `exercise_groups` only.
