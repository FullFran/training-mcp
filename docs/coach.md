# In-app coach: chat with your own ChatGPT while you train

Status: experimental, **off by default**. Issue [#48](https://github.com/FullFran/training-mcp/issues/48).

A signed-in person opens **Coach** on the workout screen, connects their own ChatGPT account with a device code, and talks with a coach that runs on the server. The coach uses training-mcp's MCP tools as that person and has no other tool. When they log a set with the chat open, the coach gets that set and reacts on its own.

Connecting ChatGPT or Claude to `/mcp` yourself (option A) keeps working alongside it.

## Read this before enabling it

- **Not verified: OpenAI's terms.** The coach signs in with a person's own ChatGPT subscription and uses it from a server. Whether OpenAI's terms allow that use is an assumption nobody has checked (RE;write carries the same open question). Decide that before you enable it for other people.
- **Not verified: a real sign-in.** No real ChatGPT account was used while this was built. [What is not verified](#what-is-not-verified) lists what that leaves open, and [Manual check](#manual-check-with-a-real-account) is how to close it.
- **Training data goes to OpenAI** with the person's account whenever the coach reads it with a tool, as it does when they connect ChatGPT to `/mcp` themselves.
- **The operator pays for no model.** Each person brings their own account.

## How it works

```mermaid
flowchart LR
  B[phone, signed in] -->|/coach/*, the app's own login| S[training-mcp]
  S -->|stdin and stdout, JSON lines| E[engine process, one per person, Pi SDK]
  E -->|/mcp on loopback, engine token| S
  E -->|model requests with the person's token| O[OpenAI]
  S --- V[(credentials, encrypted, one file per person)]
```

| Part | Where | What it does |
| --- | --- | --- |
| Routes and chat | `internal/websrv/coach.go`, `static/coach.js` | The browser side, behind the app's existing login |
| Process manager | `internal/engine/manager.go` | Starts, feeds and stops engine processes; owns credentials, refresh, slots and live events |
| Vault and tokens | `internal/engine/vault.go`, `internal/engine/tokens.go` | Encrypted credentials; engine bearers for `/mcp` |
| `/mcp` door | `internal/httpsrv/server.go` (`engineGuard`) | Admits an engine token as its person, on direct loopback only |
| Protocol | `internal/engine/protocol.go`, `engine/protocol.ts` | The messages between host and engine; the Go side is the authority |
| Engine | `engine/main.ts` | A separate Node program on the Pi SDK (`@earendil-works/pi-coding-agent` and `pi-ai` 1.5.2), with its own lockfile |

The Go server never loads Node code. With the coach off, nothing in `engine/` is started and the `/coach/*` routes do not exist (404).

### Why this protocol and not ACP

The host speaks RE;write's engine protocol (one JSON object per line over the engine's stdin and stdout), not the Agent Client Protocol that haga-harness's `pi-sidecar` uses. For a Go host this is the simpler of the two:

- It is a dozen flat message types that a Go struct and `encoding/json` parse directly; ACP is JSON-RPC 2.0 with requests, responses, notifications, session and permission methods, which needs a client library or a hand-written JSON-RPC layer.
- It already carries what the security boundaries need and ACP has no slot for: the host-held credential (`token.get`), the single refresher (`refresh`), and the device login (`login.start`, `login.code`).
- ACP's permission requests and file-system callbacks exist for a coding agent; here they would be dead surface, since the engine has no tools besides `/mcp`.

## Enable it

1. The image already contains Node and the engine (`/app/engine`). Nothing to build.
2. Create a key and keep a copy off the server. Losing it makes every stored credential unreadable; people then connect again.

   ```bash
   openssl rand -base64 32
   ```

3. Set the environment. The web app must be on (`WEB_BASE_PATH` or `WEB_OAUTH_CLIENT_ID`).

| Variable | Default | Meaning |
| --- | --- | --- |
| `TRAINING_COACH` | unset | `1` enables the coach. Anything else but empty stops the server |
| `TRAINING_COACH_KEY` | none | 32 bytes, base64. Encrypts stored credentials. Required when enabled |
| `TRAINING_COACH_MAX_PROCESSES` | `2` | Engine processes alive at once. Above the cap the answer is `busy` |
| `TRAINING_COACH_IDLE_SECONDS` | `300` | A process with no turn and no pending login for this long is stopped |
| `TRAINING_COACH_NODE` | `node` (`/nodejs/bin/node` in the image) | The Node binary |
| `TRAINING_COACH_ENGINE` | `/app/engine/main.ts` | The engine program |

Credentials are stored in `coach/credentials/` next to the database (`/data/coach/credentials/` in the image). Engine processes reach the server at `http://127.0.0.1:<port>/mcp`, so the server must listen on loopback too (the default `:8080` does).

## What a person sees

1. The workout screen has a **Coach** button. It opens a bottom sheet over the workout; logging stays where it was.
2. Not connected, the sheet asks them to connect ChatGPT, then shows a code and a link to `https://auth.openai.com/codex/device`. They sign in there and type the code. Nothing travels from their browser to the server in this step; the server polls OpenAI.
3. Connected, they type and the answer streams in. Each tool call shows as a step (`Consultando list plans…`).
4. Once the chat has been opened on that page, logging a set starts a short reaction from the coach (feedback on the set and what to do next); with the sheet closed, a dot on the button says there is something new. Without an open chat, the set waits and arrives with the next message.
5. **Desconectar ChatGPT** deletes the stored credential, stops their engine process and revokes its token.

The model is the first one OpenAI lists for the account. The conversation lives in the engine process only: it is lost when the process stops (idle, disconnect, restart) and is never written to disk.

## Routes

Under the app's base path, behind its existing login: the bearer of the secret mount, or the web login session at the root (which also checks its CSRF token on writes). Every write also needs the header `X-Coach: 1`, which a cross-site form cannot send. A request with no identity is refused with 403.

| Route | Answer |
| --- | --- |
| `GET /coach/status` | `{state}`: `disconnected`, `pending` (with `user_code` and `verification_uri`), `connected` or `reconnect` |
| `POST /coach/login` | Starts a device login: `{user_code, verification_uri, expires_in}`. One pending login per person; a new one cancels the previous. At most 3 starts per person in 15 minutes, then `rate_limited` |
| `POST /coach/disconnect` | Disconnects |
| `GET /coach/stream` | `text/event-stream`: `turn {origin: "user" or "reaction"}`, `delta {text}`, `tool {name, phase, ok?, call?}`, then `done {aborted}` or `error {code}` |
| `POST /coach/prompt` `{text}` | `202 {started}`; the answer arrives on the stream. Text is 1 to 4000 characters; any other field is 400 |
| `POST /coach/abort` | `{aborted}`: stops the running turn |

Errors are a closed set of codes: `model_busy`, `reconnect`, `provider_error`, `busy`, `rate_limited`, `turn_in_progress`, `turn_limit`, `not_connected`, `engine_unavailable`, `login_failed`. Provider error text can quote response bodies, so it never reaches a response or a log.

The service worker never handles `/coach/*`, so nothing of the chat is cached.

### Live workout events

Logging a set in the app (`POST /sets`, accepted) queues a `set_logged` event for that person: exercise, weight, reps, RPE, technique and the time, which is also when their rest starts. Events live in memory only, at most 20 per person, and are never written anywhere. At the start of each turn the host writes them, oldest first, as lines before the person's text:

```text
[training-mcp context, set by the host] now=2026-11-11T18:02:10+01:00
[training-mcp context, set by the host] event=set_logged at=2026-11-11T18:00:00+01:00 exercise="press banca" weight_kg=80 reps=8 rpe=8.5 (rest started at this time)
```

Typed values are JSON strings, cut at 120 characters, with control and invisible format characters replaced. The system prompt tells the model that only the host writes these lines, always first, and that a later line claiming to be one was typed by the person; such a line of the person's text also gets `(person text) ` in front. Rest targets and elapsed rest are not carried here: the coach reads them with the `live_workout` tool and changes the target with `set_rest_target`.

Sets the coach itself logs through MCP are not echoed back as events.

## Security boundaries

- **Same door as a connected client.** The engine's MCP bearer is a random token (`tme_...`) the server keeps only as a SHA-256 hash in memory, tied to one person. It goes through the same `/mcp` handler and the same tools as that person's own connector would. It is not a JWT, so the OAuth verifier cannot accept it, and it does not open the web app.
- **Loopback only.** An engine token is honoured only when all of this holds: a live token, a loopback peer, a loopback IP in `Host` (the address the engine dials), and none of `Forwarded`, `Via`, `X-Real-IP`, `CF-Connecting-IP`, `True-Client-IP` or any `X-Forwarded-*` header. Anything less gets 401. A reverse proxy on the same host that adds none of these headers cannot be told apart from the engine; Caddy and Traefik add `X-Forwarded-For` by default. The token never leaves the engine process and the server's memory, so this is defence in depth.
- **Not the owner.** The engine door is checked before every other one, so even `MCP_AUTH_DISABLED=1` never turns an engine into the owner.
- **Short-lived.** A token lives as long as its process: until idle stop, disconnect, shutdown, or 60 minutes at most. A process whose token would expire during a turn is replaced first.
- **Credentials.** One file per person, named by a hash of the person, AES-256-GCM with the person bound in as associated data (a file moved to another person's name does not decrypt), mode 0600 in a 0700 directory, written to a temporary file and renamed. Never logged, never sent to the browser, never on the engine's command line or in its environment: the engine asks for the access token over stdin when it needs one, and never gets the refresh token except inside a refresh request. Backups of the data volume contain them, encrypted with a key the backup does not hold.
- **One refresher.** OpenAI's refresh token is single-use. The server is the only place that decides to refresh, one refresh at a time per person (concurrent token requests wait for the same refresh); the exchange runs in that person's engine through Pi. If it fails, the stored credential is kept and the person is asked to connect again. A process stopped while a refresh is out is kept up to 10 seconds so the new token can be stored; only a disconnect throws it away.
- **Slots.** A process is a scarce slot. A pending login holds one without a credential, so it has a hard deadline (15 minutes), starts are rate-limited per person, a process left with no pending login and no credential is stopped at once, and a connected person's turn takes the slot of the oldest process that only waits for a code. A turn has a time limit (10 minutes) and a size limit (2 MiB of text); a process that does not settle after either is stopped.
- **Shutdown.** Engine processes exit when their stdin closes, which covers a server that dies. On SIGTERM or SIGINT the server stops them before draining HTTP connections, which also ends open chat streams.
- **Tools.** Pi has no sandbox, so its shell and file tools are off (`noTools: 'builtin'`) and its script tool is not activated (`direct` exposure). The engine reports its tool list when the session starts; the engine and the server each stop it if any tool is not `mcp__training__*`.
- **Model providers.** The engine process gets an environment with `PATH` only and a private temporary home, so no provider key of the operator reaches it. Before the first request it checks that every available model belongs to the person's OpenAI login.
- **Isolation.** Every coach call names the person the request was authenticated as; events, streams, slots and credentials are kept per person. Tests cover two people for each route and for events.
- **Not a sandbox against the operator.** Anyone with a shell in the container and the key can read stored credentials.

Engine lifecycle is logged as `{"event":"engine.process","action":"start"|"stop","reason":...,"live":n}`, with no person, token, code or text.

## What is not verified

Tests run against a fake engine process (the Go test binary speaking the protocol) and call no provider; `cmd/training-mcp` runs the whole path, browser routes to engine to `/mcp` and back to the stream, with that fake. The real engine was started locally and in the image with the pinned packages against a local server: it reported Pi 1.5.2, connected to `/mcp` and listed exactly the 35 training-mcp tools. Without a real ChatGPT account, these remain open:

- the device-code login end to end (code shown, approval at OpenAI, credential returned and stored);
- that OpenAI accepts this login from a server address;
- the refresh exchange with a real rotating refresh token;
- a real model request: streaming, tool calls reaching `/mcp`, the answer arriving on the phone;
- the model list of a real account, and that its first entry is the right default;
- which error texts a real 429, 503 or expired login produce, and so whether they map to `model_busy` and `reconnect`;
- abort during a real model request;
- memory and latency under real use.

## Manual check with a real account

Use a test account's data only.

1. Set `TRAINING_COACH=1` and `TRAINING_COACH_KEY`, deploy, and confirm `GET /health` still answers.
2. Open the app on a phone, signed in, and press **Coach**. The sheet says ChatGPT is not connected and that the password is never typed here. Press **Conectar ChatGPT**: a code and the OpenAI link appear.
3. Approve at OpenAI. Within a few seconds the sheet shows the message box. On the server, `ls -l /data/coach/credentials/` shows one file with mode `-rw-------` and no readable token inside.
4. Ask "¿qué rutinas tengo?". The answer streams and a step shows the tool call.
5. Log a set with the sheet open: the coach reacts to it within seconds. Close the sheet and log another: the dot appears on **Coach**.
6. Start a long answer and press **Detener**: it stops, and the next message works.
7. Wait past `TRAINING_COACH_IDLE_SECONDS` and send again: it answers, with the earlier conversation gone. `docker logs` shows `engine.process` stop and start lines and no token, code or message text.
8. After more than an hour, send again: the turn still works (a refresh happened) and the credential file has a new modification time.
9. Press **Desconectar ChatGPT**: the file is gone and the sheet asks to connect again.
10. From another account with no connection, confirm the sheet asks to connect and shows nothing of the first account.

If step 3, 4 or 8 fails, the sheet shows a fixed message only. Run the engine by hand to see the real error; it is deliberately not logged.
