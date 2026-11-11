/**
 * Protocol tests for the engine side: `node --test engine/protocol.test.ts`.
 * No Pi, no network. The Go side's tests are internal/engine/*_test.go.
 */
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { LineDecoder, MAX_IMAGES, parseImages, pickModel, thinkingLevels } from './protocol.ts';

const jpeg = Buffer.concat([Buffer.from([0xff, 0xd8, 0xff, 0xe0]), Buffer.alloc(16, 'j')]).toString('base64');

test('a prompt line from the host decodes with its photos', () => {
  // The shape internal/engine encodes: {"type":"prompt",...,"images":[{"mimeType","data"}]}.
  const line = JSON.stringify({ type: 'prompt', text: 'mira', images: [{ mimeType: 'image/jpeg', data: jpeg }] });
  const [message] = new LineDecoder().push(`${line}\n`) as { images?: unknown }[];
  assert.deepEqual(parseImages(message.images), [{ type: 'image', data: jpeg, mimeType: 'image/jpeg' }]);
});

test('a prompt without photos has none', () => {
  assert.deepEqual(parseImages(undefined), []);
});

test('photos outside the limits are refused', () => {
  const ok = { mimeType: 'image/png', data: jpeg };
  const cases: [string, unknown][] = [
    ['not a list', { mimeType: 'image/jpeg', data: jpeg }],
    ['too many', Array.from({ length: MAX_IMAGES + 1 }, () => ok)],
    ['gif', [{ mimeType: 'image/gif', data: jpeg }]],
    ['svg', [{ mimeType: 'image/svg+xml', data: jpeg }]],
    ['no type', [{ data: jpeg }]],
    ['empty', [{ mimeType: 'image/jpeg', data: '' }]],
    ['not base64', [{ mimeType: 'image/jpeg', data: 'ab$=' }]],
    ['a url', [{ mimeType: 'image/jpeg', data: 'https://example.com/a.jpg' }]],
    ['too large', [{ mimeType: 'image/jpeg', data: 'A'.repeat(4 * 1_048_576) }]],
    ['null item', [null]],
  ];
  for (const [name, value] of cases) assert.equal(parseImages(value), null, name);
});

test('a multi-megabyte line arriving in small chunks decodes once', () => {
  const data = Buffer.alloc(6 * 1_048_576, 7).toString('base64');
  const text = `${JSON.stringify({ type: 'prompt', text: 'x', images: [{ mimeType: 'image/jpeg', data }] })}\n{"type":"abort"}\n`;
  const decoder = new LineDecoder();
  const out: unknown[] = [];
  for (let i = 0; i < text.length; i += 65_536) out.push(...decoder.push(text.slice(i, i + 65_536)));
  assert.equal(out.length, 2);
  assert.equal((out[0] as { images: { data: string }[] }).images[0].data, data);
  assert.deepEqual(out[1], { type: 'abort' });
});

test('a line over the limit is a protocol violation', () => {
  assert.throws(() => new LineDecoder().push('x'.repeat(13 * 1_048_576)));
});

test('thinking levels follow what the model declares', () => {
  assert.deepEqual(thinkingLevels({ reasoning: false }), ['off']);
  assert.deepEqual(thinkingLevels({ reasoning: true }), ['off', 'minimal', 'low', 'medium', 'high']);
  assert.deepEqual(thinkingLevels({ reasoning: true, thinkingLevelMap: { minimal: null, xhigh: 'xhigh' } }), ['off', 'low', 'medium', 'high', 'xhigh']);
});

test('a chosen model is used only when the account lists it', () => {
  const available = [{ id: 'gpt-a', provider: 'openai-codex' }, { id: 'gpt-b', provider: 'openai-codex' }];
  assert.equal(pickModel(available, 'gpt-b')?.id, 'gpt-b');
  assert.equal(pickModel(available, 'gpt-4o-elsewhere')?.id, 'gpt-a');
  assert.equal(pickModel(available, undefined)?.id, 'gpt-a');
  assert.equal(pickModel([], 'gpt-a'), undefined);
});
