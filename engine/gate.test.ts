/** `node --test engine/gate.test.ts`: the connector approval gate and server list, without Pi. */
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { connectorOf, createApprovalGate, DENIED_REASON, parseServers, type ChildMessage } from './protocol.ts';

function gate(waitMs?: number) {
  const sent: ChildMessage[] = [];
  const g = createApprovalGate((m) => sent.push(m), new Set(['notion']), waitMs);
  return { g, sent };
}

test('training tools run without asking', async () => {
  const { g, sent } = gate();
  assert.equal(await g.check({ toolName: 'mcp__training__log_sets', input: {} }), undefined);
  assert.equal(sent.length, 0);
});

test('a connector tool waits for the host and runs only on allow', async () => {
  const { g, sent } = gate();
  const pending = g.check({ toolName: 'mcp__notion__search', input: { query: 'x' } });
  await Promise.resolve();
  assert.deepEqual(sent, [{ type: 'approval.request', id: 'a-1', name: 'mcp__notion__search', args: '{"query":"x"}' }]);
  g.resolve('a-1', 'allow');
  assert.equal(await pending, undefined);

  const denied = g.check({ toolName: 'mcp__notion__search', input: {} });
  await Promise.resolve();
  g.resolve('a-2', 'deny');
  assert.deepEqual(await denied, { block: true, reason: DENIED_REASON });
});

test('anything but an explicit allow blocks', async () => {
  const { g } = gate(20);
  // No answer before the deadline.
  assert.equal((await g.check({ toolName: 'mcp__notion__search', input: {} }))?.block, true);
  // A strange answer.
  const odd = g.check({ toolName: 'mcp__notion__search', input: {} });
  await Promise.resolve();
  g.resolve('a-2', true);
  assert.equal((await odd)?.block, true);
  // A tool of no known server, and arguments that cannot be shown.
  assert.equal((await g.check({ toolName: 'bash', input: {} }))?.block, true);
  assert.equal((await g.check({ toolName: 'mcp__other__x', input: {} }))?.block, true);
  const cyclic: Record<string, unknown> = {};
  cyclic.self = cyclic;
  const c = g.check({ toolName: 'mcp__notion__search', input: cyclic });
  await Promise.resolve();
  g.resolve('a-3', 'allow');
  assert.equal(await c, undefined);
});

test('an answer for one request never settles another', async () => {
  const { g } = gate(30);
  const first = g.check({ toolName: 'mcp__notion__search', input: {} });
  await Promise.resolve();
  g.resolve('a-99', 'allow');
  assert.equal((await first)?.block, true);
});

test('connector servers must be the host proxy on the host origin', () => {
  const mcp = 'http://127.0.0.1:8080/mcp';
  assert.deepEqual(parseServers(undefined, mcp), []);
  assert.deepEqual(parseServers([{ name: 'notion', url: 'http://127.0.0.1:8080/engine/connectors/notion/mcp' }], mcp), [
    { name: 'notion', url: 'http://127.0.0.1:8080/engine/connectors/notion/mcp' },
  ]);
  for (const bad of [
    [{ name: 'notion', url: 'https://mcp.notion.com/mcp' }],
    [{ name: 'notion', url: 'http://127.0.0.1:9999/engine/connectors/notion/mcp' }],
    [{ name: 'notion', url: 'http://127.0.0.1:8080/engine/connectors/other/mcp' }],
    [{ name: 'training', url: 'http://127.0.0.1:8080/engine/connectors/training/mcp' }],
    [{ name: 'Notion', url: 'http://127.0.0.1:8080/engine/connectors/Notion/mcp' }],
    [{ name: 'notion', url: 'http://127.0.0.1:8080/engine/connectors/notion/mcp' }, { name: 'notion', url: 'http://127.0.0.1:8080/engine/connectors/notion/mcp' }],
    'notion',
  ]) {
    assert.equal(parseServers(bad, mcp), null, JSON.stringify(bad));
  }
  assert.equal(connectorOf('mcp__notion__search', new Set(['notion'])), 'notion');
  assert.equal(connectorOf('mcp__training__x', new Set(['notion'])), undefined);
});
