'use strict';
// herdr-transcript-md.js renders agent text from other machines: everything
// is escaped, links keep http(s), images never render.
const { test } = require('node:test');
const assert = require('node:assert/strict');
const { md, esc, blocks } = require('../herdr-transcript-md.js');

test('agent text never reaches the page as HTML', () => {
  const out = md('<script>alert(1)</script> <img src=x onerror=alert(1)> [x](javascript:alert(1)) ![pic](https://x.test/p.png)\n\n| a | b |\n|---|---|\n| <b>c</b> | d |');
  assert.doesNotMatch(out, /<script|<img|<b>|href="javascript/);
  assert.match(out, /&lt;script&gt;/);
});

test('links keep http(s) only and open safely; a quote cannot leave the attribute', () => {
  const out = md('[ok](https://a.test/p?q=1&r=2) and https://b.test/x. and [bad](https://c.test/" onmouseover="x)');
  assert.match(out, /<a href="https:\/\/a\.test\/p\?q=1&amp;r=2" target="_blank" rel="noopener noreferrer">ok<\/a>/);
  assert.match(out, /href="https:\/\/b\.test\/x"/);
  assert.doesNotMatch(out, /onmouseover="/);
});

test('lists, task boxes, tables, fences, quotes and inline code render', () => {
  const out = md('## Title\n\n- one\n- [x] done\n  - nested `code`\n\n1. first\n2. second\n\n| a | b |\n|---|:-:|\n| 1 | 2 |\n\n```js\nconst a = "<b>";\n```\n> quoted');
  assert.match(out, /<h4>Title<\/h4>/);
  assert.match(out, /<li class="ht-task"><span class="ht-box" aria-hidden="true">☑<\/span> done/);
  assert.match(out, /<ol><li>first<\/li><li>second<\/li><\/ol>/);
  assert.match(out, /<th class="ht-nw" style="text-align:center">b<\/th>/);
  assert.match(out, /<pre class="ht-code" data-lang="js"><code>const a = &quot;&lt;b&gt;&quot;;<\/code><\/pre>/);
  assert.match(out, /<blockquote><p>quoted<\/p><\/blockquote>/);
});

test('bold can wrap a code span, and code is never formatted', () => {
  assert.equal(md('**How `--browse` works:** it `a*b*c`'), '<p><strong>How <code>--browse</code> works:</strong> it <code>a*b*c</code></p>');
  assert.equal(esc('<"\'&>'), '&lt;&quot;&#39;&amp;&gt;');
});

test('blocks split a message like crit\'s document view, each with its source lines', () => {
  const src = [
    '## Plan', '', 'First line', 'second line', '',
    '1. one', '1. two', '   - nested', '',
    '```js', 'const a = 1;', '', 'run(a);', '```',
    '| a | b |', '|---|---|', '| 1 | 2 |', '',
    '> q one', '> q two',
  ].join('\n');
  const b = blocks(src);
  assert.deepEqual(b.map((x) => [x.kind, x.ls, x.le]), [
    ['h', 1, 1], ['p', 3, 4], ['li', 6, 6], ['li', 7, 7], ['li', 8, 8],
    ['code', 11, 11], ['code', 12, 12], ['code', 13, 13],
    ['trow', 15, 15], ['trow', 17, 17], ['quote', 19, 19], ['quote', 20, 20],
  ]);
  assert.equal(b[4].depth, 1);
  assert.match(b[2].html, /data-m="1\."/);
  assert.match(b[3].html, /data-m="2\."/, 'an ordered list counts on, as markdown does');
  assert.equal(new Set(b.slice(5, 8).map((x) => x.group)).size, 1, 'code lines share one box');
  assert.equal(b[8].html, '<th class="ht-nw">a</th><th class="ht-nw">b</th>');
  assert.equal(b[9].html, '<td class="ht-nw">1</td><td class="ht-nw">2</td>');
  assert.equal(b[1].html, '<p>First line<br>second line</p>');
});

test('blocks escape agent text the same way md() does', () => {
  const all = blocks('<script>x</script>\n\n```\n<img src=x onerror=y>\n```\n\n| <b>a</b> |\n|---|\n| [x](javascript:alert(1)) |\n\n> <iframe src=x>\n\n- [ ] <svg onload=x>')
    .map((x) => x.html).join('');
  assert.doesNotMatch(all, /<script|<img|<b>|<iframe|<svg|href="javascript/);
  assert.match(all, /&lt;script&gt;x&lt;\/script&gt;/);
  assert.match(all, /data-m="☐"/);
});
