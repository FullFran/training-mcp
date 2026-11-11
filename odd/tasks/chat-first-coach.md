# Chat-first coach

Objective: the PWA's home becomes a ChatGPT-like chat with the coach, with the live workout always at hand, buttons that open panels and return to the chat, photos, and voice.
Branches: one per unit from `main`, Orca worktrees, coordinator merges. Delivery: `auto-chain`, `stacked-to-main`. Test runner: `go test ./...`.

## Specs

- S1: Chat-first home. User (2026-11-11): "montalo! que sea como chat gpt pero que te de botones para ir a paneles y despues volver al chat". Coordinator recommendation the user accepted with "montalo!": chat is the main screen, with a pinned live workout card (current exercise, one-tap log with pre-filled values, rest timer) that works without the model and offline; plan of the day, pending proposals with accept/reject, progress and the session summary appear as cards inside the chat; history and plans stay as panels reachable by buttons, with a way back to the chat.
- S2: Photos. User: "y que se le pueda mandar fotos".
- S3: Voice. User: "y si se pudiese hablar por audio seria la ostia".
- S4: Principles kept: deterministic work by tools and buttons, no tokens ("todo lo determinista que se haga determinista sin gastar tokens!"); multi-user isolation; each person's own model.

Decisions (proposed by the coordinator, confirmed by the user in L3): photos are sent with the chat turn and not stored by training-mcp; location metadata (EXIF) is stripped before upload; voice uses the browser's speech recognition for input and speech synthesis for reading replies, so it costs no tokens and needs no new provider.

## Tasks

- [x] C1 (S1, S3, S4) delegated, Orca worker: chat-first shell, pinned workout card, panel buttons and return, inline cards, voice input. Risk: none. PRs #62 (f81fc99), #65 (577d4b7), #66 (cd5520d).
- [x] C2 (S2, S4) delegated, Orca worker: photo attachments from the composer through the host to the Pi engine. Risk: item 2 (upload handling and limits). PR #63 (2558e2a).

Route evidence: two independent units with disjoint surfaces except the composer markup; C1 owns the templates, coach.js and app.css; C2 owns a separate attachment script and the host and engine paths, and mounts into a slot C1 provides.

## Log

- L1 (2026-11-11, user, verbatim): "vale me gusta, ahora no se si hacer que el chat sea lo central hummmm y lo demas vaya apareciendo que hacemos? que opinas?"
- L2 (2026-11-11, user, verbatim): "montalo! que sea como chat gpt pero que te de botones para ir a paneles y despues volver al chat, y que se le pueda mandar fotos y si se pudiese hablar por audio seria la ostia"
- L3 (2026-11-11, user, verbatim): "perfecto! me gusta tus decisiones".
- L4 (2026-11-11, user, verbatim): "el leer respuestas mejor quitalo pero lo de mandarle audios si es muy util!!!!". Read-aloud removed in #66; dictation kept with tap or silence to stop and an optional send-when-done switch.
- L5: Evidence. #62: chat home, pinned workout card, panels in a sheet, server-rendered cards, 7 RED-first tests, headless Chromium flows. #65: safe Markdown subset (innerHTML never used; script and javascript: links stay text) and per-tool proposal summaries on chat and /review. #66: voice polish, headless with a scripted recognizer. #63: up to 4 photos, 2 MiB each, 6 MiB total, EXIF stripped and 1600 px max client-side, never stored or logged, Pi prompt images; verifier no blockers; 656 tests. Not verified: real ChatGPT with photos; real speech on a phone.
- L6: Follow-ups: #64 photo hardening (pixel cap, upload concurrency, history re-send); #67 flaky TestCoachEndToEnd (predates #63).
