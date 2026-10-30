# Training MCP modernization

Objective: make the data safe and honest, then make logging resilient, then replace the secret URL.
Branch: `feat/training-modernization`. Delivery: `ask-on-risk`. Test runner: `go test ./...`; scripts are tested through `scripts/*_test.go` with fake tools.

## Specs

- S1: Off-site copy. User: "me vale R2 si". Cloudflare R2, encrypted before upload, with a restore test.
- S2: RPE provenance. A set with unknown RPE counts SI 1 and is marked as imputed, never presented as measured. User: "con un SI de 1 porque asi contabiliza como "una unidad" si esque el SI es para ponderar las series mas duras."
- S3: Record different sets in one call ("12, 12, 10 y 8") with an idempotency key, so a retry cannot duplicate them. Comparing values cannot detect duplicates, since identical straight sets are normal.
- S4: Authentication for issues #1 and #3: bearer if ChatGPT accepts it, otherwise OAuth with CIMD rather than DCR. Secret URLs go only after the replacement is proven.
- S5: Docs state 34 tools and 9 migrations; issue #1 no longer asks for DCR.

Assumption: the order S1, S2, S3, S4 is the one proposed to and accepted by the user. Parked: OpenTelemetry, MCP Apps, two-way Hevy sync, full exercise catalog.

## Tasks

- [~] T1 (S1, S5) inline: off-site upload, restore check, doc counts. Risk: item 2 (secrets), item 5 (scheduled job). Pending: real R2 upload needs the user's bucket and token; editing issue #1 moves to T4.
- [~] T2 (S2) inline: migration 011, `rpe_imputed`, log_sets accepts no RPE, suggest_load and the PWA do not present it as measured. Risk: item 1 (persisted format). Pending: marking the 160 candidate sets needs the user's confirmation.
- [~] T3 (S3) inline: heterogeneous logging with idempotency. Tool, migration 010 and tests done; whether ChatGPT reuses a key on retry is unobserved.
- [ ] T4 (S4) inline: authentication. Risk: item 2.

Route: inline; no parallel units with disjoint surfaces, so no writer trigger holds.

## Log

- L1 (2026-10-30, user, verbatim): "Perfecto pues vamos a ello! me vale R2 si"
- L2: Forecast is above 400 changed lines overall, so the chain strategy gets asked once before the slice that crosses it.
- L3: T1 uses gpg and curl, both already installed, instead of adding age or rclone. Symmetric passphrase rather than a key pair: the only thing to keep off this machine is one secret.
- L4: T1 evidence: `go test ./scripts` green; real gpg round trip of the 2026-10-30 backup through `restore-check.sh` in scratch gave 103 sessions and 2132 sets, the server booted on the copy and served list_sessions; a wrong passphrase exits 2. Upload to a real bucket not yet exercised.
- L5: RPE profile, verified against the 2026-08-08 backup: the 160 sets added since then are all RPE 8 (150 dated 2025-01/02, 10 on 2026-10-29), all inserted on 2026-10-29; the 1972 older sets are unchanged and have 7 distinct RPE values. So 7.5% of sets are suspect, not 54%. T2 waits on the user's decision on SI for unknown RPE.
- L6: T3 is built on `insertSet`, extracted from `AddSet`, so position and SI have one implementation. Unit preservation (lb) stays out of S3.
- L7: Independent review of bedbf23 found restore-check failing silently on an empty bucket or wrong passphrase, and part of an unquoted passphrase reaching the log because r2.env was sourced. Fixed: r2.env is now parsed as data, messages added, a bad configuration no longer costs the local copy, and a manual run clears the off-site marker. Real upload to R2 remains unexercised.
- L8 (2026-10-30, user, verbatim): "con un SI de 1 porque asi contabiliza como "una unidad" si esque el SI es para ponderar las series mas duras."
- L9: T2 evidence: suite and race detector green; migration on a copy of real data keeps 2132 sets, SI 2337.8, 1142 RPE-8 sets, with 0 marked. Migration marks nothing: existing candidate sets (160 inserted since 2026-10-29) are a separate explicit data change. log_set and add_set keep their required RPE; the PWA still preselects 8.
