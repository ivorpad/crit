'use strict';
// herdr-page.model.js: the /herdr page's pure parts. State per pane from its
// item, names in the contract's order, a strip that never reorders, page B's
// pass, came back, and the one message Finish review sends.
const { test } = require('node:test');
const assert = require('node:assert/strict');
const path = require('node:path');
const fs = require('node:fs');
const PM = require('../herdr-page.model.js');

const sweep = JSON.parse(fs.readFileSync(path.join(__dirname, 'fixtures', 'herdr-sweep.json'), 'utf8'));

test('a pane\'s state comes from its own fields, in this order: sent, asks, working, unread, read', () => {
  assert.equal(PM.stateOf({ needs_you: true, delivery: { acknowledged: false } }), 'sent');
  assert.equal(PM.stateOf({ needs_you: true, viewed: false, delivery: { acknowledged: true } }), 'asks');
  assert.equal(PM.stateOf({ needs_you: true, viewed: true, status: 'working' }), 'working');
  assert.equal(PM.stateOf({ status: 'done', viewed: false }), 'unread');
  assert.equal(PM.stateOf({ status: 'idle', viewed: false }), 'read');
  assert.equal(PM.stateOf({ status: 'done', viewed: true }), 'read');
  assert.ok(PM.needsAttention('asks') && PM.needsAttention('unread') && !PM.needsAttention('working'));
});

test('a pane\'s name follows the contract: name, custom title, AI title, title, cwd, pane id', () => {
  const it = { name: 'stalled-close', title: 'Blocker 5', cwd: '/x/relay', pane_id: 'wRY:pH' };
  assert.equal(PM.paneName(it, { customTitle: 'Custom', aiTitle: 'AI' }), 'stalled-close');
  assert.equal(PM.paneName(Object.assign({}, it, { name: '' }), { customTitle: 'Custom', aiTitle: 'AI' }), 'Custom');
  assert.equal(PM.paneName(Object.assign({}, it, { name: '' }), { aiTitle: 'AI' }), 'AI');
  assert.equal(PM.paneName(Object.assign({}, it, { name: '' }), null), 'Blocker 5');
  assert.equal(PM.paneName({ cwd: '/x/relay/', pane_id: 'p' }), 'relay');
  assert.equal(PM.paneName({ pane_id: 'w1:p2' }), 'w1:p2');
});

test('the strip keeps the order panes were first seen in, by machine, when the API reorders', () => {
  const first = PM.stripOrder([], sweep.machines, sweep.items);
  assert.equal(first.length, sweep.items.length);
  const machineRank = (p) => sweep.machines.findIndex((m) => m.label === sweep.items.find((i) => i.path === p).machine);
  for (let i = 1; i < first.length; i++) assert.ok(machineRank(first[i - 1]) <= machineRank(first[i]));
  const again = PM.stripOrder(first, sweep.machines, sweep.items.slice().reverse());
  assert.deepEqual(again, first);
  const gone = PM.stripOrder(first, sweep.machines, sweep.items.slice(1));
  assert.deepEqual(gone, first.filter((p) => p !== sweep.items[0].path));
});

test('the pass: queue after a pane, arrivals at the end, a detour resumes where you left', () => {
  const order = ['a', 'b', 'c', 'd', 'e'];
  const states = { a: 'asks', b: 'read', c: 'unread', d: 'asks', e: 'read' };
  const pass = PM.startPass(order, states);
  assert.deepEqual(pass.ids, ['a', 'c', 'd']);
  assert.equal(PM.firstToOpen(pass), 'a');
  assert.deepEqual(PM.queue(pass, 'a', 'a'), ['c', 'd']);
  PM.markHandled(pass, 'a');
  PM.noteStates(pass, states, Object.assign({}, states, { e: 'unread', a: 'asks' }));
  assert.deepEqual(PM.passInfo(pass).arrivals, ['e']);
  assert.deepEqual(PM.queue(pass, 'c', 'c'), ['d', 'e']);
  assert.deepEqual(PM.queue(pass, 'b', 'c'), ['c', 'd', 'e']);
  PM.noteStates(pass, Object.assign({}, states, { e: 'unread' }), Object.assign({}, states, { e: 'read' }));
  assert.deepEqual(PM.passInfo(pass).arrivals, []);
});

test('came back: the pane finished a turn after your send, and you have not read it', () => {
  const sent = { completion_seq: 10 };
  assert.equal(PM.cameBack({ delivery: { acknowledged: true }, completion_seq: 11 }, sent), true);
  assert.equal(PM.cameBack({ delivery: { acknowledged: true }, completion_seq: 10 }, sent), false);
  assert.equal(PM.cameBack({ delivery: { acknowledged: true }, completion_seq: 11, viewed: true }, sent), false);
  assert.equal(PM.cameBack({ completion_seq: 11 }, sent), false);
  assert.equal(PM.cameBack({ delivery: { acknowledged: true }, completion_seq: null, status: 'idle' }, null), true);
  assert.equal(PM.cameBack({ delivery: { acknowledged: true }, completion_seq: null, status: 'working' }, null), false);
});

test('came back without a completion_seq: the transcript has an answer after your send', () => {
  const at = Date.parse('2026-10-16T05:00:00Z');
  const model = (start, state) => ({ turns: [{ start: start }], last: { state: state } });
  const it = { delivery: { acknowledged: true }, completion_seq: null, status: 'unreadable_by_gateway' };
  assert.equal(PM.cameBack(it, { at: at, completion_seq: null }, model(at + 2000, 'waiting')), false);
  assert.equal(PM.cameBack(it, { at: at, completion_seq: null }, model(at + 2000, 'answered')), true);
  assert.equal(PM.cameBack(it, { at: at, completion_seq: null }, model(at - 3600e3, 'answered')), false);
  assert.equal(PM.cameBack(Object.assign({ herdr_status: 'working' }, it), { at: at }, null), false);
  assert.equal(PM.cameBack(Object.assign({ herdr_status: 'done' }, it), { at: at }, null), true);
  assert.equal(PM.cameBack(it, { at: at }, null), false);
});

test('a pane the gateway cannot read takes herdr\'s own status', () => {
  assert.equal(PM.stateOf({ status: 'unreadable_by_gateway', herdr_status: 'working' }), 'working');
  assert.equal(PM.stateOf({ status: 'unreadable_by_gateway', herdr_status: 'done', viewed: false }), 'unread');
  assert.equal(PM.stateOf({ status: 'unreadable_by_gateway' }), 'read');
});

test('compose: your reply first, then the comments in line order through composeMessage', () => {
  const a = { line: 40, on: 'On your last answer', quote: 'one sentence', first: 'Done.', body: 'Say why.' };
  const b = { line: 12, on: 'On your Bash call `rspec` (turn 1)', quote: '', first: '', body: 'Rerun it.' };
  assert.equal(PM.compose([a], ''), '> one sentence\nOn your last answer: Say why.');
  assert.equal(PM.compose([], ' Ship it. '), 'Ship it.');
  assert.equal(PM.compose([a, b], 'Good.'),
    'Good.\n\n2 review comments:\n\n1.\nOn your Bash call `rspec` (turn 1): Rerun it.\n\n2.\n> one sentence\nOn your last answer: Say why.');
});

test('compose keeps composeMessage\'s limits: 6 quoted lines, each cut at 200 characters', () => {
  const long = Array.from({ length: 7 }, (_, i) => String(i) + 'x'.repeat(299)).join('\n');
  const text = PM.compose([{ line: 1, on: 'On your last answer', quote: long, body: 'Shorter.' }], '');
  const quoted = text.split('\n').filter((l) => l.startsWith('> '));
  assert.equal(quoted.length, 7);
  assert.equal(quoted[6], '> …');
  assert.ok(quoted.slice(0, 6).every((l) => l.length === 2 + 200 + 1 && l.endsWith('…')));
});

test('a rename turns what you typed into herdr\'s agent-name form', () => {
  const ok = /^[a-z][a-z0-9_-]{0,31}$/;
  assert.equal(PM.herdrName('Lab Pane Two!'), 'lab-pane-two');
  assert.equal(PM.herdrName('  Progress updates on ivorpad/relay #843 '), 'progress-updates-on-ivorpadrelay');
  assert.equal(PM.herdrName('42 Fix_the  BUILD'), 'fix_the-build');
  assert.equal(PM.herdrName('-- --'), '');
  assert.equal(PM.herdrName('Ñandú café'), 'nandu-cafe');
  assert.equal(PM.herdrName('Fix  the -- build !'), 'fix-the-build');
  assert.equal(PM.herdrName('a'.repeat(31) + ' b'), 'a'.repeat(31));
  for (const s of ['Lab Pane Two!', 'x'.repeat(50), 'A b', 'z9']) assert.match(PM.herdrName(s), ok, s);
});

test('receipts and titles name the machine and the pane', () => {
  const it = { machine: 'workbox3', pane_id: 'w3:p4', agent: 'codex' };
  assert.equal(PM.receiptTo(it), 'workbox3 w3:p4');
  assert.equal(PM.previewTitle(it), 'Send to workbox3 w3:p4');
  assert.match(PM.whyNoTranscript(it, { state: 'reachable' }), /^Codex transcripts are read on this Mac only/);
  assert.match(PM.whyNoTranscript(it, { state: 'unreachable', error: 'ssh timeout' }), /^workbox3 is unreachable \(ssh timeout\)/);
});

test('compose: comments from the gutter go in row then line order, each quoting its lines', () => {
  const src = Array.from({ length: 9 }, (_, i) => 'line ' + (i + 1));
  const a = { line: 40, ls: 4, le: 9, on: 'On your last answer (lines 4-9)', quote: src.slice(3, 9).join('\n'), body: 'Too long.', at: 2 };
  const b = { line: 40, ls: 1, le: 1, on: 'On your last answer (line 1)', quote: 'line 1', body: 'Good start.', at: 3 };
  const c = { line: 40, on: 'On your last answer', quote: '', first: 'line 1', body: 'Overall fine.', at: 1 };
  const d = { line: 12, ls: 2, le: 3, on: 'On your Bash call `ls` (turn 1), output lines 2-3', quote: 'b\nc', body: 'Why?', at: 4 };
  assert.equal(PM.compose([a, b, c, d], ''), [
    '4 review comments:', '',
    '1.', '> b', '> c', 'On your Bash call `ls` (turn 1), output lines 2-3: Why?', '',
    '2.', '> line 1', 'On your last answer: Overall fine.', '',
    '3.', '> line 1', 'On your last answer (line 1): Good start.', '',
    '4.', '> line 4', '> line 5', '> line 6', '> line 7', '> line 8', '> line 9', 'On your last answer (lines 4-9): Too long.',
  ].join('\n'));
});

test('compose: a comment with nothing to quote quotes nothing, even on transcript line 1', () => {
  assert.equal(PM.compose([{ line: 1, on: 'On your Bash call `ls` (turn 1)', quote: '', first: '', body: 'Why?' }], ''), 'On your Bash call `ls` (turn 1): Why?');
});
