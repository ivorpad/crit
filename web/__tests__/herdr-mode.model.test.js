'use strict';
// herdr-mode.model.js is pure: it reads the GET /api/herdr payload
// (docs/herdr-api.md) and nothing else. The real sweep fixture covers the
// shapes the step-1 collector produces (questions in prose, unverified input
// lines, reachable machines); the inline items below cover what it cannot
// yet (dialogs, exited and unreadable panes, paused boxes).
const { test } = require('node:test');
const assert = require('node:assert/strict');
const path = require('node:path');
const fs = require('node:fs');

const M = require('../herdr-mode.model.js');
const sweep = JSON.parse(fs.readFileSync(path.join(__dirname, 'fixtures', 'herdr-sweep.json'), 'utf8'));

test('fixture is the /api/herdr shape: every item has a review path and a read mark', () => {
  assert.ok(Array.isArray(sweep.machines) && sweep.machines.length > 0);
  assert.ok(Array.isArray(sweep.items) && sweep.items.length > 0);
  assert.equal(typeof sweep.interval_seconds, 'number');
  for (const it of sweep.items) {
    assert.equal(typeof it.id, 'string');
    assert.match(it.path, /^[^/]+\/[^/]+\.md$/, it.id + ' path');
    assert.equal(it.path, it.id + '.md');
    assert.equal(typeof it.machine, 'string');
    assert.equal(typeof it.pane_id, 'string');
    assert.equal(typeof it.agent, 'string');
    assert.equal(typeof it.status, 'string');
    assert.equal(typeof it.needs_you, 'boolean');
    assert.equal(typeof it.viewed, 'boolean');
  }
});

test('normalizeSnapshot tolerates junk, arrays and the real payload', () => {
  assert.deepEqual(M.normalizeSnapshot(null).items, []);
  assert.deepEqual(M.normalizeSnapshot('x').machines, []);
  assert.equal(M.normalizeSnapshot([{ path: 'a/b.md' }, 3, null]).items.length, 1);
  const s = M.normalizeSnapshot(sweep);
  assert.equal(s.items.length, sweep.items.length);
  assert.equal(s.machines.length, sweep.machines.length);
  assert.equal(s.needYou, sweep.need_you);
  assert.equal(s.intervalSeconds, 15);
});

test('index keys items by review path', () => {
  const byPath = M.index(sweep);
  assert.equal(Object.keys(byPath).length, sweep.items.length);
  assert.equal(byPath['workbox3/w3:p4.md'].pane_id, 'w3:p4');
  assert.equal(M.index(null)['x'], undefined);
});

test('needsYou is needs_you && !viewed; count and label follow it', () => {
  assert.equal(M.needsYou({ needs_you: true, viewed: false }), true);
  assert.equal(M.needsYou({ needs_you: true, viewed: true }), false);
  assert.equal(M.needsYou({ needs_you: false, viewed: false }), false);
  assert.equal(M.needsYou(null), false);
  assert.equal(M.needsYouCount(sweep).needs, sweep.need_you);
  assert.equal(M.needsYouLabel(sweep), sweep.need_you + ' of ' + sweep.items.length + ' panes need you');
  assert.equal(M.needsYouLabel({ items: [{ needs_you: true, viewed: false }] }), '1 of 1 pane needs you');
  assert.equal(M.needsYouLabel({ items: [] }), 'no panes');
  const allRead = { items: sweep.items.map((it) => Object.assign({}, it, { viewed: true })) };
  assert.equal(M.needsYouLabel(allRead), '0 of ' + sweep.items.length + ' panes need you');
});

test('machineNotices lists machines with no pane or with errors, with a parked Wake for paused boxes', () => {
  const real = M.machineNotices(sweep);
  // ovh and syno answered with zero agents in the real sweep.
  assert.deepEqual(real.map((n) => n.label), ['ovh', 'syno']);
  assert.equal(real[0].text, 'no agent panes');
  assert.equal(real[0].action, null);

  const notices = M.machineNotices({
    machines: [
      { label: 'local', state: 'reachable', agents: 1 },
      { label: 'workbox2', instabox: 'ib2', instabox_state: 'suspended', state: 'paused' },
      { label: 'workbox5', instabox: 'ib5', state: 'error', error: 'timed out after 8s' },
      { label: 'ovh', state: 'unreachable', error: 'ssh: no route' },
      { label: 'workbox3', state: 'reachable', gateway_error: 'gateway: 502', agents: 2 },
    ],
    items: [{ machine: 'local', path: 'local/%1.md' }, { machine: 'workbox3', path: 'workbox3/%1.md' }],
  });
  assert.deepEqual(notices.map((n) => [n.label, n.state]), [
    ['workbox2', 'paused'], ['workbox5', 'error'], ['ovh', 'unreachable'], ['workbox3', 'reachable'],
  ]);
  assert.deepEqual(notices[0].action, { label: 'Wake', kind: 'wake' });
  assert.equal(notices[0].box, 'ib2');
  assert.equal(notices[1].text, 'error: timed out after 8s');
  assert.equal(notices[2].text, 'unreachable: ssh: no route');
  assert.equal(notices[3].text, 'gateway: 502');
  assert.equal(notices[3].panes, 1);
});

test('badgeFor priority: paused box > exited > unreadable > blocked > question > working > idle', () => {
  const paused = { label: 'wb', state: 'paused' };
  assert.equal(M.badgeFor({ status: 'exited' }, paused).key, 'paused');
  assert.deepEqual(M.badgeFor({ status: 'blocked' }, paused).action, { label: 'Wake', kind: 'wake' });
  const exited = M.badgeFor({ status: 'exited', resume_command: 'claude --resume abc', dialog: { options: [] } }, null);
  assert.equal(exited.key, 'exited');
  assert.deepEqual(exited.action, { label: 'Resume', kind: 'resume', command: 'claude --resume abc' });
  assert.equal(M.badgeFor({ status: 'unreadable_by_gateway', attention: 'dialog' }).key, 'unreadable');
  assert.equal(M.badgeFor({ status: 'done', attention: 'dialog' }).key, 'blocked');
  assert.equal(M.badgeFor({ status: 'blocked' }).key, 'blocked');
  assert.equal(M.badgeFor({ status: 'idle', dialog_screen: ' Do you trust' }).key, 'blocked');
  assert.equal(M.badgeFor({ status: 'done', attention: 'question' }).key, 'question');
  assert.equal(M.badgeFor({ status: 'working' }).key, 'working');
  assert.equal(M.badgeFor({ status: 'background' }).key, 'background');
  assert.equal(M.badgeFor({ status: 'idle' }).label, 'idle');
  assert.equal(M.badgeFor({ status: 'done' }).label, 'done');
  assert.equal(M.badgeFor({ status: 'weird' }).key, 'unknown');
  assert.equal(M.badgeFor({}).label, 'unknown');
  // The real sweep: every item maps to a known badge and no action.
  for (const it of sweep.items) {
    const b = M.badgeFor(it, M.machineByLabel(sweep, it.machine));
    assert.ok(['question', 'working', 'idle'].includes(b.key), it.id + ' -> ' + b.key);
    assert.equal(b.action, null);
  }
});

test('headerMeta and paneTitle drop empty parts', () => {
  assert.equal(M.headerMeta({ agent: 'claude', title: 'fix tests' }), 'claude · fix tests');
  assert.equal(M.headerMeta({ agent: 'codex', name: 'n' }), 'codex · n');
  assert.equal(M.headerMeta({ agent: 'pi' }), 'pi');
  assert.equal(M.paneTitle({ machine: 'workbox3', pane_id: 'w3:p4', agent: 'claude', title: 'T' }), 'workbox3 · w3:p4 · claude · T');
  assert.deepEqual(M.paneTitleParts({ machine: 'local', pane_id: '%1', title: '  ' }), ['local', '%1']);
});

test('dialogCardModel: single choice with a free-text option', () => {
  const m = M.dialogCardModel({
    agent: 'claude',
    dialog: {
      dialog_id: 'd1', kind: 'question', text: 'Which colour?',
      options: [{ n: 1, label: 'Red', current: true }, { n: 2, label: 'Green' }, { n: 4, label: 'Type something.', free_text: true }],
      multi: false, free_text: true, go_ahead: null,
    },
  });
  assert.equal(m.author, 'claude');
  assert.equal(m.kind, 'question');
  assert.equal(m.text, 'Which colour?');
  assert.equal(m.multi, false);
  assert.equal(m.freeText, true);
  assert.equal(m.submitLabel, null);
  assert.equal(m.screen, null);
  assert.equal(m.goAhead, null);
  assert.equal(m.tooltip, M.STEP3_TOOLTIP);
  assert.deepEqual(m.options, [
    { n: 1, label: 'Red', current: true, checked: false, freeText: false },
    { n: 2, label: 'Green', current: false, checked: false, freeText: false },
    { n: 4, label: 'Type something.', current: false, checked: false, freeText: true },
  ]);
});

test('dialogCardModel: multi-select gets a Submit, go-ahead menus carry the option', () => {
  const multi = M.dialogCardModel({
    agent: 'claude',
    dialog: { dialog_id: 'd2', kind: 'question', text: 'Toppings?', options: [{ n: 1, label: 'cheese', checked: true }, { n: 2, label: 'basil' }], multi: true, free_text: false, go_ahead: null },
  });
  assert.equal(multi.multi, true);
  assert.equal(multi.submitLabel, 'Submit');
  assert.equal(multi.options[0].checked, true);
  assert.equal(multi.freeText, false);
  const trust = M.dialogCardModel({
    agent: 'codex',
    dialog: { dialog_id: 'd3', kind: 'trust', text: 'Trust and continue', options: [{ n: 1, label: 'Trust and continue' }, { n: 2, label: 'Back' }], multi: false, free_text: false, go_ahead: 1 },
  });
  assert.equal(trust.goAhead, 1);
  assert.equal(trust.kind, 'trust');
  // Options missing n or label still render something clickable later.
  const sparse = M.dialogCardModel({ dialog: { options: [{}, { n: 7 }] } });
  assert.deepEqual(sparse.options.map((o) => [o.n, o.label]), [[1, '1'], [7, '7']]);
  assert.equal(sparse.author, 'agent');
});

test('dialogCardModel: unparsed screen and no dialog', () => {
  const screen = M.dialogCardModel({ agent: 'claude', dialog_screen: ' Do you trust the files?\n ❯ No, exit' });
  assert.equal(screen.kind, 'unparsed');
  assert.equal(screen.text, '');
  assert.deepEqual(screen.options, []);
  assert.match(screen.screen, /No, exit/);
  assert.equal(M.dialogCardModel({ agent: 'claude', dialog_screen: '   ' }), null);
  assert.equal(M.dialogCardModel({ agent: 'claude' }), null);
  assert.equal(M.dialogCardModel(null), null);
  // A parsed dialog wins over a screen.
  const both = M.dialogCardModel({ dialog: { kind: 'permission', text: 'Run?', options: [] }, dialog_screen: 'x' });
  assert.equal(both.kind, 'permission');
  assert.equal(both.screen, null);
});

test('classifyGhost: true is a suggestion, false typed, null unverified, empty none', () => {
  assert.deepEqual(M.classifyGhost({ text: 'commit it', ghost: true }), { state: 'ghost', label: 'suggested by agent', text: 'commit it' });
  assert.deepEqual(M.classifyGhost({ text: 'ls', ghost: false }), { state: 'typed', label: 'typed, not sent', text: 'ls' });
  assert.deepEqual(M.classifyGhost({ text: '1a, 2b and 3a — go ahead', ghost: null }), { state: 'unverified', label: 'unverified', text: '1a, 2b and 3a — go ahead' });
  assert.deepEqual(M.classifyGhost({ text: '  ', ghost: true }), { state: 'none', label: '', text: '' });
  assert.deepEqual(M.classifyGhost(null), { state: 'none', label: '', text: '' });
  assert.deepEqual(M.classifyGhost(undefined).state, 'none');
  // The deployed gateway strips ANSI, so every real input line is unverified.
  const lines = sweep.items.filter((it) => it.input_line).map((it) => M.classifyGhost(it.input_line).state);
  assert.ok(lines.length > 0);
  assert.ok(lines.every((s) => s === 'unverified'));
});

test('planInfo names the plan file and the default save path', () => {
  const p = M.planInfo({ title: 'Practice redesign', plan: { path: '~/.claude/plans/practice-redesign.md', text: '# Plan\n' } });
  assert.equal(p.title, 'plan: Practice redesign');
  assert.equal(p.slug, 'practice-redesign');
  assert.equal(p.hasText, true);
  assert.equal(p.error, '');
  assert.equal(p.defaultSavePath, 'docs/plans/practice-redesign.md');
  const remote = M.planInfo({ plan: { path: '~/.claude/plans/x.md', error: 'path_not_allowed' } });
  assert.equal(remote.hasText, false);
  assert.equal(remote.error, 'path_not_allowed');
  assert.equal(remote.title, 'plan: x');
  assert.equal(M.planInfo({ plan: null }), null);
  assert.equal(M.planInfo({}), null);
});

test('focusRequest targets POST /api/herdr/focus with the review path', () => {
  assert.deepEqual(M.focusRequest({ path: 'workbox3/w3:p4.md' }), { url: '/api/herdr/focus', body: { path: 'workbox3/w3:p4.md' } });
  assert.equal(M.focusRequest({ id: 'a/b' }), null);
  assert.equal(M.focusRequest(null), null);
  for (const it of sweep.items) assert.equal(M.focusRequest(it).body.path, it.path);
});

test('paneExtrasModel is null for a quiet pane and populated otherwise', () => {
  assert.equal(M.paneExtrasModel({ status: 'working', path: 'a/b.md' }, null), null);
  assert.equal(M.paneExtrasModel(null), null);
  const ghost = M.paneExtrasModel({ path: 'a/b.md', status: 'idle', input_line: { text: 'x', ghost: true } }, null);
  assert.equal(ghost.ghost.state, 'ghost');
  assert.equal(ghost.dialog, null);
  const exited = M.paneExtrasModel({ path: 'a/c.md', status: 'exited', resume_command: 'codex resume t1', notes: ['n1', 3] }, null);
  assert.equal(exited.resumeCommand, 'codex resume t1');
  assert.deepEqual(exited.notes, ['n1']);
  assert.equal(exited.badge.key, 'exited');
  const paused = M.paneExtrasModel({ path: 'a/d.md', status: 'idle' }, { state: 'paused' });
  assert.equal(paused.badge.action.kind, 'wake');
  assert.equal(paused.tooltip, M.STEP3_TOOLTIP);
});

test('canSend needs a review path and a numeric state_change_seq; exited and background panes cannot take text', () => {
  assert.equal(M.canSend({ path: 'local/wA:p1.md', status: 'idle', state_change_seq: 7 }), true);
  assert.equal(M.canSend({ path: 'local/wA:p1.md', status: 'working', state_change_seq: 0 }), true);
  assert.equal(M.canSend({ path: 'local/wA:p1.md', status: 'idle', state_change_seq: null }), false);
  assert.equal(M.canSend({ path: 'local/wA:p1.md', status: 'exited', state_change_seq: 7 }), false);
  assert.equal(M.canSend({ path: 'local/wA:p1.md', status: 'background', state_change_seq: 7 }), false);
  assert.equal(M.canSend({ status: 'idle', state_change_seq: 7 }), false);
  assert.equal(M.canSend(null), false);
});

test('composeMessage: one comment is its quote and body; several are numbered in line order with owner replies', () => {
  const content = ['# Title', 'line two', 'line three', 'x'.repeat(250)].join('\n');
  assert.equal(M.composeMessage([], content), '');
  assert.equal(
    M.composeMessage([{ start_line: 2, end_line: 3, body: ' fix this ' }], content),
    '> line two\n> line three\nfix this');
  assert.equal(M.composeMessage([{ scope: 'file', body: '- a list reply' }], content), '- a list reply');
  assert.equal(M.composeMessage([{ start_line: 1, end_line: 1, quote: 'Tit', body: 'b' }], content), '> Tit\nb');
  const two = M.composeMessage([
    { start_line: 4, end_line: 4, body: 'second', replies: [{ author: 'crit', body: 'Sent to local wA:p1 at 10:00' }, { author: 'ivor', body: 'more' }] },
    { start_line: 1, end_line: 1, body: 'first' },
  ], content);
  assert.equal(two, '2 review comments:\n\n1.\n> # Title\nfirst\n\n2.\n> ' + 'x'.repeat(200) + '…\nsecond\nmore');
  const long = M.composeMessage([{ start_line: 1, end_line: 8, body: 'b' }], 'a\nb\nc\nd\ne\nf\ng\nh');
  assert.equal(long, '> a\n> b\n> c\n> d\n> e\n> f\n> …\nb');
});

test('sendErrorMessage names what to do for each refusal', () => {
  assert.match(M.sendErrorMessage(409, { error: 'stale' }), /changed since the last sweep/);
  assert.match(M.sendErrorMessage(409, { error: 'blocked' }), /dialog is up/);
  assert.match(M.sendErrorMessage(404, { error: 'x' }), /gone/);
  assert.equal(M.sendErrorMessage(502, { error: 'herdr: boom' }), 'Send failed: herdr: boom');
  assert.equal(M.sendErrorMessage(500, null), 'Send failed: HTTP 500');
});
