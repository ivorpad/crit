'use strict';
// herdr-transcript-parse.js reads a Claude Code transcript into turns and rows
// for the /herdr page. The fixtures are made up (herdr-demo-*.jsonl, a fake
// project): no real session is read here.
const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const P = require('../herdr-transcript-parse.js');

const fx = (n) => fs.readFileSync(path.join(__dirname, 'fixtures', n), 'utf8');
const flaky = fx('herdr-demo-flaky-spec.jsonl');
const working = fx('herdr-demo-working.jsonl');

test('every prompt you typed opens a turn; user lines nobody typed are marks', () => {
  const m = P.parseTranscript(flaky);
  const prompts = m.rows.filter((r) => r.kind === 'prompt');
  assert.equal(m.turns.length, 4);
  assert.equal(prompts.length, 4);
  assert.ok(prompts.every((r) => r.typed));
  const tags = new Set(m.rows.filter((r) => r.kind === 'mark').map((r) => r.tag));
  for (const t of ['meta', 'command', 'queued', 'peer', 'task', 'away', 'compact']) assert.ok(tags.has(t), t);
});

test('a tool call and its result are one row; failed and denied calls say so', () => {
  const tools = P.parseTranscript(flaky).rows.filter((r) => r.kind === 'tool');
  assert.ok(tools.every((r) => r.result !== null));
  assert.equal(tools.filter((r) => r.status === 'error').length, 2);
  assert.equal(tools.filter((r) => r.status === 'denied').length, 1);
  assert.equal(tools.find((r) => r.name === 'Grep').headline, 'sleep [0-9.]+');
  assert.equal(tools.find((r) => r.name === 'Agent').headline, 'Audit timing in three specs');
});

test('the last turn ends on its answer even when a recap and a compaction follow it', () => {
  const m = P.parseTranscript(flaky);
  assert.equal(m.last.state, 'answered');
  const answer = m.rows[m.last.answer[0]];
  assert.equal(answer.kind, 'message');
  assert.match(answer.text, /^Done\. Both specs freeze time now/);
  assert.ok(m.last.answer.slice(1).every((i) => m.rows[i].kind === 'mark'));
  assert.ok(m.last.fold.length > 0);
});

test('a turn still running has no answer: its last step is a call with no result', () => {
  const m = P.parseTranscript(working);
  assert.equal(m.last.state, 'working');
  const last = m.rows[m.last.lastRow];
  assert.equal(last.kind, 'tool');
  assert.equal(last.status, 'pending');
  assert.notEqual(m.last.latestText, null);
});

test('the ledger accounts for every line: drawn + not drawn + malformed = parsed', () => {
  const L = P.parseTranscript(flaky).ledger;
  assert.equal(L.drawnLines + L.notDrawn + L.malformed, L.parsed);
  assert.equal(L.malformed, 1);
  assert.equal(L.unplaced['brand-new-type'], 1);
});

test('rows keep their keys when lines are appended, so comment anchors survive a refresh', () => {
  const lines = flaky.split('\n');
  const head = lines.slice(0, Math.floor(lines.length / 2)).join('\n') + '\n';
  const before = P.parseTranscript(head).rows.map((r) => r.key);
  const after = new Set(P.parseTranscript(flaky).rows.map((r) => r.key));
  assert.ok(before.length > 10);
  for (const k of before) assert.ok(after.has(k), k);
});

test('junk and empty input do not throw', () => {
  for (const t of ['', '\n\n', 'not json\n{"type":', '{"no":"type"}\n']) {
    const m = P.parseTranscript(t);
    assert.equal(m.turns.length, 0);
    assert.equal(m.last.state, 'empty');
  }
});

test('a picture keeps a name the page asks the server for (line uuid, block), never its data', () => {
  const m = P.parseTranscript(fx('herdr-demo-images.jsonl'));
  const prompt = m.rows.find((r) => r.kind === 'prompt');
  const tool = (id) => m.rows.find((r) => r.kind === 'tool' && r.toolId === id);
  // pasted into the prompt: its place in the line's content
  assert.deepEqual(prompt.images, [{ uuid: 'img-u1', block: '1', media: 'image/png' }]);
  // a Read's picture: inside the tool result, with the size the Read reports (shown size first)
  assert.deepEqual(tool('t-read').images, [{ uuid: 'img-r1', block: '0.0', media: 'image/png', w: 1220, h: 880 }]);
  // after text in the same result, and no size when the line reports none
  assert.deepEqual(tool('t-shot').images, [{ uuid: 'img-r3', block: '0.1', media: 'image/jpeg' }]);
  // an SVG is not drawn, and a line without a uuid cannot be asked for
  assert.deepEqual(tool('t-svg').images, []);
  assert.deepEqual(tool('t-old').images, []);
  // a picture that is not drawn keeps its placeholder; a drawn one leaves none
  assert.equal(tool('t-svg').result, '[image image/svg+xml]');
  assert.doesNotMatch(prompt.text, /\[image /);
  assert.doesNotMatch(JSON.stringify(m.rows.map((r) => r.images)), /iVBOR|4AAQ|PHN2/);
});

test('a picture the agent sent is a message row that names it', () => {
  const ln = (o) => JSON.stringify(o);
  const m = P.parseTranscript([
    ln({ type: 'user', uuid: 'u1', promptSource: 'typed', message: { role: 'user', content: 'Draw it' } }),
    ln({ type: 'assistant', uuid: 'a1', message: { id: 'm1', role: 'assistant', content: [{ type: 'text', text: 'Here:' }, { type: 'image', source: { type: 'base64', media_type: 'image/webp', data: 'UklGR' } }] } }),
    ln({ type: 'assistant', uuid: 'a2', message: { id: 'm2', role: 'assistant', content: [{ type: 'image', source: { type: 'url', url: 'https://example.com/x.png' } }] } }),
  ].join('\n') + '\n');
  const pics = m.rows.filter((r) => r.kind === 'message' && r.text === '[image]');
  assert.equal(pics.length, 2);
  assert.deepEqual(pics[0].image, { uuid: 'a1', block: '1', media: 'image/webp' });
  assert.equal(pics[1].image, null, 'only base64 blocks can be asked for');
  assert.deepEqual(pics[1].images, []);
});
