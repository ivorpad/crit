'use strict';
// herdr-transcript-view.js draws one pane's transcript: a band per earlier
// turn, the last turn with its steps folded and its answer open, rows built
// only for what is open, and every transcript string escaped.
const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const P = require('../herdr-transcript-parse.js');
const V = require('../herdr-transcript-view.js');
const M = require('../herdr-transcript-md.js');
const R = require('../herdr-transcript-rules.js');

const flaky = fs.readFileSync(path.join(__dirname, 'fixtures', 'herdr-demo-flaky-spec.jsonl'), 'utf8');
const ps = (o) => Object.assign({ openBands: {}, foldOpen: false, openRows: {}, showAll: {}, forms: {}, drafts: {}, editing: {} }, o);
const count = (s, re) => (s.match(re) || []).length;

test('earlier turns are one band each; the last turn shows its prompt, one fold row and the answer', () => {
  const m = P.parseTranscript(flaky);
  const html = V.stream({ model: m, ps: ps(), comments: [] });
  assert.equal(count(html, /class="ht-band"/g), 3);
  assert.equal(count(html, /class="ht-fold"/g), 1);
  assert.equal(count(html, /ht-final/g), 1);
  assert.match(html, /5 tool calls<\/b>, <span class="ht-bad">1 failed<\/span>/);
});

test('rows are built only for what is open', () => {
  const m = P.parseTranscript(flaky);
  const closed = count(V.stream({ model: m, ps: ps(), comments: [] }), /class="ht-row /g);
  const open = count(V.stream({ model: m, ps: ps({ openBands: { 1: true }, foldOpen: true }), comments: [] }), /class="ht-row /g);
  assert.ok(closed < 10, 'closed: ' + closed);
  assert.ok(open > closed + 10, 'open: ' + open);
});

test('transcript text is escaped in prompts, messages, tool input and output', () => {
  const lines = [
    { type: 'user', uuid: 'u1', promptSource: 'typed', message: { role: 'user', content: 'Check <img src=x onerror=alert(1)>' } },
    { type: 'assistant', uuid: 'a1', message: { id: 'm1', role: 'assistant', content: [{ type: 'tool_use', id: 't1', name: 'Bash', input: { command: 'echo "<script>x</script>"' } }] } },
    { type: 'user', uuid: 'r1', message: { role: 'user', content: [{ type: 'tool_result', tool_use_id: 't1', content: '<script>x</script>' }] } },
    { type: 'assistant', uuid: 'a2', message: { id: 'm2', role: 'assistant', content: [{ type: 'text', text: 'Done <iframe src=x></iframe>' }] } },
  ].map((l) => JSON.stringify(l)).join('\n') + '\n';
  const m = P.parseTranscript(lines);
  const tool = m.rows.find((r) => r.kind === 'tool');
  const html = V.stream({ model: m, ps: ps({ foldOpen: true, openRows: { [tool.key]: true } }), comments: [] });
  assert.doesNotMatch(html, /<img|<script|<iframe/);
  assert.match(html, /&lt;script&gt;x&lt;\/script&gt;/);
});

test('a prompt herdr typed shows its words with a "pasted" tag, in the row and in its band', () => {
  const ln = (o) => JSON.stringify(o);
  const text = [
    ln({ type: 'user', uuid: 'u1', timestamp: '2026-10-16T05:00:00Z', promptSource: 'typed', message: { role: 'user', content: '<pasted_content id="a4b3">Review: rerun the spec.</pasted_content>' } }),
    ln({ type: 'assistant', uuid: 'a1', timestamp: '2026-10-16T05:00:05Z', message: { id: 'm1', role: 'assistant', content: [{ type: 'text', text: 'Rerun done.' }] } }),
    ln({ type: 'user', uuid: 'u2', timestamp: '2026-10-16T05:01:00Z', promptSource: 'typed', message: { role: 'user', content: 'Thanks' } }),
    ln({ type: 'assistant', uuid: 'a2', timestamp: '2026-10-16T05:01:05Z', message: { id: 'm2', role: 'assistant', content: [{ type: 'text', text: 'Ok.' }] } }),
  ].join('\n') + '\n';
  const m = P.parseTranscript(text);
  assert.equal(m.turns[0].label, 'Review: rerun the spec.');
  const closed = V.stream({ model: m, ps: ps(), comments: [] });
  assert.doesNotMatch(closed, /pasted_content/);
  assert.match(closed, /<span class="ht-tl">Review: rerun the spec\.<\/span>/);
  const open = V.stream({ model: m, ps: ps({ openBands: { 1: true } }), comments: [] });
  assert.doesNotMatch(open, /pasted_content/);
  assert.match(open, /<span class="ht-pasted"[^>]*>pasted<\/span>/);
  assert.match(open, /<div class="ht-lines ht-plain ht-ptext" data-part="prompt"><div class="ht-lb" data-ls="1" data-le="1"><button[^>]*class="ht-lplus"[^>]*><\/button><span class="ht-lc">Review: rerun the spec\.<\/span><\/div>/);
});

test('a sent comment shows its receipt; an open one offers Send now', () => {
  const m = P.parseTranscript(flaky);
  const r = m.rows[m.last.answer[0]];
  const base = { key: r.key, anchor: 'L1', on: 'On your last answer', quote: '', body: 'Ok', at: Date.parse('2026-10-16T09:05:00') };
  const sent = V.row({ model: m, ps: ps(), comments: [Object.assign({ id: 'a', resolved: true, receipt: { to: 'local wNR:pC', at: Date.parse('2026-10-16T09:07:00') } }, base)] }, r, { final: true });
  const open = V.row({ model: m, ps: ps(), comments: [Object.assign({ id: 'b', resolved: false }, base)] }, r, { final: true });
  assert.match(sent, /✓ Sent to local wNR:pC at 09:07/);
  assert.match(open, /data-act="send-one"/);
});

const ln = (o) => JSON.stringify(o);
function oneTurn(answer, tool) {
  const lines = [ln({ type: 'user', uuid: 'u1', timestamp: '2026-10-16T05:00:00Z', promptSource: 'typed', message: { role: 'user', content: 'Go' } })];
  if (tool) {
    lines.push(ln({ type: 'assistant', uuid: 'a0', timestamp: '2026-10-16T05:00:01Z', message: { id: 'm0', role: 'assistant', content: [{ type: 'tool_use', id: 't1', name: 'Bash', input: tool.input }] } }));
    lines.push(ln({ type: 'user', uuid: 'r1', timestamp: '2026-10-16T05:00:02Z', message: { role: 'user', content: [{ type: 'tool_result', tool_use_id: 't1', content: tool.output }] } }));
  }
  lines.push(ln({ type: 'assistant', uuid: 'a1', timestamp: '2026-10-16T05:00:05Z', message: { id: 'm1', role: 'assistant', content: [{ type: 'text', text: answer }] } }));
  return P.parseTranscript(lines.join('\n') + '\n');
}
const lb = (html, ls) => new RegExp('<div class="ht-lb([^"]*)" data-ls="' + ls + '"').exec(html)[1];

test('the last answer is line blocks, one gutter "+" per paragraph and list item', () => {
  const m = P.parseTranscript(flaky);
  const r = m.rows[m.last.answer[0]];
  const html = V.row({ model: m, ps: ps(), comments: [] }, r, { final: true });
  assert.equal(count(html, /class="ht-lplus"/g), M.blocks(r.text).length);
  assert.equal(count(html, /class="ht-lplus"/g), 6);
  assert.match(html, /<div class="ht-lines ht-md" data-part="text">/);
  assert.match(html, /aria-label="Comment on line 3"/);
});

test('a comment on lines sits under its last line and marks them; a whole-row comment stays at the end', () => {
  const m = oneTurn('Para one\n\n- a\n- b\n- c\n\nPara two');
  const r = m.rows[m.last.answer[0]];
  const base = { key: r.key, anchor: 'L1', at: 1, resolved: false, receipt: null };
  const html = V.row({ model: m, ps: ps(), comments: [
    Object.assign({ id: 'rng', part: 'text', ls: 3, le: 4, on: 'On your last answer (lines 3-4)', quote: '- a\n- b', body: 'Range body' }, base),
    Object.assign({ id: 'all', on: 'On your last answer', quote: '', body: 'Row body' }, base),
  ] }, r, { final: true });
  const at = (s) => html.indexOf(s);
  assert.ok(at('>b</span>') < at('Range body') && at('Range body') < at('>c</span>'), 'the card is between line 4 and line 5');
  assert.ok(at('Row body') > at('Para two'), 'the whole-row card comes after the text');
  assert.match(lb(html, 3), /ht-lcom/);
  assert.match(lb(html, 4), /ht-lcom/);
  assert.doesNotMatch(lb(html, 5), /ht-lcom/);
});

test('tool input and output: one block per line, no "+" on a blank line, and the quote is the source lines', () => {
  const m = oneTurn('Done.', { input: { command: 'cd app\nnpm test', description: 'Run <tests>' }, output: 'ok 1\n\nok <3>' });
  const t = m.rows.find((r) => r.kind === 'tool');
  assert.deepEqual(V.partLines(t, 'in'), ['$ cd app', '  npm test', 'description: Run <tests>']);
  assert.deepEqual(V.partLines(t, 'out'), ['ok 1', '', 'ok <3>']);
  assert.equal(R.sliceLines(V.partLines(t, 'out'), 1, 3), 'ok 1\n\nok <3>');
  const html = V.row({ model: m, ps: ps({ openRows: { [t.key]: true } }), comments: [] }, t);
  const out = html.slice(html.indexOf('data-part="out"'));
  assert.equal(count(out, /class="ht-lplus"/g), 2);
  assert.match(out, /<div class="ht-lb ht-lgap" data-ls="2" data-le="2"><\/div>/);
  assert.match(out, /<span class="ht-lc">ok &lt;3&gt;<\/span>/);
  assert.doesNotMatch(html, /<tests>|<3>/);
});

test('an open range form lights its lines and shows the quote as the message carries it', () => {
  const output = Array.from({ length: 9 }, (_, i) => 'line ' + (i + 1)).join('\n');
  const m = oneTurn('Done.', { input: { command: 'seq 9' }, output });
  const t = m.rows.find((r) => r.kind === 'tool');
  const fk = t.key + '|out|1-8';
  const forms = { [fk]: { key: t.key, part: 'out', ls: 1, le: 8, quote: R.sliceLines(V.partLines(t, 'out'), 1, 8), on: R.describeRange(t, m, 'out', 1, 8) } };
  const html = V.row({ model: m, ps: ps({ openRows: { [t.key]: true }, forms }), comments: [] }, t);
  const out = html.slice(html.indexOf('data-part="out"'));
  assert.match(out, new RegExp('<form class="ht-form" data-key="' + t.key + '" data-fk="' + t.key.replace(/[|]/g, '\\|') + '\\|out\\|1-8">'));
  assert.match(out, /<blockquote>line 1\nline 2\nline 3\nline 4\nline 5\nline 6<\/blockquote><div class="ht-qmore">2 more lines\. The message quotes the first 6\.<\/div>/);
  assert.ok(out.indexOf('<form') > out.indexOf('>line 8<') && out.indexOf('<form') < out.indexOf('>line 9<'), 'the form opens under line 8');
  assert.match(lb(out, 1), /ht-lsel/);
  assert.match(lb(out, 8), /ht-lsel/);
  assert.doesNotMatch(lb(out, 9), /ht-lsel/);
});

test('a folded tool call keeps the comments on its lines at its end', () => {
  const m = oneTurn('Done.', { input: { command: 'ls' }, output: 'a\nb' });
  const t = m.rows.find((r) => r.kind === 'tool');
  const c = { id: 'x', key: t.key, part: 'out', ls: 2, le: 2, anchor: 'L3', on: 'On your Bash call `ls` (turn 1), output line 2', quote: 'b', body: 'Why b?', at: 1, resolved: false };
  const closed = V.row({ model: m, ps: ps(), comments: [c] }, t);
  assert.doesNotMatch(closed, /data-part="out"/);
  assert.match(closed, /Why b\?/);
});

// ---------- pictures ----------
const images = fs.readFileSync(path.join(__dirname, 'fixtures', 'herdr-demo-images.jsonl'), 'utf8');
const pane = { id: 'local/wSJ:p2.md', store: 'local/wSJ:p2.md|demo-images' };
const src = (uuid, block) => '/api/herdr/image?path=local%2FwSJ%3Ap2.md&amp;uuid=' + uuid + '&amp;block=' + block;

test('a folded tool call that returned a picture shows it in a strip and says "1 image"', () => {
  const m = P.parseTranscript(images);
  const t = m.rows.find((r) => r.toolId === 't-read');
  const html = V.row({ model: m, ps: ps(), comments: [], pane }, t);
  assert.doesNotMatch(html, /data-part="out"/, 'still folded');
  assert.match(html, /<span class="ht-sz">1 image · 1 s<\/span>/);
  assert.ok(html.includes('<div class="ht-thumbs"><a class="ht-imglink" href="' + src('img-r1', '0.0') + '" target="_blank" rel="noopener" title="Open the full image">' +
    '<img class="ht-img" loading="lazy" decoding="async" src="' + src('img-r1', '0.0') + '" alt="image/png 1220×880" data-img="img-r1|0.0" width="1220" height="880"></a></div>'), html);
  const at = html.indexOf('class="ht-rh"');
  assert.ok(!html.slice(at, html.indexOf('</button>', at)).includes('ht-thumbs'), 'the strip is not inside the row head button');
});

test('a closed fold says how many pictures its steps returned and shows them; open, each row has its own', () => {
  const m = P.parseTranscript(images);
  const closed = V.stream({ model: m, ps: ps(), comments: [], pane });
  assert.match(closed, /<b>4 tool calls<\/b>, 2 images<\/span>/);
  const strip = /<div class="ht-thumbs ht-fthumbs">([\s\S]*?)<\/div>/.exec(closed)[1];
  assert.equal(count(strip, /<img /g), 2);
  assert.ok(strip.includes(src('img-r1', '0.0')) && strip.includes(src('img-r3', '0.1')));
  const open = V.stream({ model: m, ps: ps({ foldOpen: true }), comments: [], pane });
  assert.doesNotMatch(open, /ht-fthumbs/);
  assert.equal(count(open, /<div class="ht-thumbs">/g), 2);
});

test('a pasted picture sits under the prompt, outside its line blocks', () => {
  const m = P.parseTranscript(images);
  const html = V.stream({ model: m, ps: ps(), comments: [], pane });
  const prompt = html.slice(html.indexOf('data-part="prompt"'));
  const pics = prompt.slice(prompt.indexOf('<div class="ht-imgs">'));
  assert.ok(pics.startsWith('<div class="ht-imgs"><a class="ht-imglink" href="' + src('img-u1', '1') + '"'), pics.slice(0, 200));
  assert.doesNotMatch(pics.slice(0, pics.indexOf('</div>')), /ht-lplus|ht-lb/);
  assert.match(pics, /alt="image\/png"/, 'no size when the line reports none');
});

test('an SVG, or a line with no uuid, keeps its placeholder text and draws no picture', () => {
  const m = P.parseTranscript(images);
  ['t-svg', 't-old'].forEach((id) => {
    const t = m.rows.find((r) => r.toolId === id);
    const html = V.row({ model: m, ps: ps({ openRows: { [t.key]: true } }), comments: [], pane }, t);
    assert.doesNotMatch(html, /<img|ht-thumbs/);
    assert.match(html, /<span class="ht-lc">\[image image\/(svg\+xml|png)\]<\/span>/);
  });
});

test('a picture that failed to load once is a note on every redraw', () => {
  const m = P.parseTranscript(images);
  const t = m.rows.find((r) => r.toolId === 't-read');
  const html = V.row({ model: m, ps: ps({ badImg: { 'img-r1|0.0': true } }), comments: [], pane }, t);
  assert.doesNotMatch(html, /<img/);
  assert.match(html, /<span class="ht-imgbad">image not available<\/span>/);
});

test('an earlier turn\'s band counts its pictures', () => {
  const more = images + [
    ln({ type: 'user', uuid: 'u9', timestamp: '2026-10-16T06:05:00Z', promptSource: 'typed', message: { role: 'user', content: 'Thanks' } }),
    ln({ type: 'assistant', uuid: 'a9', timestamp: '2026-10-16T06:05:05Z', message: { id: 'm9', role: 'assistant', content: [{ type: 'text', text: 'Ok.' }] } }),
  ].join('\n') + '\n';
  const html = V.stream({ model: P.parseTranscript(more), ps: ps(), comments: [], pane });
  assert.match(html, /<span class="ht-tc">4 tool calls, 3 images<\/span>/);
});

test('a picture the agent sent is the whole row: only the row "+", and its name is encoded', () => {
  const m = P.parseTranscript([
    ln({ type: 'user', uuid: 'u1', promptSource: 'typed', message: { role: 'user', content: 'Draw it' } }),
    ln({ type: 'assistant', uuid: 'a"b<c', message: { id: 'm1', role: 'assistant', content: [{ type: 'image', source: { type: 'base64', media_type: 'image/gif', data: 'R0lG' } }] } }),
  ].join('\n') + '\n');
  const r = m.rows.find((x) => x.image);
  const html = V.row({ model: m, ps: ps(), comments: [], pane }, r, { final: true });
  assert.match(html, /class="ht-add"/);
  assert.doesNotMatch(html, /ht-lplus|ht-lines/);
  assert.ok(html.includes('src="/api/herdr/image?path=local%2FwSJ%3Ap2.md&amp;uuid=a%22b%3Cc&amp;block=0"'), html);
  assert.ok(html.includes('data-img="a&quot;b&lt;c|0"'));
  assert.doesNotMatch(html, /a"b<c/);
});
