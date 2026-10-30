# Training MCP modernization

Objective: make the data safe and honest, then make logging resilient, then replace the secret URL.
Branch: `feat/training-modernization`. Delivery: `ask-on-risk`. Test runner: `go test ./...`; scripts are tested through `scripts/*_test.go` with fake tools.

## Specs

- S1: Off-site copy. User: "me vale R2 si". Cloudflare R2, encrypted before upload, with a restore test.
- S2: RPE provenance. The new field must distinguish measured from imputed RPE, and unknown must be able to stay unknown. Open decision: which SI a set with unknown RPE carries.
- S3: Record different sets in one call ("12, 12, 10 y 8") with an idempotency key, so a retry cannot duplicate them. Comparing values cannot detect duplicates, since identical straight sets are normal.
- S4: Authentication for issues #1 and #3: bearer if ChatGPT accepts it, otherwise OAuth with CIMD rather than DCR. Secret URLs go only after the replacement is proven.
- S5: Docs state 34 tools and 9 migrations; issue #1 no longer asks for DCR.

Assumption: the order S1, S2, S3, S4 is the one proposed to and accepted by the user. Parked: OpenTelemetry, MCP Apps, two-way Hevy sync, full exercise catalog.

## Tasks

- [~] T1 (S1, S5) inline: off-site upload, restore check, doc counts. Risk: item 2 (secrets), item 5 (scheduled job). Pending: real R2 upload needs the user's bucket and token; editing issue #1 moves to T4.
- [ ] T2 (S2) inline: migration and SI decision. Risk: item 1 (persisted format).
- [ ] T3 (S3) inline: heterogeneous logging with idempotency.
- [ ] T4 (S4) inline: authentication. Risk: item 2.

Route: inline; no parallel units with disjoint surfaces, so no writer trigger holds.

## Log

- L1 (2026-10-30, user, verbatim): "Perfecto pues vamos a ello! me vale R2 si"
- L2: Forecast is above 400 changed lines overall, so the chain strategy gets asked once before the slice that crosses it.
- L3: T1 uses gpg and curl, both already installed, instead of adding age or rclone. Symmetric passphrase rather than a key pair: the only thing to keep off this machine is one secret.
- L4: T1 evidence: `go test ./scripts` green; real gpg round trip of the 2026-10-30 backup through `restore-check.sh` in scratch gave 103 sessions and 2132 sets, the server booted on the copy and served list_sessions; a wrong passphrase exits 2. Upload to a real bucket not yet exercised.
