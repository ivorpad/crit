'use strict';
// herdr-transcript-rules.js: which user lines are prompts, how a tool call is
// named, and the words a comment uses to say what it is on.
const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const R = require('../herdr-transcript-rules.js');
const P = require('../herdr-transcript-parse.js');

test('user lines nobody typed are told apart from prompts', () => {
  assert.equal(R.userLineKind('[Request interrupted by user]'), 'interrupt');
  assert.equal(R.userLineKind('[Request interrupted by user for tool use]'), 'interrupt');
  assert.equal(R.userLineKind('<task-notification><summary>done</summary></task-notification>'), 'notification');
  assert.equal(R.userLineKind('<local-command-stdout>ok</local-command-stdout>'), 'command-output');
  assert.equal(R.userLineKind('<system-reminder>x</system-reminder>'), 'command-output');
  assert.equal(R.userLineKind('Fix the flaky test'), 'prompt');
});

test('a prompt label drops envelopes, pasted-content tags and markdown marks', () => {
  assert.equal(R.stripPromptEnvelopes('<command-message>review</command-message>\n<command-name>/review</command-name>\n<command-args>42</command-args>'), 'review 42');
  assert.equal(R.plainLabel('<pasted_content id="7">\n> **Blocker:** `pnpm issues:finish` needs proof'), 'Blocker: pnpm issues:finish needs proof');
});

test('text herdr typed into the pane arrives in a pasted_content envelope: the words stay, the tags go', () => {
  const typed = '<pasted_content id="a4b3">Good. Open the PR as a draft.\n\n2 review comments:</pasted_content>';
  assert.equal(R.userLineKind(typed), 'prompt');
  assert.equal(R.isPasted(typed), true);
  assert.equal(R.unwrapPasted(typed), 'Good. Open the PR as a draft.\n\n2 review comments:');
  assert.equal(R.stripPromptEnvelopes(typed), 'Good. Open the PR as a draft. 2 review comments:');
  assert.equal(R.plainLabel(typed), 'Good. Open the PR as a draft. 2 review comments:');
  assert.equal(R.unwrapPasted('<pasted_content id="x">cut off'), 'cut off');
});

test('a tool call is named by the input that says what it did', () => {
  assert.equal(R.toolHeadline('Bash', { command: 'rg -n x\nsecond line', description: 'Find x' }), 'rg -n x');
  assert.equal(R.toolHeadline('Grep', { path: 'tests', pattern: 'sleep' }), 'sleep');
  assert.equal(R.toolHeadline('Agent', { prompt: 'A long prompt', description: 'Audit specs' }), 'Audit specs');
  assert.equal(R.toolHeadline('Edit', { file_path: '/repo/a/b.rb' }, '/repo'), 'a/b.rb');
  assert.equal(R.toolLabel('mcp__github__create_issue'), 'github/create_issue');
});

test('a comment says what it is on; without a selection it quotes the row\'s first line', () => {
  const m = P.parseTranscript(fs.readFileSync(path.join(__dirname, 'fixtures', 'herdr-demo-flaky-spec.jsonl'), 'utf8'));
  const grep = m.rows.find((r) => r.kind === 'tool' && r.name === 'Grep');
  const edit = m.rows.find((r) => r.kind === 'tool' && r.name === 'Edit');
  const answer = m.rows[m.last.answer[0]];
  const prompt = m.rows.find((r) => r.kind === 'prompt');
  assert.equal(R.describeRow(grep, m), 'On your Grep for `sleep [0-9.]+` (turn 3)');
  assert.equal(R.describeRow(edit, m), 'On your Edit of `tests/queue_spec.rb` (turn 1)');
  assert.equal(R.describeRow(answer, m), 'On your last answer');
  assert.equal(R.describeRow(prompt, m), 'On my prompt in turn 1');
  assert.equal(R.quoteFor(grep), '');
  assert.equal(R.quoteFor(answer), 'Done. Both specs freeze time now, and the suite is steady.');
});

test('a range from the gutter: a drag either way, a shift-click, its quote and its words', () => {
  assert.deepEqual(R.spanOf({ ls: 7, le: 9 }, { ls: 3, le: 3 }), { ls: 3, le: 9 }, 'drag up from a paragraph');
  assert.deepEqual(R.spanOf({ ls: 3, le: 3 }, { ls: 10, le: 12 }), { ls: 3, le: 12 }, 'drag down onto a paragraph');
  assert.deepEqual(R.spanOf({ ls: 4, le: 6 }, { ls: 5, le: 5 }), { ls: 4, le: 6 }, 'shift-click inside the range');
  assert.equal(R.sliceLines(['a', 'b', 'c', 'd'], 2, 3), 'b\nc');
  const m = P.parseTranscript(fs.readFileSync(path.join(__dirname, 'fixtures', 'herdr-demo-flaky-spec.jsonl'), 'utf8'));
  const ans = m.rows[m.last.answer[0]];
  assert.equal(R.describeRange(ans, m, 'text', 3, 6), 'On your last answer (lines 3-6)');
  assert.equal(R.describeRange(ans, m, 'text', 8, 8), 'On your last answer (line 8)');
  const bash = m.rows.find((r) => r.kind === 'tool' && r.name === 'Bash');
  assert.equal(R.describeRange(bash, m, 'out', 1, 2), 'On your Bash call `rg -n "sleep|Time.now" tests/queue_spec.rb` (turn 1), output lines 1-2');
  assert.match(R.describeRange(bash, m, 'in', 1, 1), /\(turn 1\), input line 1$/);
  const prompt = m.rows.find((r) => r.kind === 'prompt');
  assert.equal(R.describeRange(prompt, m, 'prompt', 2, 2), 'On my prompt in turn 1 (line 2)');
});
