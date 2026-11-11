/**
 * The real engine against a fake host: `node --test engine/main.test.ts`
 * (Node 24, installed engine/node_modules). No model is called: the test ends
 * when the engine reports the tools of its session.
 *
 * HCS-0008-R025: MCP servers, settings and credentials planted in the user's
 * and the workspace's configuration have no effect; only the servers the host
 * gives in its start message load.
 */
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { createServer, type IncomingMessage, type ServerResponse } from 'node:http';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { test } from 'node:test';
import { fileURLToPath } from 'node:url';
import { LineDecoder } from './protocol.ts';

const here = dirname(fileURLToPath(import.meta.url));

/** A minimal Streamable HTTP MCP server per path, recording every path and bearer it sees. */
function fakeHost() {
  const seen: { path: string; auth: string }[] = [];
  const tools: Record<string, string[]> = {
    '/mcp': ['get_session', 'log_sets'],
    '/engine/connectors/notion/mcp': ['search', 'create-page'],
    '/planted/mcp': ['planted_tool'],
  };
  const server = createServer((req: IncomingMessage, res: ServerResponse) => {
    seen.push({ path: req.url ?? '', auth: req.headers.authorization ?? '' });
    if (req.method !== 'POST' || !(req.url! in tools)) {
      res.writeHead(req.method === 'POST' ? 404 : 405).end();
      return;
    }
    let body = '';
    req.on('data', (c) => { body += c; });
    req.on('end', () => {
      const msg = JSON.parse(body) as { id?: number; method: string };
      if (msg.id === undefined) {
        res.writeHead(202).end();
        return;
      }
      const result = msg.method === 'initialize'
        ? { protocolVersion: '2025-06-18', capabilities: { tools: {} }, serverInfo: { name: 'fake', version: '1' } }
        : msg.method === 'tools/list'
          ? { tools: tools[req.url!].map((name) => ({ name, description: name, inputSchema: { type: 'object', properties: {} } })) }
          : {};
      res.writeHead(200, { 'content-type': 'application/json', 'mcp-session-id': 's1' });
      res.end(JSON.stringify({ jsonrpc: '2.0', id: msg.id, result }));
    });
  });
  return { server, seen };
}

test('only the servers the host gives load; planted configuration has no effect', { timeout: 60_000 }, async () => {
  const { server, seen } = fakeHost();
  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve));
  const port = (server.address() as { port: number }).port;
  const origin = `http://127.0.0.1:${port}`;

  // A user home and a workspace full of configuration a stock Pi would read.
  const planted = mkdtempSync(join(tmpdir(), 'engine-planted-'));
  const plantedServer = { mcpServers: { planted: { url: `${origin}/planted/mcp` } } };
  for (const dir of [join(planted, '.pi', 'agent'), join(planted, 'work', '.pi')]) {
    mkdirSync(dir, { recursive: true });
    writeFileSync(join(dir, 'mcp.json'), JSON.stringify(plantedServer));
    writeFileSync(join(dir, 'settings.json'), JSON.stringify({ defaultProvider: 'anthropic' }));
  }
  writeFileSync(join(planted, 'work', '.mcp.json'), JSON.stringify(plantedServer));
  writeFileSync(join(planted, '.pi', 'agent', 'auth.json'), JSON.stringify({ anthropic: { type: 'api_key', key: 'planted-key' } }));

  const child = spawn(process.execPath, [join(here, 'main.ts')], {
    cwd: join(planted, 'work'),
    env: { PATH: process.env.PATH ?? '', HOME: planted, PI_CODING_AGENT_DIR: join(planted, '.pi', 'agent'), ANTHROPIC_API_KEY: 'planted-key' },
    stdio: ['pipe', 'pipe', 'ignore'],
  });
  const decoder = new LineDecoder();
  const session = new Promise<string[]>((resolve, reject) => {
    child.stdout.on('data', (chunk: Buffer) => {
      for (const m of decoder.push(chunk) as { type?: string; tools?: string[] }[]) {
        if (m?.type === 'ready') {
          child.stdin.write(`${JSON.stringify({
            type: 'start', mcpUrl: `${origin}/mcp`, mcpToken: 'tme_test', systemPrompt: 'x',
            servers: [{ name: 'notion', url: `${origin}/engine/connectors/notion/mcp` }],
          })}\n`);
        }
        if (m?.type === 'session') resolve(m.tools ?? []);
      }
    });
    child.on('exit', () => reject(new Error('engine exited before its session')));
  });
  try {
    const tools = (await session).sort();
    assert.deepEqual(tools, ['mcp__notion__create_page', 'mcp__notion__search', 'mcp__training__get_session', 'mcp__training__log_sets']);
    assert.ok(!seen.some((s) => s.path.startsWith('/planted')), 'a planted server was contacted');
    // Every request carried the host's engine token and nothing else.
    assert.ok(seen.length > 0 && seen.every((s) => s.auth === 'Bearer tme_test'), JSON.stringify(seen));
  } finally {
    child.stdin.end();
    child.kill();
    server.close();
    rmSync(planted, { recursive: true, force: true });
  }
});
