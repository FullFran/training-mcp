# HCS-0008 Host adapter: projecting a generation into an engine

| Field | Value |
| --- | --- |
| Status | Draft |
| Provisional | yes (HCS-0000 section 4) |
| Superseded when | a neutral body (for example an AAIF project) publishes an engine-neutral format for installing skills, MCP servers and instructions into an agent runtime, or MCP and Agent Skills define client-side installation; this document then profiles that format or is withdrawn |
| Version | 0.1.0-draft |
| Requires | HCS-0001, HCS-0003, HCS-0004, HCS-0005, HCS-0006 |
| Schema | `schemas/hcs-0008/acp/v0.1/acp.schema.json` (`urn:hcs:schema:hcs-0008:acp:0.1`), section 6.8; `schemas/hcs-0008/client-limits/v0.1/client-limits.schema.json` (`urn:hcs:schema:hcs-0008:client-limits:0.1`), section 6.9 |
| Replaces | none |

## 1. Scope

Resolution (HCS-0003) and distribution (HCS-0007) end with a set of prepared entries that the host has selected. Something still has to turn that set into the configuration an agent engine reads at start: where its skills are, which MCP servers it connects to, which instructions it loads, which permissions it enforces. This document calls that step **projection** and the host component that performs it a **host adapter**. It defines what an adapter receives, what it must do with each contribution, the precedence between host-owned settings and generation content, how it reports what it could not project, which generation an engine run sees, and when a new generation counts as confirmed.

The first host does this step ad hoc, for skills only, in two places, and confirms a generation on one of them without checking that the engine loaded anything; MCP, policy and instruction packages arrive and are reported active while no engine reads them ([host-adapter-standards.md](https://github.com/FullFran/hcs/blob/v0.1.0-draft.10/docs/research/host-adapter-standards.md) section 1). Every requirement below traces to one of those gaps or to an adopted standard.

No existing standard covers projection into an agent engine ([host-adapter-standards.md](https://github.com/FullFran/hcs/blob/v0.1.0-draft.10/docs/research/host-adapter-standards.md) section 3), so this document is **Provisional** ([ADR-0016](https://github.com/FullFran/hcs/blob/v0.1.0-draft.10/docs/adr/0016-host-adapter-contract-is-provisional.md)). It reuses, and does not restate:

| Need | Standard | How |
| --- | --- | --- |
| Skill format | Agent Skills ([agent-skills.md](https://github.com/FullFran/hcs/blob/v0.1.0-draft.10/docs/standards/agent-skills.md)) | adopted as is; the adapter moves directories, never edits them |
| MCP server descriptor | MCP Registry `server.json`, schema 2025-12-11 ([mcp-registry-server-json.md](https://github.com/FullFran/hcs/blob/v0.1.0-draft.10/docs/standards/mcp-registry-server-json.md)) | profiled: no secret values inside a generation ([ADR-0017](https://github.com/FullFran/hcs/blob/v0.1.0-draft.10/docs/adr/0017-mcp-descriptors-profile-server-json.md)) |
| Instructions | AGENTS.md ([agents-md.md](https://github.com/FullFran/hcs/blob/v0.1.0-draft.10/docs/standards/agents-md.md)) | content convention for instruction contributions, which stay extension kinds ([ADR-0018](https://github.com/FullFran/hcs/blob/v0.1.0-draft.10/docs/adr/0018-instructions-and-policies-stay-extension-kinds.md)) |
| Layered precedence | UAPI.6 vendor and administrator layers ([uapi-config-files.md](https://github.com/FullFran/hcs/blob/v0.1.0-draft.10/docs/standards/uapi-config-files.md)) | model for host-owned settings ([ADR-0019](https://github.com/FullFran/hcs/blob/v0.1.0-draft.10/docs/adr/0019-host-owned-settings-take-precedence.md)) |
| Confirm after health, bounded retry | UAPI.1 boot counting, systemd Automatic Boot Assessment ([uapi-boot-counting.md](https://github.com/FullFran/hcs/blob/v0.1.0-draft.10/docs/standards/uapi-boot-counting.md)) | state model with one attempt ([ADR-0020](https://github.com/FullFran/hcs/blob/v0.1.0-draft.10/docs/adr/0020-confirm-after-health-one-attempt.md)) |
| Health check semantics | Kubernetes startup probes, gRPC health ([kubernetes-probes.md](https://github.com/FullFran/hcs/blob/v0.1.0-draft.10/docs/standards/kubernetes-probes.md), [grpc-health.md](https://github.com/FullFran/hcs/blob/v0.1.0-draft.10/docs/standards/grpc-health.md)) | outcome only; HCS names no protocol |
| Errors | HCS-0006 | existing codes only ([ADR-0021](https://github.com/FullFran/hcs/blob/v0.1.0-draft.10/docs/adr/0021-projection-reuses-existing-error-codes.md)) |

Out of scope: how a host starts, stops or supervises an engine process; engine-specific file layouts and flags (non-normative [appendix A](#appendix-a-engine-mappings-non-normative)); how a host stores secrets; new contribution kinds (issue #3).

## 2. Terms

- **Generation:** the prepared, unpacked content of the entries a host selected for one desired set (HCS-0003 step 6, HCS-0004 `prepared`), identified by a generation id and never modified after it is prepared (HCS-0007-R017).
- **Engine:** the agent runtime a host starts and configures (for example a coding agent CLI or an agent server).
- **Engine run:** one engine process, or one engine session the host creates, from start to stop.
- **Host adapter:** the host component that turns a generation into an engine's configuration for one engine.
- **Projection:** the configuration an adapter produces for one engine run: files, command-line arguments or environment values the engine reads at start, or the parameters of the request that creates the engine session (for example ACP `session/new`, section 6.8). Its shape is engine-specific.
- **Engine-level name:** a name the engine uses to tell configured items apart: a skill `name`, an MCP server name, a permission rule's target.
- **Host-owned setting:** an engine setting the host declares non-negotiable for a generation, for example the permission policy, a content-safety proxy, an MCP server the host itself provides, or a name the host reserves. The host declares the list; HCS defines no vocabulary for it (HCS-0005 section 1).
- **Host-provided default:** an engine setting the host supplies but does not own, for example a skill bundled with the host application. A generation contribution with the same engine-level name replaces it.
- **Confirmed generation:** the last generation that passed section 7; the generation a host returns to.

## 3. Inputs and outputs

An adapter receives:

1. the generation: its id and, in activation order, each selected entry with its id, version, the root of its unpacked content and its contributions (HCS-0001 section 3.2);
2. the host-owned settings and host-provided defaults for the target engine;
3. the secrets the host grants to each entry (HCS-0005), by name;
4. the contribution kinds the adapter can project for the target engine.

It produces:

1. the projection for one engine run;
2. for each entry, either "projected" or the HCS-0006 errors that prevented it.

The host decides from the second output which entries it may report `active` on that engine (R005). An entry the engine cannot take does not stop the transition (R017); the other projection errors count against the host's activation policy (HCS-0003 step 6).

## 4. One generation per engine run

**[HCS-0008-R001]** A host MUST derive each engine run's projection from exactly one generation and MUST make the projection reference that generation by an identifier that does not change during the run, never by a pointer the host moves to the next generation.

**[HCS-0008-R002]** A host MUST NOT change what a running engine reads through its projection; it MUST move an engine to another generation only by starting a new engine run from a new projection.

R001 and R002 are the atomicity rule seen from the engine: an engine run sees one generation, whole. An `active` pointer switched atomically ([kubernetes-probes.md](https://github.com/FullFran/hcs/blob/v0.1.0-draft.10/docs/standards/kubernetes-probes.md), `AtomicWriter`) is a correct way to choose the generation of the next run; a projection that names the pointer itself is not, because an engine that rescans during the run can see the next generation, or a mix.

## 5. Every contribution is projected or reported

**[HCS-0008-R003]** An adapter MUST, for every contribution of every entry in the generation, either project it into the engine run or report an HCS-0006 error with the entry's id; it MUST NOT leave a contribution out silently.

**[HCS-0008-R004]** An adapter MUST report `unsupported_contribution` for an entry that has a contribution whose kind it cannot project into its engine.

**[HCS-0008-R005]** A host MUST NOT report an entry as `active` unless every contribution of that entry was projected into the engine run.

**[HCS-0008-R017]** A host MUST NOT fail or roll back a move to a new generation only because an adapter reported `unsupported_contribution`; it MUST treat the reported entry as not selected for activation on that engine and MUST keep it reported until a generation or an engine change lets the adapter project it.

R005 is HCS-0001-R008 applied at projection time: a host that implements a kind for resolution but whose engine ignores it does not implement that kind on that engine. R017 keeps that per-engine fact from deciding the fate of the whole generation: the `all-or-nothing` policy (HCS-0003 step 6, required by DISTRO-1, HCS-0007-R016) governs whether the signed catalog is accepted and resolved, and HCS-0004-R003 applies to the entries selected for activation on the engine; an entry the engine cannot take was never selected there. Other projection errors (`ambiguous_provider`, `permission_denied`, `manifest_invalid`) are failures and count against the activation policy ([ADR-0022](https://github.com/FullFran/hcs/blob/v0.1.0-draft.10/docs/adr/0022-host-adapter-open-questions-decided.md), decision 2).

## 6. Projection rules

### 6.1 Name collisions

**[HCS-0008-R006]** When two contributions of one generation would project the same engine-level name, an adapter MUST project neither and MUST report `ambiguous_provider` for each entry involved.

Engines resolve such a collision in undocumented or order-dependent ways (recursive directory scans, last writer wins); the adapter is the last point where it is still visible. A collision with a host-provided default is not a collision: the generation contribution replaces the default.

### 6.2 Host-owned settings take precedence

**[HCS-0008-R007]** An adapter MUST NOT produce a projection in which a generation contribution replaces, removes or widens a host-owned setting, and MUST report `permission_denied` for that entry.

To **widen** a setting is to allow an operation the host-owned setting denies or asks about, to disable a host-owned component (a safety proxy, an MCP server, a hook), or to change where a host-owned component points. A contribution MAY narrow a host-owned setting: deny or ask about an operation the host allows. A generation never receives authority by configuring an engine (HCS-0005-R002).

### 6.3 Skills

**[HCS-0008-R008]** An adapter MUST NOT modify the files of a `skill` contribution's directory when it projects the skill.

**[HCS-0008-R009]** An adapter SHOULD project a skill under its Agent Skills `name`, without a prefix or namespace added by the engine.

The skill directory is Agent Skills as is: the adapter places it, or a link to it, where the engine discovers skills. R009 is a SHOULD because some engines only load a packaged skill under a plugin namespace (appendix A).

### 6.4 MCP servers

**[HCS-0008-R010]** An adapter MUST read an `mcp-server` contribution's `path` as an MCP Registry `server.json` document of schema revision 2025-12-11, and MUST report `manifest_invalid` for an entry whose document does not validate against that schema.

**[HCS-0008-R011]** A `server.json` document in a generation MUST NOT give a `value` or a `default` to an input whose `isSecret` is true; an adapter MUST report `manifest_invalid` for an entry that has one.

**[HCS-0008-R012]** An adapter MUST take the value of a secret input only from the secrets the host grants to that entry, MUST NOT write it inside the generation, and MUST report `permission_denied` for an entry with a required secret input that has no granted value.

A host that presents the tools of an `mcp-server` contribution to an engine, a client or a person does so under [HCS-0009](HCS-0009-tool-surface.md): it presents each tool's annotations from the effect class the package declares, and fails closed when none is declared. An invalid HCS-0009 declaration is `manifest_invalid`, reported with the R010 check.

Inputs are environment variables, headers, arguments and URL variables (`KeyValueInput` and `Input` in the schema). The engine-level name of an MCP server is the adapter's choice (appendix A); it is subject to R006 and R007 like any other name. Where the engine can resolve a reference at start (for example an environment placeholder), the adapter SHOULD write the reference rather than the value, so that the projection holds no secret either. Such a reference is compatible with R030 only if the engine resolves it once and does not interpret the value it inserts.

Engines may interpret characters in the values they pass to an MCP server: one observed engine runs a value that starts with `!` as a shell command and expands `$VAR`. A value that is data in a signed `server.json` would then become code on the user's machine. MCP Registry `server.json` defines its own substitutions: `{name}` in an input's `value` filled from that input's `variables` (an unknown `{name}` stays as written), and `{name}` in a transport `url` filled from the remote's `variables` or, for a package, from its argument and environment variable names. Nothing else in a value has a meaning ([ADR-0028](https://github.com/FullFran/hcs/blob/v0.1.0-draft.10/docs/adr/0028-literal-values-and-model-reachability.md)).

**[HCS-0008-R030]** An adapter MUST apply the `server.json` substitutions once, never to text a substitution inserted, and MUST deliver each resulting value or URL of an MCP server input (environment variables, headers, arguments, URL variables) so that the server receives exactly that string, escaping it for the engine or passing it through a channel the engine does not interpret. It MUST report `unsupported_contribution` for an entry with an input it cannot deliver that way to that engine; it decides this without reading secret values, treating a secret as an arbitrary string.

#### 6.4.1 Server code inside the generation (`registryType: "file"`)

A package can ship the code of a local MCP server, not only its descriptor: a script the engine starts over stdio. MCP Registry `server.json` describes packages that a client downloads from a registry (`npm`, `pypi`, `oci`, `nuget`, `mcpb`); its `registryType` is an open string, and no value names code that is already on disk. HCS profiles the value `file` for that case ([ADR-0026](https://github.com/FullFran/hcs/blob/v0.1.0-draft.10/docs/adr/0026-mcp-server-code-in-the-generation.md)). The code is covered by the entry's `contentHash` in the signed catalog (HCS-0001-R007, HCS-0007), so `fileSha256` is not used.

**[HCS-0008-R021]** A `server.json` package whose `registryType` is `file` MUST have a `stdio` transport and an `identifier` that is a relative path as HCS-0001-R005 defines for contribution paths, with no empty segments, that names, after removing `.` segments, a regular file inside the directory of the `mcp-server` contribution; a document MUST NOT have more than one such package. An adapter MUST report `manifest_invalid` for an entry that breaks this, including when the file, after resolving links, lies outside that directory.

**[HCS-0008-R022]** An adapter MUST start a `file` server from the file inside the generation, by a path that does not change during the run (R001), with the contribution's directory as working directory, and MUST NOT copy, move or modify the server's files to do so.

**[HCS-0008-R023]** An adapter MUST start a `file` server with a runtime the host provides for its `runtimeHint`, and MUST report `unsupported_contribution` for an entry whose `runtimeHint` names no runtime the host provides, whose package has a non-empty `runtimeArguments`, or whose package sets an environment variable that the host declares as a control setting of that runtime or of the loader (for example `NODE_OPTIONS`, `PYTHONPATH`, `LD_PRELOAD`).

**[HCS-0008-R024]** An adapter MUST pass the secrets granted to a `file` server only through its environment, never as command-line arguments, and MUST report `unsupported_contribution` for an entry whose package declares a secret argument, including an argument with a secret input among its `variables`.

The host owns the runtime and its flags (R023): runtime arguments, or control variables such as `NODE_OPTIONS`, in a signed descriptor would let a publisher change how the host's own runtime behaves. Arguments are readable by any local user (for example `/proc/<pid>/cmdline`); the environment is readable only by the same user and administrators, so it is less exposed (R024). A runtime that is present but fails to start the server is a health failure (R014, R015), not a projection error.

When several rules apply to one `mcp-server` contribution, an adapter reports only the first, in this order: `manifest_invalid` (R010, R011, R021), then `unsupported_contribution` (R024, R023, R030, R037, R038), then `permission_denied` (R012). An adapter does not ask for authority for a server it will not start.

Before this section, `registryType: "file"` had no meaning in HCS: R010 accepted it as an open string, and nothing said how to start it. This section gives it one; it does not change the meaning of any value HCS already defined (HCS-0000 section 5). Whether to run the code is decided by the signed catalog (HCS-0007) and the entry's content hash; what the running server may do is decided by HCS-0005 grants. HCS adds no separate allowlist of executable entries. Values given to the server also follow R030 (literal delivery).


### 6.5 Instructions, policies and other extension kinds

HCS-0001 defines two core kinds, `skill` and `mcp-server`. Instruction files, agent definitions, commands and policies are extension kinds of the distribution (`x-<distribution>-...`) until a second harness needs them in core (README rule 1, [ADR-0018](https://github.com/FullFran/hcs/blob/v0.1.0-draft.10/docs/adr/0018-instructions-and-policies-stay-extension-kinds.md)). R003 to R007 apply to them unchanged: the adapter projects them or reports them, and they never override a host-owned setting. A distribution that ships instruction contributions SHOULD use plain Markdown in the AGENTS.md convention and define no structure inside the file. A policy contribution is a narrowing (section 6.2): it can only make a host-owned permission stricter.

### 6.6 Guards on the model transport

A host-owned setting can be a guard on what reaches the model, for example a content-safety proxy that masks personal data. Placing it in an engine's message hooks does not cover every request: engines commonly let a hook that throws continue with the unmodified data, and they send model requests of their own (context compaction, branch summaries, cache warming) that never pass through those hooks. All of them pass through the engine's model transport ([ADR-0025](https://github.com/FullFran/hcs/blob/v0.1.0-draft.10/docs/adr/0025-host-guards-fail-closed.md)).

**[HCS-0008-R019]** A host that declares a guard on model requests as a host-owned setting MUST run the engine so that no request from a process of the run (the engine, its extensions and tools, and the MCP servers the run starts) can leave toward a model provider except through that guard, which runs outside those processes, and MUST NOT count a hook inside the engine, or a wrapper around the engine's own provider object, as that guard.

**[HCS-0008-R020]** When such a guard is unavailable or fails on a request, a host MUST ensure that the request does not reach the model provider.

R019 is a property of reachability, not of a code path: a component inside the run that resolves a model on its own (for example a code-execution tool that looks up a provider) bypasses a guard that only wraps the session's provider. A request that reaches a provider and is then refused has already delivered its body, which for a guard that masks data is the leak itself. So the property is met only when the request cannot leave:

- **Network confinement** meets R019: the run's outbound traffic is denied by default, and the guard, or an egress proxy in front of it, is the only allowed destination for model providers (for example a Kubernetes `NetworkPolicy` with default-deny egress, or the OpenSandbox egress policy).
- **Credential confinement** does not meet it alone. If only the guard holds provider keys, a bypassing request fails at the provider, but its body has been sent. It is a second layer that stops authenticated use. The first host does this with an environment allow-list and in-memory credentials held outside the engine's processes; its tests show that keys planted in the environment and the engine's credential file are never used.

Granting a provider credential to an MCP server of the run (R012) gives that server a direct route to a model; with a declared guard, its traffic falls under R019 like any other process of the run. A host-provided provider or base URL that the projection sets as a host-owned setting (R007 keeps a contribution from moving it) points the engine at the guard; it does not confine anything by itself. Engine hooks may add a second layer; they never replace it.

### 6.7 No ambient configuration

An engine that also reads the user's own configuration (a global settings directory, an extensions folder, a credentials file, a project's dot-files) runs with things the generation does not contain. Two runs of the same generation then differ by machine, and a host-owned setting can be overridden from outside the projection.

**[HCS-0008-R025]** A host MUST start each engine run so that the engine reads its configuration, skills, extensions, prompts, MCP servers and credentials only from the projection and from host settings, never from user-global locations, and never from workspace locations except instruction files the host allows as a host-owned setting.

Known ways to meet R025 are a private home directory and configuration directories for the run, an environment built from an allow-list, and engine switches that turn off discovery. The test is behavioural: with an empty home directory, the run loads everything it projected, and settings, extensions, skills, MCP servers and credentials planted in the user's or the workspace's configuration locations have no effect. Whether the engine reads a workspace's instruction files (for example `AGENTS.md`) is the host's choice, made visible as a host-owned setting; a coding host usually wants them, a host for office work usually does not.

### 6.8 Engines driven over ACP

Some engines are not configured through files but driven over the Agent Client Protocol: the host is the ACP client and creates each engine run with `session/new`. ACP v1 carries `cwd`, `mcpServers` and `_meta`, but it has no way to pass skills, no identifier for what configuration a session was created from, and no notification of what the agent loaded ([docs/standards/acp.md](https://github.com/FullFran/hcs/blob/v0.1.0-draft.10/docs/standards/acp.md)). This section defines an ACP extension under the HCS namespace `io.github.fullfran.hcs` (HCS-0000 section 7) for those three gaps. HCS proposes them to the ACP project; until ACP covers them, the extension is Provisional like this document ([ADR-0027](https://github.com/FullFran/hcs/blob/v0.1.0-draft.10/docs/adr/0027-projection-over-acp.md)). The shapes are in `schemas/hcs-0008/acp/v0.1/acp.schema.json`.

**[HCS-0008-R026]** A host MUST project a generation over ACP only to an agent whose `initialize` response advertises, in `agentCapabilities._meta["io.github.fullfran.hcs"]`, an object whose `projection` member is `"0.1"`. With any other agent it starts no engine run from the generation and reports `unsupported_contribution` for each entry with contributions for that engine (R004, R017).

**[HCS-0008-R027]** A host that creates an engine run over ACP MUST send, in the `session/new` request, the generation's MCP servers in `mcpServers` and an object `_meta["io.github.fullfran.hcs"].generation` with the generation's `id` and, in `skills`, the absolute paths of every skill directory the run gets, valid against the `sessionMeta` schema: the generation's skills, and the host-provided defaults and host-owned skills no generation contribution replaces (R006). A path of a generation skill MUST resolve inside the generation by its identifier, never through a pointer the host moves (R001), and no path may contain `..` segments. An agent that advertises R026 MUST load skills only from those paths.

**[HCS-0008-R028]** A host MUST send `_meta["io.github.fullfran.hcs"].generation` in every ACP `session/load` and `session/resume`, as in R027. An agent that advertises R026 MUST refuse a `session/load` or `session/resume` that lacks it or whose `generation.id` differs from the generation the session was created with, with a JSON-RPC error that carries an HCS error with code `generation_mismatch` (HCS-0006-R003). A host MUST NOT reopen a session on another generation; it starts a new session instead.

**[HCS-0008-R029]** An agent that advertises R026 MUST answer the request `_io.github.fullfran.hcs/loaded` (params `{ "sessionId": <id> }`) with what the session has loaded at that moment, valid against the `loadedResult` schema: skills by their Agent Skills `name`, MCP servers by the `name` the host gave them in `mcpServers`. A host that drives the engine over ACP MUST use that answer as the engine's report under R014; it may ask again until the health deadline passes (R013).

R028 follows from R001 and R002: a session's history, and any session-limited grants (HCS-0005-R006, R008), were created against one generation's code. Reopening it on another would apply them to code the person never saw. What an agent counts as a loaded MCP server is its own observation, for example that the connection completed or that the server's tools are listed. A server with no tools may be indistinguishable from one that did not start; HCS does not yet define a finer signal.


### 6.9 Client limits for MCP projection

An MCP client that shows a model a server's `instructions` and `tools/list` result may keep only part of them. The observed limit is not the same across clients and is not part of the MCP specification ([docs/standards/mcp.md](https://github.com/FullFran/hcs/blob/v0.1.0-draft.10/docs/standards/mcp.md)). One documented case: OpenAI's Apps SDK guide for MCP servers says to keep the most important details of the server instructions in their first 512 characters ([ADR-0037](https://github.com/FullFran/hcs/blob/v0.1.0-draft.10/docs/adr/0037-client-limits-are-host-declared.md) lists the sources). It states no limit on the size of the tool list; two harnesses keep that list under 36 KB for ChatGPT, a figure no public source confirms. HCS therefore names no number: the host declares the limits of each client it projects to, and the adapter checks against what was declared.

**[HCS-0008-R036]** A host that applies a limit of an MCP client to a projection MUST declare it as a host-owned setting, valid against the `clientLimits` schema (`schemas/hcs-0008/client-limits/v0.1/client-limits.schema.json`, `urn:hcs:schema:hcs-0008:client-limits:0.1`): the client's name, and at least one of `instructionsPrefix` (a positive number of characters) and `toolListBytes` (a positive number of bytes). A limit that is not declared is not applied, and HCS defines no default.

**[HCS-0008-R037]** When the client's declared limits include `toolListBytes`, an adapter MUST compare it, at projection time, with the size of the `tools` array that each projected `mcp-server` contribution answers to `tools/list` (all pages, serialized as compact JSON in UTF-8), and MUST report `unsupported_contribution` for the entry of a server whose size is greater than the limit.

**[HCS-0008-R038]** When the client's declared limits include `toolListBytes` and the host supplies no measured size for a projected `mcp-server` contribution, an adapter MUST report `unsupported_contribution` for its entry, and MUST NOT assume the server fits.

**[HCS-0008-R039]** An MCP server's `instructions` SHOULD give, within their first `instructionsPrefix` characters, what a model needs to use the server, for the smallest `instructionsPrefix` the host declares for a client it projects to.

The error is `unsupported_contribution` (ADR-0021): the client cannot take that contribution, and R017 keeps it from failing the generation. The host measures the size by listing the tools of the server it will start, for example in a probe run before the projection; the figure is the host's observation, not a claim of the publisher. A host that serves several servers behind one endpoint compares the list the client receives from that endpoint. A server whose tool list grows after the check is a case for the health check (R013), not for projection. R039 is a SHOULD because "enough to use the server" is not a property a program can test; an adapter cannot read instructions a server only sends once it runs. Instructions beyond the prefix remain allowed. The adapter never edits, truncates or reorders a server's instructions to fit.

## 7. Confirm after health

**[HCS-0008-R013]** A host MUST NOT confirm a generation until an engine run started from its projection has passed the host's health check within the host's deadline.

**[HCS-0008-R014]** A host whose engine reports which skills or MCP servers it loaded MUST fail the health check when a projected skill or MCP server is missing from that report.

**[HCS-0008-R015]** When the health check of a new generation fails or its deadline passes, a host MUST report the generation as failed with `plugin_failed` or `timeout`, MUST start an engine run from the confirmed generation, and MUST NOT start the failed generation again automatically.

**[HCS-0008-R016]** When the engine run started from the confirmed generation after a failure also fails its health check, a host MUST stop and report it, and MUST NOT start a further rollback or retry until a new desired set or an operator action arrives. The stop outlives a restart (R033 item 5, R035). R035 applies when the host cannot return to the confirmed generation at all.

**[HCS-0008-R018]** A host that reports the health of an engine run SHOULD report one of the states `healthy`, `unhealthy` or `unknown`, and SHOULD report the failure itself with the codes of R015.

R018 follows how the most widely used health mechanisms report health: a state plus, at most, a free-text or code reason, never a dedicated error code. Kubernetes pod conditions have `status` `True`, `False` or `Unknown` with a machine-readable `reason`; gRPC health returns `SERVING`, `NOT_SERVING` or `UNKNOWN`; boot assessment marks an entry good or bad ([kubernetes-probes.md](https://github.com/FullFran/hcs/blob/v0.1.0-draft.10/docs/standards/kubernetes-probes.md), [grpc-health.md](https://github.com/FullFran/hcs/blob/v0.1.0-draft.10/docs/standards/grpc-health.md), [uapi-boot-counting.md](https://github.com/FullFran/hcs/blob/v0.1.0-draft.10/docs/standards/uapi-boot-counting.md)). No HCS-0006 code is added for a failed health check ([ADR-0022](https://github.com/FullFran/hcs/blob/v0.1.0-draft.10/docs/adr/0022-host-adapter-open-questions-decided.md), decision 3).

This is UAPI.1 boot counting with one try ([uapi-boot-counting.md](https://github.com/FullFran/hcs/blob/v0.1.0-draft.10/docs/standards/uapi-boot-counting.md)): a new generation is indeterminate until the running engine, not the installer, proves it; a failed one is not retried; there is exactly one rollback and never a loop. It refines HCS-0007-R018 for engines. The health check itself is host-defined (an HTTP status, a gRPC `Check`, a command); R014 adds the one check every host can make when its engine exposes it. A host with no confirmed generation (first start) that fails R013 stops and reports under R016.

### 7.1 Crash-safe switch and persisted failure marks

These terms name facts a host persists; HCS defines no file format. All identifiers are generation ids (section 2).

- **active:** the generation the next engine run is started from.
- **confirmed:** the last generation that passed R013 (the generation R015 returns to).
- **pending:** the generation being tried, or none.
- **failed:** the set of generations marked failed.
- **stopped:** a recorded stop under R016 or R035, with the generation it concerns, or none.

A host persists a fact durably when a reader after a crash sees either the old record or the new one, never a partial one (for example: write a temporary file, flush it, atomically replace the record; the `AtomicWriter` pattern of [kubernetes-probes.md](https://github.com/FullFran/hcs/blob/v0.1.0-draft.10/docs/standards/kubernetes-probes.md)).

**[HCS-0008-R031]** Before a host changes which generation is active, it MUST durably record the generation it is moving to as pending and the confirmed generation it would return to.

**[HCS-0008-R032]** A host that reports a generation as failed under R015 MUST durably record it as failed before it starts the engine run from the confirmed generation, MUST NOT start a generation recorded as failed, including after a restart, and MUST NOT reuse the identifier of a generation recorded as failed for other content.

**[HCS-0008-R033]** After a restart, a host MUST decide what to start from its persisted facts alone, as follows, and MUST NOT treat an unconfirmed generation as confirmed:

1. when a generation is pending and it is the active generation, the host MUST run the check of R013 for it, applying R014 to R016 to the outcome;
2. when a generation is pending and it is not the active generation, the host MUST discard the pending record and keep the active generation;
3. when the active generation is recorded neither as confirmed, nor as pending, nor as failed, the host MUST treat it as pending as in item 1, with the confirmed generation as the one to return to, if there is one;
4. when the active generation is recorded as failed, the host MUST complete the return to the confirmed generation under R015 and MUST NOT start the failed generation;
5. when a stop is recorded, the host MUST stay stopped, MUST NOT start any generation or run a health check, and MUST report the stop as R016 requires, until a new desired set or an operator action arrives; a new desired set or an operator action ends the recorded stop.

**[HCS-0008-R034]** A host that removes old generations MUST keep the active, the pending and the confirmed generation and MUST keep every failed record; it MAY remove the content of a failed generation.

**[HCS-0008-R035]** When a host cannot complete a return to the confirmed generation because it cannot change which generation is active, or because there is none to return to, it MUST NOT start an engine run from the failed generation, MUST durably record the stop, MUST stop, and MUST report as R016 requires, until a new desired set or an operator action arrives. A host that stops under R016 records the stop in the same way.

R031 to R033 are UAPI.1 boot counting with one try ([uapi-boot-counting.md](https://github.com/FullFran/hcs/blob/v0.1.0-draft.10/docs/standards/uapi-boot-counting.md)), where the counter lives on disk: a host cannot forget a failure by restarting, and cannot confirm a generation it never checked. The order of writes is: (1) record pending and confirmed; (2) change the active pointer; (3) health check; (4a) on success record confirmed = pending and clear pending, or (4b) on failure record failed, change the pointer back, clear pending. A crash between any two steps is handled by R033: after (1) alone, item 2; after (2), item 1; after the failed mark of (4b) but before the pointer change, item 4; with no pending record at all but a new active generation, item 3; after a stop was recorded, item 5, which outranks the others so that a restart cannot turn a stop into a loop. When the active generation is recorded as failed, item 4 takes precedence over item 1.

Marks are small and permanent: identifiers are never reused (R032), so a failed record needs no lifetime. A host that bounds the set of marks MUST NOT allow an identifier to be reused. A host with no records, for example one that persisted nothing before this section, finds its active generation under item 3, health-checks it once and confirms or fails it; it has no confirmed generation to return to, so a failing check ends in R035. A host that can vouch for its current active generation SHOULD seed `confirmed` with it when it first records facts.

No HCS-0006 code is defined for a failed pointer change: it is a host-internal fault, not a plugin or health failure, and R015 already reports the original failure with `plugin_failed` or `timeout`. A host reports the pointer fault as it reports other conditions HCS does not define (HCS-0006 section 3, last paragraph): outside the HCS error object, for example as a problem type its distribution owns. [ADR-0014](https://github.com/FullFran/hcs/blob/v0.1.0-draft.10/docs/adr/0014-profile-error-codes-join-the-core-registry.md) applies the same reasoning to out-of-band modification: a code needs a defined condition and more than one implementation. Revisit when a second host reports the same condition. The decision is recorded in [ADR-0031](https://github.com/FullFran/hcs/blob/v0.1.0-draft.10/docs/adr/0031-crash-safe-generation-switch.md).

## 8. Conformance

`recovery` cases run the reference function `conformance/runner/recovery.py`: the input states the persisted facts and the generations on disk, the expected output is the action a host takes after a restart and the generations a cleanup retains. R033 and R034 are checked by `recovery` cases, which use abstract facts, not a storage format. R031, R032 and R035 have `recovery` cases for the decision they imply and `host` cases for the writes and faults that need a running host, reported `pending`.

`projection` cases run the reference projection in `conformance/runner/run.py`: the input states the generation, the kinds the adapter projects, the host-owned settings and the granted secrets; the expected output is the errors per entry and the entries the host may report active (`selected`). R001, R002, R008, R013 to R016, R019, R020, R022, R025, R028 and R030 need a running engine (R030 depends on what each engine interprets, which the reference projection does not model) and have `host` cases only, reported `pending`. R017 is checked by `projection` cases under `all-or-nothing`. R009, R018 and R039 are SHOULDs and have no cases. R036 has `document` cases against the `clientLimits` schema; R037 and R038 have `projection` cases (`clientLimits` in the input, `toolListBytes` on the contribution). R010 is checked by the reference runner only for the members `server.json` requires (`name`, `description`, `version`); full schema validation is the adapter's. R021, R026, R027 and R029 have both `projection` or `document` cases and `host` cases: their shape is checked by schema or the reference projection, and their behaviour needs a running engine.

## 9. Open questions

Closed by [ADR-0022](https://github.com/FullFran/hcs/blob/v0.1.0-draft.10/docs/adr/0022-host-adapter-open-questions-decided.md) (2026-11-06): OQ-0008-1 (no core `instructions` kind until a second harness), OQ-0008-4 (no health-check code; R018), OQ-0008-5 (unprojectable entries are reported, not fatal; R017), OQ-0008-6 (no host adapter profile until a second host).

- OQ-0008-2: a shared vocabulary for host-owned settings (permission policy, safety proxy, reserved names), so two hosts can declare them the same way (HCS-0005 OQ-0005-1).
- OQ-0008-3: whether projection outcomes need their own status document, or HCS-0004 status with HCS-0006 errors is enough. The first host needs only the latter.
- OQ-0008-7: whether an adapter may project a `server.json` package of a registry type other than `file`, which makes the engine fetch code that the signed catalog does not cover. The first host refuses them; the current requirements allow them.
- OQ-0008-8: whether the line-delimited JSON stdio protocol that two harnesses use to drive an engine, instead of ACP (section 6.8), becomes a second engine profile, a narrower ACP profile, or stays a distribution extension. Proposed in [ADR-0038](https://github.com/FullFran/hcs/blob/v0.1.0-draft.10/docs/adr/0038-line-delimited-engine-protocol.md); this document does not change.
- OQ-0008-9: whether a server may declare its tool-list size statically so that R037 does not need a probe run, and whether a client's limits can be discovered instead of declared.

## Appendix A. Engine mappings (non-normative)

Observed by the first host on 2026-11-06 at the engine versions shown; not verified independently and not part of the contract. Engine behaviour changes between versions; an adapter pins the versions it supports (HCS-0001 `compatibility.hosts`).

| Concern | OpenCode 1.18.32, 1.19.5 | Pi 1.5.0 | Claude Code 2.1.318 |
| --- | --- | --- | --- |
| Skills | `skills.paths` (recursive scan for `SKILL.md`), in a config file or the `OPENCODE_CONFIG_CONTENT` environment value | `--skill <dir>` (repeatable) or `settings.json` `{"skills":[<dir>]}` | `--add-dir <dir>` loads `<dir>/.claude/skills/<name>/SKILL.md`; `--plugin-dir <package>` loads one package as a plugin with names `<plugin>:<skill>` (R009 not met); a folder of packages without manifests loads nothing; `permissions.additionalDirectories` does not load skills |
| MCP servers | `mcp{}`; headers accept `{env:VAR}` references | `~/.pi/agent/mcp.json` | managed `managed-mcp.json` |
| Interpreted MCP values (R030) | `{env:VAR}` in headers; the engine documents `{env:…}` and `{file:…}` substitution across its configuration (not verified by a host) | env and header values: a leading `!` runs a shell command, `$VAR` expands; escape `$` as `$$` and a leading `!` as `$!` (checked against the engine's own resolver) | the engine documents `${VAR}` and `${VAR:-default}` expansion in MCP command, args, env, url and headers (not verified by a host) |
| Instructions | `instructions[]` | not observed | managed `CLAUDE.md` |
| Permissions | `permission{}` | not observed | managed `managed-settings.json` |
| Host-owned layer | host config file, not writable by generation content | not observed | `/etc/claude-code/` managed files, highest precedence |

Consequences for an adapter, in this document's terms:

- One generation per run (R001): point `skills.paths`, `--skill` or `--add-dir` at the resolved generation directory (`<generation>/...`), not at `active/...`.
- Claude Code needs a view of the generation in its own layout: a per-run directory with `.claude/skills/<name>` links to each skill directory of the generation (R008 forbids copying with changes, not linking).
- MCP: translate `server.json` into the engine's MCP block; write secret inputs as environment references the host fills (R012).
- An engine column marked "not observed" means the adapter reports `unsupported_contribution` for that kind (R004) until a mapping exists; the rest of the generation proceeds (R017).
- Agent and command definitions are engine-specific and stay per-engine extension kinds outside the normative text; an engine that cannot take them reports them under R004 ([ADR-0022](https://github.com/FullFran/hcs/blob/v0.1.0-draft.10/docs/adr/0022-host-adapter-open-questions-decided.md), decision 6).
