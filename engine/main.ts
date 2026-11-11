/**
 * The in-app coach's engine process: one person, one conversation, on the Pi SDK.
 *
 * training-mcp starts this program and talks to it over stdin and stdout with the
 * protocol of `protocol.ts`. It gets no credential on its command line or in its
 * environment: it asks the host for the current access token when the model
 * runtime needs one, and the host alone decides when to refresh. It has no shell
 * and no file tools; its only tools are training-mcp's MCP tools, reached on
 * loopback with a short-lived token that acts as this one person.
 *
 * Re-implemented from RE;write's embedded engine (engine/main.ts there).
 */
import { mkdirSync, mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import type { Credential, CredentialStore } from '@earendil-works/pi-ai';
import { openaiCodexProvider } from '@earendil-works/pi-ai/providers/openai-codex';
import {
  createAgentSession, createMcpExtension, DefaultResourceLoader, ModelRuntime, SessionManager, SettingsManager, VERSION,
  type AgentSession,
} from '@earendil-works/pi-coding-agent';
import {
  composePrompt, DEVICE_VERIFICATION_URI, encode, LineDecoder, MCP_SERVER_NAME, MCP_TOOL_PREFIX, parseCredential,
  type ChildMessage, type EngineErrorCode, type ParentMessage,
} from './protocol.ts';

const PROVIDER = 'openai-codex';
/** Pi asks for five minutes of validity; the host hands out at least ten. */
const TOKEN_REUSE_MARGIN_MS = 6 * 60_000;
const MCP_CONNECT_WAIT_MS = 15_000;

// stdout carries the protocol and nothing else.
const writeLine = process.stdout.write.bind(process.stdout);
process.stdout.write = (() => true) as typeof process.stdout.write;
console.log = console.info = console.debug = console.warn = console.error = () => {};
const send = (message: ChildMessage): void => { writeLine(encode(message)); };

// A private home: nothing of this process reads or writes the operator's Pi files.
const home = mkdtempSync(join(tmpdir(), 'training-engine-'));
const agentDir = join(home, 'agent');
const cwd = join(home, 'work');
mkdirSync(agentDir);
mkdirSync(cwd);
process.env.HOME = home;
process.env.PI_CODING_AGENT_DIR = agentDir;
process.on('exit', () => rmSync(home, { recursive: true, force: true }));

class Reconnect extends Error {}

/** Maps anything thrown or reported by the SDK to a fixed code. The text never leaves. */
function codeOf(error: unknown): EngineErrorCode {
  if (error instanceof Reconnect) return 'reconnect';
  const text = error instanceof Error ? error.message : String(error ?? '');
  if (/\b(429|503|529)\b|overloaded|rate.?limit|capacity|too many requests/i.test(text)) return 'model_busy';
  if (/\b(401|403)\b|unauthori[sz]ed|invalid.?(token|grant)|token.*(expired|revoked)|sign in again|reconnect/i.test(text)) return 'reconnect';
  return 'provider_error';
}

// --- Access token, held by the host --------------------------------------------------

const tokenWaiters = new Map<string, (message: Extract<ParentMessage, { type: 'token' }>) => void>();
let tokenSeq = 0;
let cached: Credential | undefined;

async function currentCredential(): Promise<Credential | undefined> {
  if (cached?.type === 'oauth' && cached.expires - Date.now() > TOKEN_REUSE_MARGIN_MS) return cached;
  const id = `t-${++tokenSeq}`;
  const reply = await new Promise<Extract<ParentMessage, { type: 'token' }>>((resolve) => {
    tokenWaiters.set(id, resolve);
    send({ type: 'token.get', id });
  });
  if (reply.error === 'not_connected') return undefined;
  if (reply.error || !reply.access || !reply.accountId || !reply.expires) throw new Reconnect('reconnect');
  // The refresh token stays with the host: this store can never refresh on its own.
  cached = { type: 'oauth', access: reply.access, refresh: 'held-by-host', expires: reply.expires, accountId: reply.accountId };
  return cached;
}

const credentials: CredentialStore = {
  read: async (providerId) => providerId === PROVIDER ? currentCredential() : undefined,
  list: async () => [{ providerId: PROVIDER, type: 'oauth' }],
  // Pi refreshes inside `modify`. Here the host owns the refresh, so the callback is
  // not run and the answer is whatever the host holds now.
  modify: async (providerId) => {
    if (providerId !== PROVIDER) return undefined;
    cached = undefined;
    return currentCredential();
  },
  delete: async () => {},
};

// --- Login and refresh, on behalf of the host ------------------------------------------

let loginAbort: AbortController | undefined;

async function login(): Promise<void> {
  loginAbort?.abort();
  const abort = new AbortController();
  loginAbort = abort;
  try {
    const credential = await openaiCodexProvider().auth.oauth!.login({
      signal: abort.signal,
      // Only the device-code flow works on a server; the browser flow redirects to localhost.
      prompt: async (prompt) => {
        if (prompt.type === 'select' && prompt.options.some((option) => option.id === 'device_code')) return 'device_code';
        throw new Error('unsupported login prompt');
      },
      notify: (event) => {
        if (event.type !== 'device_code') return;
        if (event.verificationUri !== DEVICE_VERIFICATION_URI) return abort.abort();
        send({ type: 'login.code', userCode: event.userCode });
      },
    });
    const stored = parseCredential(credential);
    if (abort.signal.aborted) return;
    send(stored ? { type: 'login.done', credential: stored } : { type: 'login.failed' });
  } catch {
    if (!abort.signal.aborted) send({ type: 'login.failed' });
  } finally {
    if (loginAbort === abort) loginAbort = undefined;
  }
}

async function refresh(message: Extract<ParentMessage, { type: 'refresh' }>): Promise<void> {
  try {
    const next = parseCredential(await openaiCodexProvider().auth.oauth!.refresh({ type: 'oauth', ...message.credential }, AbortSignal.timeout(30_000)));
    send(next ? { type: 'refresh.done', id: message.id, credential: next } : { type: 'refresh.failed', id: message.id });
  } catch {
    send({ type: 'refresh.failed', id: message.id });
  }
}

// --- The conversation ------------------------------------------------------------------

let runtime: ModelRuntime | undefined;
let session: Promise<AgentSession> | undefined;
let modelChosen = false;
let running = false;
let aborting = false;

async function createSession(start: Extract<ParentMessage, { type: 'start' }>): Promise<AgentSession> {
  // No network here: the model catalog is refreshed on the first prompt.
  runtime = await ModelRuntime.create({ credentials, modelsPath: null, allowModelNetwork: true, refreshOnCreate: false });
  const settingsManager = SettingsManager.inMemory({ transport: 'sse' });
  const resourceLoader = new DefaultResourceLoader({
    cwd,
    agentDir,
    settingsManager,
    noSkills: true,
    noPromptTemplates: true,
    noThemes: true,
    noContextFiles: true,
    systemPromptOverride: () => start.systemPrompt,
    appendSystemPromptOverride: () => [],
    extensionFactories: [createMcpExtension({
      // The one server, given here and never read from an mcp.json. `direct` declares its
      // tools to the model; the default exposure would switch on a script tool instead.
      loadConfig: () => ({
        servers: [{
          name: MCP_SERVER_NAME,
          source: 'host',
          config: { type: 'http', url: start.mcpUrl, headers: { Authorization: `Bearer ${start.mcpToken}` }, exposure: 'direct' },
        }],
        autoEnableCodemode: false,
        errors: [],
      }),
      logPath: join(agentDir, 'mcp.log'),
      openUrl: () => {},
    })],
  });
  await resourceLoader.reload();
  const model = runtime.getModels(PROVIDER)[0];
  if (!model) throw new Error('no model');
  const created = await createAgentSession({
    cwd,
    agentDir,
    modelRuntime: runtime,
    model,
    resourceLoader,
    settingsManager,
    sessionManager: SessionManager.inMemory(cwd),
    // No shell, no file tools. Extension tools, which are the MCP tools, stay.
    noTools: 'builtin',
  });
  await created.session.bindExtensions({});

  const deadline = Date.now() + MCP_CONNECT_WAIT_MS;
  let tools = created.session.getActiveToolNames();
  while (!tools.some((name) => name.startsWith(MCP_TOOL_PREFIX)) && Date.now() < deadline) {
    await new Promise((resolve) => setTimeout(resolve, 100));
    tools = created.session.getActiveToolNames();
  }
  // Fail closed, here and again in the host: training-mcp's MCP tools and nothing else.
  send({ type: 'session', tools });
  if (tools.length === 0 || tools.some((name) => !name.startsWith(MCP_TOOL_PREFIX))) {
    created.session.dispose();
    throw new Error('unexpected tools');
  }

  const calls = new Map<string, number>();
  let nextCall = 0;
  created.session.subscribe((event) => {
    if (!running) return;
    if (event.type === 'message_update' && event.assistantMessageEvent.type === 'text_delta') {
      send({ type: 'delta', text: event.assistantMessageEvent.delta });
    } else if (event.type === 'tool_execution_start') {
      // A small number pairs the start and the end of one call; Pi's own call id stays here.
      nextCall = (nextCall % 100_000) + 1;
      calls.set(event.toolCallId, nextCall);
      send({ type: 'tool', phase: 'start', call: nextCall, name: event.toolName, ok: true });
    } else if (event.type === 'tool_execution_end') {
      const call = calls.get(event.toolCallId);
      calls.delete(event.toolCallId);
      // Only the name and the outcome leave; never an argument or a result.
      send({ type: 'tool', phase: 'end', name: event.toolName, ok: !event.isError, ...(call ? { call } : {}) });
    }
  });
  return created.session;
}

/** The first visible model of the person's account, in the provider's own order. */
async function chooseModel(active: AgentSession): Promise<void> {
  if (modelChosen || !runtime) return;
  const result = await runtime.refresh({ allowNetwork: true, providers: [PROVIDER], signal: AbortSignal.timeout(20_000) });
  const failure = result.errors.get(PROVIDER);
  if (failure) throw failure;
  const available = await runtime.getAvailable();
  // Only the person's own account may serve a request, whatever else Pi has registered.
  if (available.some((model) => model.provider !== PROVIDER)) throw new Error('unexpected provider');
  const first = available[0];
  if (!first) throw new Reconnect('reconnect');
  await active.setModel(first);
  modelChosen = true;
}

async function prompt(text: string): Promise<void> {
  if (running) return send({ type: 'error', code: 'turn_in_progress' });
  if (!session) return send({ type: 'error', code: 'engine_unavailable' });
  running = true;
  aborting = false;
  try {
    let active: AgentSession;
    try {
      active = await session;
    } catch {
      send({ type: 'error', code: 'engine_unavailable' });
      return process.exit(1);
    }
    await chooseModel(active);
    await active.prompt(text);
    const last = active.messages.at(-1) as { role?: string; stopReason?: string; errorMessage?: string } | undefined;
    if (aborting || last?.stopReason === 'aborted') send({ type: 'done', aborted: true });
    else if (last?.role === 'assistant' && last.stopReason === 'error') send({ type: 'error', code: codeOf(last.errorMessage) });
    else send({ type: 'done', aborted: false });
  } catch (error) {
    send(aborting ? { type: 'done', aborted: true } : { type: 'error', code: codeOf(error) });
  } finally {
    running = false;
  }
}

function handle(message: ParentMessage): void {
  switch (message.type) {
    case 'start':
      session ??= createSession(message);
      session.catch(() => {});
      return;
    case 'login.start':
      return void login();
    case 'login.cancel':
      return loginAbort?.abort();
    case 'refresh':
      return void refresh(message);
    case 'token':
      tokenWaiters.get(message.id)?.(message);
      tokenWaiters.delete(message.id);
      return;
    case 'prompt':
      return void prompt(composePrompt(message.context, message.text));
    case 'abort':
      aborting = running;
      return void session?.then((active) => active.abort(), () => {});
    case 'shutdown':
      return process.exit(0);
  }
}

const decoder = new LineDecoder();
process.stdin.on('data', (chunk: Buffer) => {
  let values: unknown[];
  try {
    values = decoder.push(chunk);
  } catch {
    return process.exit(1);
  }
  for (const value of values) {
    if (typeof value === 'object' && value !== null && typeof (value as { type?: unknown }).type === 'string') handle(value as ParentMessage);
  }
});
// The host is gone: so is this process.
process.stdin.on('end', () => process.exit(0));
process.on('uncaughtException', () => process.exit(1));
process.on('unhandledRejection', () => {});

send({ type: 'ready', piVersion: VERSION });
