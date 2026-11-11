/**
 * The engine's side of the line-delimited JSON protocol with training-mcp
 * (internal/engine/protocol.go is the host's side and the authority on it).
 *
 * The host writes one JSON object per line to stdin; the engine answers one per
 * line on stdout. Provider error text never has a field to travel in: errors are
 * a closed set of codes.
 */

export const ENGINE_ERROR_CODES = [
  'model_busy', 'reconnect', 'provider_error', 'busy', 'turn_in_progress',
  'not_connected', 'engine_unavailable', 'login_failed', 'rate_limited', 'turn_limit',
] as const;
export type EngineErrorCode = typeof ENGINE_ERROR_CODES[number];

/** Where a person types the device code. Fixed on both sides. */
export const DEVICE_VERIFICATION_URI = 'https://auth.openai.com/codex/device';
/** Name training-mcp's MCP server is registered under; tools are `mcp__training__<tool>`. */
export const MCP_SERVER_NAME = 'training';
export const MCP_TOOL_PREFIX = `mcp__${MCP_SERVER_NAME}__`;

/**
 * The longest line the host may write. A prompt with photos carries them in
 * base64 (internal/engine/images.go: at most 6 MiB of photos per turn), so this
 * is far above what the engine itself ever sends back (1 MiB on the host side).
 */
export const MAX_LINE_BYTES = 12 * 1_048_576;
const MAX_SECRET_CHARS = 16_384;

/** Photo limits, the host's (internal/engine/images.go), checked again here. */
export const MAX_IMAGES = 4;
const MAX_IMAGE_BASE64_CHARS = Math.ceil((2 * 1_048_576) / 3) * 4;
const IMAGE_TYPES = new Set(['image/jpeg', 'image/png', 'image/webp']);
const BASE64 = /^[A-Za-z0-9+/]+={0,2}$/;

/** A photo as the model API takes it. */
export interface PromptImage {
  type: 'image';
  data: string;
  mimeType: string;
}

/** The photos of a prompt, or null when anything about them is off. */
export function parseImages(value: unknown): PromptImage[] | null {
  if (value === undefined) return [];
  if (!Array.isArray(value) || value.length > MAX_IMAGES) return null;
  const out: PromptImage[] = [];
  for (const item of value) {
    const image = item as { mimeType?: unknown; data?: unknown } | null;
    if (typeof image !== 'object' || image === null) return null;
    const { mimeType, data } = image;
    if (typeof mimeType !== 'string' || !IMAGE_TYPES.has(mimeType)) return null;
    if (typeof data !== 'string' || data.length === 0 || data.length > MAX_IMAGE_BASE64_CHARS || data.length % 4 !== 0 || !BASE64.test(data)) return null;
    out.push({ type: 'image', data, mimeType });
  }
  return out;
}

/** A person's ChatGPT credential. The refresh token is single-use and rotates. */
export interface StoredCredential {
  access: string;
  refresh: string;
  expires: number;
  accountId: string;
}

const isSecret = (value: unknown): value is string => typeof value === 'string' && value.length > 0 && value.length <= MAX_SECRET_CHARS;

export function parseCredential(value: unknown): StoredCredential | null {
  const c = value as Partial<StoredCredential> | null;
  if (typeof c !== 'object' || c === null) return null;
  if (!isSecret(c.access) || !isSecret(c.refresh) || !isSecret(c.accountId)) return null;
  if (typeof c.expires !== 'number' || !Number.isFinite(c.expires) || c.expires <= 0) return null;
  return { access: c.access, refresh: c.refresh, expires: c.expires, accountId: c.accountId };
}

/** A person's connector, reached through the host's proxy with the engine token. */
export interface ConnectorServer {
  name: string;
  url: string;
}

const SERVER_NAME = /^[a-z][a-z0-9]{0,23}$/;
export const MAX_SERVERS = 16;

/**
 * The connectors of a start message, or null when anything is off. A server's
 * URL must be the host's own proxy for that name, on the origin of the host's
 * /mcp: the engine token goes nowhere else (HCS-0008-R012: the engine never
 * holds a third-party credential; the host's proxy adds it).
 */
export function parseServers(value: unknown, mcpUrl: string): ConnectorServer[] | null {
  if (value === undefined) return [];
  if (!Array.isArray(value) || value.length > MAX_SERVERS) return null;
  let origin: string;
  try {
    origin = new URL(mcpUrl).origin;
  } catch {
    return null;
  }
  const seen = new Set<string>();
  const out: ConnectorServer[] = [];
  for (const item of value) {
    const server = item as { name?: unknown; url?: unknown } | null;
    if (typeof server !== 'object' || server === null) return null;
    const { name, url } = server;
    if (typeof name !== 'string' || !SERVER_NAME.test(name) || name === MCP_SERVER_NAME || seen.has(name)) return null;
    if (typeof url !== 'string' || url !== `${origin}/engine/connectors/${name}/mcp`) return null;
    seen.add(name);
    out.push({ name, url });
  }
  return out;
}

/** The connector a tool belongs to, or undefined (training's own tools included). */
export function connectorOf(tool: string, names: ReadonlySet<string>): string | undefined {
  const match = /^mcp__([a-z][a-z0-9]{0,23})__./.exec(tool);
  return match && names.has(match[1]) ? match[1] : undefined;
}

/** The longest arguments text shown to the person with an approval request. */
export const MAX_APPROVAL_ARGS = 2000;

export type ParentMessage =
  | { type: 'start'; mcpUrl: string; mcpToken: string; systemPrompt: string; servers?: ConnectorServer[] }
  | { type: 'approval'; id: string; decision: 'allow' | 'deny' }
  | { type: 'login.start' }
  | { type: 'login.cancel' }
  | { type: 'refresh'; id: string; credential: StoredCredential }
  | { type: 'token'; id: string; access?: string; accountId?: string; expires?: number; error?: EngineErrorCode }
  /**
   * `context` is the host's lines for this turn; the person's text never comes before them.
   * `images` are the person's photos for this turn only, in base64.
   */
  | { type: 'prompt'; text: string; context?: string; images?: { mimeType: string; data: string }[]; model?: string; thinking?: string }
  /** Asks for the models the person's login can use and the current choice. */
  | { type: 'models.get' }
  | { type: 'abort' }
  | { type: 'shutdown' };

export type ChildMessage =
  | { type: 'ready'; piVersion: string }
  | { type: 'login.code'; userCode: string }
  | { type: 'login.done'; credential: StoredCredential }
  | { type: 'login.failed' }
  | { type: 'refresh.done'; id: string; credential: StoredCredential }
  | { type: 'refresh.failed'; id: string }
  | { type: 'token.get'; id: string }
  | { type: 'approval.request'; id: string; name: string; args?: string }
  | { type: 'session'; tools: string[] }
  | { type: 'delta'; text: string }
  /** The model's reasoning text, apart from the answer. */
  | { type: 'thinking'; text: string }
  | { type: 'models'; models: ModelEntry[]; model: string; thinking: string }
  | { type: 'models.failed'; code: EngineErrorCode }
  | { type: 'tool'; name: string; phase: 'start' | 'end'; ok: boolean; call?: number; pending?: boolean }
  | { type: 'done'; aborted: boolean }
  | { type: 'error'; code: EngineErrorCode };

/** One model the person can choose, with the thinking levels it takes. */
export interface ModelEntry {
  id: string;
  name: string;
  thinking: ThinkingLevel[];
}

/** Pi's thinking levels, in order (the host accepts these and nothing else). */
export const THINKING_LEVELS = ['off', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max'] as const;
export type ThinkingLevel = typeof THINKING_LEVELS[number];

/**
 * The thinking levels a model takes, as Pi decides them: none but `off` without
 * reasoning; a level mapped to null is not offered; `xhigh` and `max` only when
 * the model maps them. (Pi's own `getSupportedThinkingLevels` is not exported.)
 */
export function thinkingLevels(model: { reasoning: boolean; thinkingLevelMap?: Partial<Record<string, unknown>> }): ThinkingLevel[] {
  if (!model.reasoning) return ['off'];
  return THINKING_LEVELS.filter((level) => {
    const mapped = model.thinkingLevelMap?.[level];
    if (mapped === null) return false;
    if (level === 'xhigh' || level === 'max') return mapped !== undefined;
    return true;
  });
}

/** The chosen model when the account lists it, else the account's first. */
export function pickModel<T extends { id: string }>(available: T[], id: string | undefined): T | undefined {
  return available.find((model) => model.id === id) ?? available[0];
}

export function encode(message: ChildMessage): string {
  return `${JSON.stringify(message)}\n`;
}

/** The message the model gets: the host's lines first, then the person's text. */
export function composePrompt(context: string | undefined, text: string): string {
  return context === undefined || context === '' ? text : `${context}\n${text}`;
}

/** Splits a byte stream into JSON lines. A line over the limit is a protocol violation. */
export class LineDecoder {
  private buffer = '';

  push(chunk: Buffer | string): unknown[] {
    // A photo line arrives in many chunks: only the new part is searched for its end.
    let from = this.buffer.length;
    this.buffer += typeof chunk === 'string' ? chunk : chunk.toString('utf8');
    const out: unknown[] = [];
    let index: number;
    while ((index = this.buffer.indexOf('\n', from)) >= 0) {
      const line = this.buffer.slice(0, index);
      this.buffer = this.buffer.slice(index + 1);
      from = 0;
      if (!line.trim()) continue;
      try { out.push(JSON.parse(line)); } catch { out.push(undefined); }
    }
    if (this.buffer.length > MAX_LINE_BYTES) {
      this.buffer = '';
      throw new Error('engine protocol line too long');
    }
    return out;
  }
}

// --- Connector tool approvals ----------------------------------------------------------
//
// The engine's half of connector tool approvals (HCS-0005). Before a tool of a
// person's connector runs, the engine asks the host and waits; only an explicit
// "allow" lets the call run. The host decides from its session grants or asks
// the person; the engine never remembers an answer (HCS-0005-R009), and any
// failure here blocks the call (R014). This lives in protocol.ts because the
// image ships main.ts and protocol.ts only.

export const DENIED_REASON = 'The person did not allow this call. Do not retry it unless they ask.';
/** Longer than the host's own deadline, which answers first. */
const ENGINE_WAIT_MS = 5 * 60_000;

export interface ToolCall {
  toolName: string;
  input: unknown;
}

export interface ApprovalGate {
  /** The tool_call hook: undefined lets a call run, a block result stops it. */
  check(call: ToolCall): Promise<{ block: true; reason: string } | undefined>;
  /** A decision from the host. */
  resolve(id: string, decision: unknown): void;
}

export function createApprovalGate(send: (message: ChildMessage) => void, connectors: ReadonlySet<string>, waitMs = ENGINE_WAIT_MS): ApprovalGate {
  const waiting = new Map<string, (allow: boolean) => void>();
  let seq = 0;
  const blocked = { block: true as const, reason: DENIED_REASON };

  return {
    async check(call) {
      try {
        if (call.toolName.startsWith(MCP_TOOL_PREFIX)) return undefined;
        // Anything that is neither training's nor a connector's never runs.
        if (connectorOf(call.toolName, connectors) === undefined) return blocked;
        const id = `a-${++seq}`;
        let args: string | undefined;
        try {
          args = JSON.stringify(call.input ?? {}).slice(0, MAX_APPROVAL_ARGS);
        } catch {
          args = undefined;
        }
        const allow = await new Promise<boolean>((resolve) => {
          const timer = setTimeout(() => { waiting.delete(id); resolve(false); }, waitMs);
          waiting.set(id, (value) => { clearTimeout(timer); resolve(value); });
          send({ type: 'approval.request', id, name: call.toolName, ...(args === undefined ? {} : { args }) });
        });
        return allow ? undefined : blocked;
      } catch {
        return blocked;
      }
    },
    resolve(id, decision) {
      const done = waiting.get(id);
      waiting.delete(id);
      done?.(decision === 'allow');
    },
  };
}
