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

export const MAX_LINE_BYTES = 1_048_576;
const MAX_SECRET_CHARS = 16_384;

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

export type ParentMessage =
  | { type: 'start'; mcpUrl: string; mcpToken: string; systemPrompt: string }
  | { type: 'login.start' }
  | { type: 'login.cancel' }
  | { type: 'refresh'; id: string; credential: StoredCredential }
  | { type: 'token'; id: string; access?: string; accountId?: string; expires?: number; error?: EngineErrorCode }
  /** `context` is the host's lines for this turn; the person's text never comes before them. */
  | { type: 'prompt'; text: string; context?: string }
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
  | { type: 'session'; tools: string[] }
  | { type: 'delta'; text: string }
  | { type: 'tool'; name: string; phase: 'start' | 'end'; ok: boolean; call?: number; pending?: boolean }
  | { type: 'done'; aborted: boolean }
  | { type: 'error'; code: EngineErrorCode };

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
    this.buffer += typeof chunk === 'string' ? chunk : chunk.toString('utf8');
    const out: unknown[] = [];
    let index: number;
    while ((index = this.buffer.indexOf('\n')) >= 0) {
      const line = this.buffer.slice(0, index);
      this.buffer = this.buffer.slice(index + 1);
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
