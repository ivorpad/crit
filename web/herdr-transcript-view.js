// herdr-transcript-view.js: HTML for one pane's transcript on the /herdr page:
// a band per earlier turn, the last turn (your prompt, its steps folded into
// one row, the final answer open), the rows inside, and the comment cards
// under them. A row's text (a message, your prompt, a tool call's input and
// output) is drawn as line blocks with a gutter "+" each, the way crit's
// document view draws a file: a message splits into paragraphs, list items,
// code lines, table rows and quote lines, plain text into lines. A comment
// on a range of them sits under its last line.
// Everything from a transcript or a comment goes through esc() or the
// escaped markdown renderer; nothing raw reaches HTML. A picture (pasted
// into a prompt, or returned by a tool call) is an <img> on GET
// /api/herdr/image, named by its line's uuid and block; the transcript's own
// base64 is cut and never used.
// A ctx is { model, ps (fold state), comments, pane }.
// Mounted on window.crit.herdrTranscriptView.
// Dependencies: window.crit.herdrTranscriptMd (require() in Node).
(function () {
  'use strict';
  var M = typeof module === 'object' && module.exports ? require('./herdr-transcript-md.js') : window.crit.herdrTranscriptMd;
  var esc = M.esc;

  // ---------- small formats ----------
  function pad(n) { return (n < 10 ? '0' : '') + n; }
  function hhmm(t) { var d = new Date(t); return pad(d.getHours()) + ':' + pad(d.getMinutes()); }
  var DAYS = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'];
  function dayTime(t) {
    if (t === null || t === undefined) return '';
    var d = new Date(t), now = new Date();
    return (d.toDateString() === now.toDateString() ? '' : DAYS[d.getDay()] + ' ') + hhmm(t);
  }
  function dur(ms) {
    if (ms === null || ms === undefined || ms < 0) return '';
    if (ms < 1000) return (ms / 1000).toFixed(1) + ' s';
    var s = Math.round(ms / 1000);
    if (s < 60) return s + ' s';
    var m = Math.floor(s / 60);
    if (m < 60) return m + 'm ' + pad(s % 60) + 's';
    return Math.floor(m / 60) + 'h ' + pad(m % 60) + 'm';
  }
  function size(n) { return n < 1000 ? n + ' B' : n < 1e6 ? (n / 1000).toFixed(n < 10000 ? 1 : 0) + ' KB' : (n / 1e6).toFixed(1) + ' MB'; }
  function plural(n, one, many) { return n + ' ' + (n === 1 ? one : (many || one + 's')); }
  function lines(s) { return s ? String(s).split('\n').length : 0; }
  function anchor(r) { return 'L' + r.line + (r.uuid ? ' · ' + r.uuid.slice(0, 8) : ''); }
  // backticks in "On your Bash call `x`" become code, the rest stays text
  function onHtml(s) { return esc(s).replace(/`([^`]*)`/g, '<code>$1</code>'); }
  function isObj(v) { return v !== null && typeof v === 'object' && !Array.isArray(v); }

  // Long text is cut to a few lines with "Show all"; ps.showAll remembers.
  function clipped(ctx, id, text, maxLines, maxChars) {
    text = String(text || '');
    var all = text.split('\n');
    if (ctx.ps.showAll[id] || (all.length <= maxLines && text.length <= maxChars)) return { text: text, more: '' };
    var cut = all.slice(0, maxLines).join('\n');
    if (cut.length > maxChars) cut = cut.slice(0, maxChars);
    return { text: cut + '\n…', more: moreBtn(id, all.length, text.length) };
  }
  function moreBtn(id, nLines, nChars) {
    return '<button type="button" class="ht-more" data-act="all" data-id="' + esc(id) + '">Show all ' + plural(nLines, 'line') + ' (' + size(nChars) + ')</button>';
  }

  // ---------- pictures: GET /api/herdr/image names the line and the block ----------
  function imagesOf(r) { return r.images || []; }
  function imageCount(ctx, idxs) { return idxs.reduce(function (n, i) { return n + imagesOf(ctx.model.rows[i]).length; }, 0); }
  function imageId(d) { return d.uuid + '|' + d.block; }
  function imageSrc(ctx, d) {
    return '/api/herdr/image?path=' + encodeURIComponent(ctx.pane && ctx.pane.id ? ctx.pane.id : '') +
      '&uuid=' + encodeURIComponent(d.uuid) + '&block=' + encodeURIComponent(d.block);
  }
  // one picture inside a link to the full image. A picture that failed to
  // load once (ps.badImg, set by herdr-transcript-ctl.js) is a note from then
  // on, so a redraw never asks for it again.
  function picture(ctx, d) {
    if (ctx.ps.badImg && ctx.ps.badImg[imageId(d)]) return '<span class="ht-imgbad">image not available</span>';
    var src = esc(imageSrc(ctx, d)), alt = d.media + (d.w && d.h ? ' ' + d.w + '×' + d.h : '');
    return '<a class="ht-imglink" href="' + src + '" target="_blank" rel="noopener" title="Open the full image">' +
      '<img class="ht-img" loading="lazy" decoding="async" src="' + src + '" alt="' + esc(alt) + '" data-img="' + esc(imageId(d)) + '"' +
      (d.w && d.h ? ' width="' + d.w + '" height="' + d.h + '"' : '') + '></a>';
  }
  // cls: ht-imgs (as wide as the band) or ht-thumbs (a strip about 160 px tall)
  function pictures(ctx, list, cls) {
    return list.length ? '<div class="' + cls + '">' + list.map(function (d) { return picture(ctx, d); }).join('') + '</div>' : '';
  }
  // the pictures inside a closed fold, newest last; the strip keeps the last 8
  var FOLD_THUMBS = 8;
  function foldPictures(ctx, idxs) {
    var all = [];
    idxs.forEach(function (i) { all = all.concat(imagesOf(ctx.model.rows[i])); });
    if (!all.length) return '';
    var shown = all.slice(-FOLD_THUMBS), more = all.length - shown.length;
    return '<div class="ht-thumbs ht-fthumbs">' + (more ? '<span class="ht-imgmore">' + plural(more, 'earlier image') + '</span>' : '') +
      shown.map(function (d) { return picture(ctx, d); }).join('') + '</div>';
  }
  // a tool result that is only image placeholders has no text worth sizing
  function onlyImages(r) { return imagesOf(r).length > 0 && !String(r.result || '').replace(/\[image[^\]\n]*\]/g, '').trim(); }

  // ---------- rows ----------
  var MARK_WHO = {
    interrupt: 'Stopped', task: 'Task', command: 'Command', meta: 'Context', reminder: 'Reminder',
    compact: 'Compacted', away: 'Recap', info: 'Notice', queued: 'You, queued', hook: 'Hook', error: 'Error',
    result: 'Result', peer: 'Session', auto: 'Auto', system: 'System',
  };
  var STATUS_WORD = { ok: 'ok', error: 'failed', denied: 'denied', interrupted: 'stopped', pending: 'no result', encrypted: 'encrypted', nooutput: 'output not kept' };
  var KIND_WORD = { prompt: 'prompt', message: 'message', tool: 'tool call', thinking: 'thinking', mark: 'note' };

  // what you typed, as you typed it: reminder blocks gone, a pasted_content
  // envelope (how herdr's typed text arrives) unwrapped
  function promptText(r) {
    if (/<command-(name|message|args)>/.test(r.text)) return r.label;
    return r.text.replace(/<system-reminder>[\s\S]*?<\/system-reminder>/g, '').replace(/<\/?pasted_content\b[^>]*>/g, '').trim() || r.label;
  }
  function toggleHead(open, inner) {
    return '<button type="button" class="ht-rh" data-act="toggle" aria-expanded="' + open + '"><span class="ht-chev" aria-hidden="true">▸</span>' + inner + '</button>';
  }

  function row(ctx, r, opts) {
    opts = opts || {};
    var open = !!ctx.ps.openRows[r.key];
    var cls = 'ht-row ht-r-' + r.kind + (r.kind === 'mark' ? ' ht-m-' + r.tag : '') + (r.kind === 'tool' ? ' ht-s-' + r.status : '') + (opts.final ? ' ht-final' : '');
    var h = '<div class="' + cls + '" data-key="' + esc(r.key) + '" data-i="' + r.i + '">' +
      '<button type="button" class="ht-add" data-act="add" aria-label="Comment on this ' + KIND_WORD[r.kind] + '" title="Comment on this ' + KIND_WORD[r.kind] + '">+</button>';
    var tag = '<span class="ht-atag" title="Line in the transcript, and its uuid">' + esc(anchor(r)) + '</span>';
    var when = r.t !== null ? '<span class="ht-when">' + dayTime(r.t) + '</span>' : '';
    if (r.kind === 'prompt') {
      h += '<div class="ht-rh"><span class="ht-who">You</span>' + when + (r.pasted ? '<span class="ht-pasted" title="Typed into the pane by herdr, or pasted">pasted</span>' : '') + tag + '</div>' +
        '<div class="ht-rb">' + plainLines(ctx, r, 'prompt', partLines(r, 'prompt'), 18, 1600, 'ht-ptext') + pictures(ctx, imagesOf(r), 'ht-imgs') + '</div>';
    } else if (r.kind === 'message') {
      // a picture the agent sent is drawn, with only the row "+" to comment on it
      h += '<div class="ht-rh"><span class="ht-who">' + esc(ctx.model.info.agent || 'Claude') + '</span>' + when + tag + '</div><div class="ht-rb">' +
        (r.image ? pictures(ctx, [r.image], 'ht-imgs') : mdLines(ctx, r, 'text', r.text)) + '</div>';
    } else if (r.kind === 'tool') {
      var nImg = imagesOf(r).length;
      var sz = r.result !== null && r.status !== 'encrypted' && !onlyImages(r) ? plural(lines(r.result), 'line') + ' · ' + size(r.result.length) : '';
      var took = r.tEnd && r.t !== null ? dur(r.tEnd - r.t) : '';
      h += toggleHead(open, '<span class="ht-tname">' + esc(r.label) + '</span><span class="ht-thead">' + esc(r.headline || '') + '</span>' +
        '<span class="ht-st ht-st-' + r.status + '">' + STATUS_WORD[r.status] + (r.status === 'denied' && r.denial ? ' (' + esc(r.denial.replace(/-/g, ' ')) + ')' : '') + '</span>' +
        '<span class="ht-sz">' + [sz, nImg ? plural(nImg, 'image') : '', took].filter(Boolean).join(' · ') + '</span>' + tag);
      // the pictures a call returned show folded too: a screenshot is the point of the call
      h += pictures(ctx, imagesOf(r), 'ht-thumbs');
      if (open) h += '<div class="ht-rb">' + toolBody(ctx, r) + '</div>';
    } else if (r.kind === 'thinking') {
      if (r.stored) {
        h += toggleHead(open, '<span class="ht-who">Thinking</span><span class="ht-tt1">' + esc(r.text.split('\n')[0]) + '</span><span class="ht-sz">' +
          [plural(r.text.length, 'char'), r.ms ? 'thought ' + dur(r.ms) : ''].filter(Boolean).join(' · ') + '</span>' + tag);
        if (open) { var th = clipped(ctx, r.key + ':t', r.text, 40, 6000); h += '<div class="ht-rb"><div class="ht-ftext">' + esc(th.text) + '</div>' + th.more + '</div>'; }
      } else {
        h += '<div class="ht-rh"><span class="ht-who">Thinking</span><span class="ht-tt1">' + (r.redacted ? 'redacted, ' : '') + 'text not stored</span><span class="ht-sz">' + (r.ms ? 'thought ' + dur(r.ms) : '') + '</span>' + tag + '</div>';
      }
    } else {
      var who = MARK_WHO[r.tag] || 'Note', strip = pictures(ctx, imagesOf(r), 'ht-thumbs');
      if (r.full && (r.full.length > r.text.length + 2 || /\n/.test(r.full))) {
        h += toggleHead(open, '<span class="ht-who">' + who + '</span><span class="ht-tt1">' + esc(r.text) + '</span>' + when + tag) + strip;
        if (open) { var mt = clipped(ctx, r.key + ':m', r.full, 30, 5000); h += '<div class="ht-rb"><div class="ht-ftext">' + esc(mt.text) + '</div>' + mt.more + '</div>'; }
      } else {
        h += '<div class="ht-rh"><span class="ht-who">' + who + '</span><span class="ht-tt1">' + esc(r.text) + '</span>' + when + tag + '</div>' + strip;
      }
    }
    // a folded tool call still shows the comments on its lines, at its end
    return h + comments(ctx, r, r.kind === 'tool' && !open) + '</div>';
  }

  function toolBody(ctx, r) {
    var h = '<div class="ht-io"><div class="ht-io-h">Input</div>' + plainLines(ctx, r, 'in', inputLines(r), 60, 8000, 'ht-mono') + '</div>';
    // r.why: why a transcript has no output for the call (cursor-agent keeps none)
    if (r.result === null) return h + '<div class="ht-io"><div class="ht-io-h">Output</div><div class="ht-kv">' + esc(r.why || 'No result in the transcript yet. The call is still running, or the session stopped.') + '</div></div>';
    var bad = r.status === 'error' || r.status === 'interrupted' || r.status === 'denied';
    return h + '<div class="ht-io"><div class="ht-io-h">Output<span class="ht-st ht-st-' + r.status + '">' + STATUS_WORD[r.status] + '</span><span class="ht-sz">L' + r.resultLine + '</span></div>' +
      plainLines(ctx, r, 'out', partLines(r, 'out'), 40, 6000, 'ht-mono' + (bad ? ' ht-err' : '')) + '</div>';
  }

  // ---------- the text a gutter "+" works on, one entry per source line ----------
  function srcLines(s) { return String(s === null || s === undefined ? '' : s).replace(/\r\n?/g, '\n').split('\n'); }
  // a tool call's input as it is shown: a Bash command after "$ ", an edit as
  // "- old" and "+ new" lines under its path, a write under its path, any
  // other input as "key: value" lines
  function inputLines(r) {
    var inp = r.input, out = [];
    if (!isObj(inp)) return srcLines(inp);
    function push(list, first, rest) { list.forEach(function (ln, j) { out.push((j ? rest : first) + ln); }); }
    function kv(skip) {
      Object.keys(inp).forEach(function (k) {
        if (skip.indexOf(k) >= 0) return;
        var v = inp[k], s = typeof v === 'string' ? v : JSON.stringify(v, null, 1);
        if (s === undefined || s === '') return;
        push(srcLines(s), k + ': ', '  ');
      });
    }
    function diff(a, b) { push(srcLines(a), '- ', '- '); push(srcLines(b), '+ ', '+ '); }
    if (r.name === 'Bash' && typeof inp.command === 'string') { push(srcLines(inp.command), '$ ', '  '); kv(['command']); }
    else if (r.name === 'Edit' && typeof inp.file_path === 'string') { out.push(inp.file_path + (inp.replace_all ? ' (every match)' : '')); diff(inp.old_string, inp.new_string); }
    else if (r.name === 'MultiEdit' && Array.isArray(inp.edits)) { out.push(String(inp.file_path || '')); inp.edits.forEach(function (e) { diff(e && e.old_string, e && e.new_string); }); }
    else if (r.name === 'Write' && typeof inp.content === 'string') { out.push(String(inp.file_path || '')); push(srcLines(inp.content), '', ''); }
    else kv([]);
    return out.length ? out : ['(no input)'];
  }
  // partLines: the source lines of one text in a row. Line numbers in a
  // range comment count from 1 within it, and its quote is a slice of these.
  function partLines(r, part) {
    if (part === 'prompt') return srcLines(promptText(r));
    if (part === 'in') return inputLines(r);
    if (part === 'out') return srcLines(r.result);
    return srcLines(r.text);
  }

  // ---------- line blocks ----------
  // the ranges of this text's open forms light up; open comments mark theirs
  function lineState(ctx, r, part) {
    var sel = [], com = [];
    Object.keys(ctx.ps.forms).forEach(function (fk) { var f = ctx.ps.forms[fk]; if (f.key === r.key && f.part === part) sel.push(f); });
    ctx.comments.forEach(function (c) { if (c.key === r.key && c.part === part && !c.resolved) com.push(c); });
    return { sel: sel, com: com };
  }
  function inAny(list, b) {
    for (var i = 0; i < list.length; i++) if (b.ls >= list[i].ls && b.le <= list[i].le) return true;
    return false;
  }
  function lbOpen(tag, b, st, extra) {
    return '<' + tag + ' class="ht-lb' + (extra ? ' ' + extra : '') + (inAny(st.sel, b) ? ' ht-lsel' : '') + (inAny(st.com, b) ? ' ht-lcom' : '') +
      '" data-ls="' + b.ls + '" data-le="' + b.le + '">';
  }
  // the "+" is drawn by CSS, so a text selection never picks it up
  function plus(b) {
    var what = b.ls === b.le ? 'line ' + b.ls : 'lines ' + b.ls + '-' + b.le;
    return '<button type="button" class="ht-lplus" tabindex="-1" aria-label="Comment on ' + what + '" title="Comment on ' + what + '. Drag for more lines, shift-click to extend."></button>';
  }
  // placed: the cards and open forms of this text's ranges by block index,
  // each after the last block that starts at or before its last line
  function placed(ctx, r, part, blocks) {
    var at = {};
    function put(le, html) {
      var k = -1;
      for (var i = 0; i < blocks.length && blocks[i].ls <= le; i++) k = i;
      if (k < 0) k = blocks.length;
      at[k] = (at[k] || '') + html;
    }
    ctx.comments.forEach(function (c) { if (c.key === r.key && c.part === part) put(c.le, card(ctx, c)); });
    Object.keys(ctx.ps.forms).forEach(function (fk) { var f = ctx.ps.forms[fk]; if (f.key === r.key && f.part === part) put(f.le, formHTML(ctx, fk, f)); });
    return at;
  }
  function cardsAt(html) { return html ? '<div class="ht-cards ht-incards">' + html + '</div>' : ''; }

  // what fits before "Show all": maxLines lines and about maxChars characters
  function cap(list, maxLines, maxChars) {
    var out = [], used = 0;
    for (var k = 0; k < list.length && k < maxLines; k++) {
      if (used + list[k].length > maxChars) { out.push(list[k].slice(0, Math.max(0, maxChars - used)) + '…'); return { lines: out, cut: true }; }
      out.push(list[k]);
      used += list[k].length + 1;
    }
    return { lines: out, cut: out.length < list.length };
  }
  // plain text (your prompt, a tool call's input or output): one block per line
  function plainLines(ctx, r, part, list, maxLines, maxChars, cls) {
    var id = r.key + ':' + part;
    var shown = ctx.ps.showAll[id] ? { lines: list, cut: false } : cap(list, maxLines, maxChars);
    var st = lineState(ctx, r, part), diffy = part === 'in' && (r.name === 'Edit' || r.name === 'MultiEdit');
    var blocks = shown.lines.map(function (_, k) { return { ls: k + 1, le: k + 1 }; });
    var at = placed(ctx, r, part, blocks);
    var h = '<div class="ht-lines ht-plain' + (cls ? ' ' + cls : '') + '" data-part="' + part + '">';
    shown.lines.forEach(function (text, k) {
      var b = blocks[k];
      if (!text.trim()) h += lbOpen('div', b, st, 'ht-lgap') + '</div>';
      else {
        var lc = diffy && k > 0 ? (/^- /.test(text) ? 'ht-del' : /^\+ /.test(text) ? 'ht-ins' : '') : '';
        h += lbOpen('div', b, st, lc) + plus(b) + '<span class="ht-lc">' + esc(text) + '</span></div>';
      }
      h += cardsAt(at[k]);
    });
    h += cardsAt(at[blocks.length]) + '</div>';
    if (shown.cut) h += moreBtn(id, list.length, list.join('\n').length);
    return h;
  }
  // markdown (a message), split like crit's document view. A code block, a
  // quote and a table keep their box; each line or row inside is a block.
  // A picture paragraph or a preview frame takes no gutter "+": the row's
  // "+" comments on it.
  var BARE = { pic: 1, preview: 1 };
  function mdLines(ctx, r, part, text) {
    var bl = M.blocks(text, { path: ctx.pane && ctx.pane.id, bad: ctx.ps.badImg }), st = lineState(ctx, r, part), at = placed(ctx, r, part, bl), i = 0;
    var h = '<div class="ht-lines ht-md" data-part="' + part + '">';
    while (i < bl.length) {
      var b = bl[i];
      if (!b.group) {
        h += lbOpen('div', b, st, 'ht-k-' + b.kind + (b.depth ? ' ht-d' + b.depth : '')) + (BARE[b.kind] ? '' : plus(b)) + '<div class="ht-lc">' + b.html + '</div></div>' + cardsAt(at[i]);
        i++;
        continue;
      }
      var g = b.group, inner = '';
      for (; i < bl.length && bl[i].group === g; i++) {
        var x = bl[i];
        if (x.kind === 'trow') {
          inner += lbOpen('tr', x, st) + x.html.replace(/^<t[hd][^>]*>/, function (m) { return m + plus(x); }) + '</tr>' +
            (at[i] ? '<tr class="ht-lunder"><td colspan="' + x.cols + '">' + cardsAt(at[i]) + '</td></tr>' : '');
        } else inner += lbOpen('div', x, st) + plus(x) + '<div class="ht-lc">' + x.html + '</div></div>' + cardsAt(at[i]);
      }
      h += b.kind === 'trow' ? '<div class="ht-table"><table>' + inner + '</table></div>' : '<div class="' + (b.kind === 'code' ? 'ht-codeblock' : 'ht-bq') + '">' + inner + '</div>';
    }
    return h + cardsAt(at[bl.length]) + '</div>';
  }

  // ---------- comments ----------
  // the quote as the message carries it (herdrModel.composeMessage): 6 lines,
  // each cut at 200 characters
  var QUOTE_LINES = 6, QUOTE_CHARS = 200;
  function quoteHTML(q) {
    if (!q) return '';
    var all = String(q).split('\n');
    var shown = all.slice(0, QUOTE_LINES).map(function (l) { return l.length > QUOTE_CHARS ? l.slice(0, QUOTE_CHARS) + '…' : l; });
    return '<blockquote>' + esc(shown.join('\n')) + '</blockquote>' +
      (all.length > QUOTE_LINES ? '<div class="ht-qmore">' + plural(all.length - QUOTE_LINES, 'more line') + '. The message quotes the first ' + QUOTE_LINES + '.</div>' : '');
  }
  function formHTML(ctx, fk, f) {
    return '<form class="ht-form" data-key="' + esc(f.key) + '" data-fk="' + esc(fk) + '"><div class="ht-on">' + onHtml(f.on) + '</div>' + quoteHTML(f.quote) +
      '<textarea aria-label="Your comment" placeholder="What should the agent change or check?">' + esc(ctx.ps.drafts[fk] || '') + '</textarea>' +
      '<div class="ht-cact"><button type="submit" class="btn btn-sm btn-primary">Add to review</button>' +
      '<button type="button" class="btn btn-sm" data-act="form-send">Send now</button>' +
      '<button type="button" class="btn btn-sm ht-ghost" data-act="form-cancel">Cancel</button>' +
      '<span class="ht-hint"><kbd>⌘</kbd> <kbd>↵</kbd> adds it</span></div></form>';
  }
  // comments on the whole row (its own "+", or a text selection); with
  // closed set, the ones on its lines too
  function comments(ctx, r, closed) {
    var h = '';
    ctx.comments.forEach(function (c) { if (c.key === r.key && (!c.part || closed)) h += card(ctx, c); });
    Object.keys(ctx.ps.forms).forEach(function (fk) { var f = ctx.ps.forms[fk]; if (f.key === r.key && (!f.part || closed)) h += formHTML(ctx, fk, f); });
    return h ? '<div class="ht-cards">' + h + '</div>' : '';
  }
  function card(ctx, c) {
    var editing = ctx.ps.editing[c.id];
    var h = '<div class="ht-card' + (c.resolved ? ' ht-sent' : '') + '" data-cid="' + esc(c.id) + '">' +
      '<div class="ht-ch"><b>You</b><span>' + dayTime(c.at) + '</span><span class="ht-anchor" title="Line in the transcript, and its uuid">' + esc(c.anchor) + '</span></div>' +
      '<div class="ht-on">' + onHtml(c.on) + '</div>' + quoteHTML(c.quote);
    if (editing) {
      h += '<textarea class="ht-edit" aria-label="Edit comment">' + esc(c.body) + '</textarea>' +
        '<div class="ht-cact"><button type="button" class="btn btn-sm btn-primary" data-act="edit-save">Save</button><button type="button" class="btn btn-sm ht-ghost" data-act="edit-cancel">Cancel</button></div>';
    } else {
      h += '<div class="ht-cbody">' + esc(c.body) + '</div>';
      if (c.resolved && c.receipt) h += '<div class="ht-receipt">✓ Sent to ' + esc(c.receipt.to) + ' at ' + hhmm(c.receipt.at) + '</div>';
      else h += '<div class="ht-cact"><button type="button" class="btn btn-sm" data-act="send-one">Send now</button><button type="button" class="btn btn-sm ht-ghost" data-act="edit">Edit</button><button type="button" class="btn btn-sm ht-ghost" data-act="del">Delete</button></div>';
    }
    return h + '</div>';
  }

  // ---------- turns ----------
  function countsHtml(tn) {
    if (!tn.tools) return tn.messages ? 'no tool calls' : tn.marks || tn.thinking ? plural(tn.marks + tn.thinking, 'note') : 'no reply';
    return plural(tn.tools, 'tool call') + (tn.failed ? ', <span class="ht-bad">' + tn.failed + ' failed</span>' : '') + (tn.denied ? ', <span class="ht-warn">' + tn.denied + ' denied</span>' : '');
  }
  function commentChip(ctx, idxs) {
    var set = {}, open = 0, sent = 0;
    idxs.forEach(function (i) { set[ctx.model.rows[i].key] = 1; });
    ctx.comments.forEach(function (c) { if (set[c.key]) { if (c.resolved) sent++; else open++; } });
    if (open) return '<span class="ht-count">' + plural(open, 'comment') + '</span>';
    if (sent) return '<span class="ht-count ht-count-sent">' + sent + ' sent</span>';
    return '';
  }
  function turnRows(tn) { return (tn.opener !== null ? [tn.opener] : []).concat(tn.rows); }
  function bandInner(ctx, tn) {
    var nImg = imageCount(ctx, turnRows(tn));
    return '<span class="ht-chev" aria-hidden="true">▸</span><span class="ht-tn">' + (tn.n ? 'Turn ' + tn.n : 'Start') + '</span>' +
      '<span class="ht-tl">' + esc(tn.label) + '</span>' + commentChip(ctx, turnRows(tn)) +
      '<span class="ht-tc">' + countsHtml(tn) + (nImg ? ', ' + plural(nImg, 'image') : '') + '</span><span class="ht-td">' + dur(tn.ms) + '</span><span class="ht-tt">' + dayTime(tn.start) + '</span>';
  }
  function band(ctx, tn) {
    var open = !!ctx.ps.openBands[tn.n];
    return '<section class="ht-turn" data-turn="' + tn.n + '"><button type="button" class="ht-band" data-act="band" aria-expanded="' + open + '">' + bandInner(ctx, tn) + '</button>' +
      (open ? '<div class="ht-turn-body">' + turnRows(tn).map(function (i) { return row(ctx, ctx.model.rows[i]); }).join('') + '</div>' : '') + '</section>';
  }
  function foldInner(ctx, idxs) {
    var c = { tools: 0, failed: 0, denied: 0, messages: 0, thinking: 0, marks: 0 };
    idxs.forEach(function (i) {
      var r = ctx.model.rows[i];
      if (r.kind === 'tool') { c.tools++; if (r.status === 'error' || r.status === 'interrupted') c.failed++; if (r.status === 'denied') c.denied++; }
      else if (r.kind === 'message') c.messages++; else if (r.kind === 'thinking') c.thinking++; else c.marks++;
    });
    var rest = [c.messages ? plural(c.messages, 'message') : '', c.thinking ? c.thinking + ' thinking' : '', c.marks ? plural(c.marks, 'note') : ''].filter(Boolean).join(', ');
    var nImg = imageCount(ctx, idxs);
    return '<span class="ht-chev" aria-hidden="true">▸</span><span class="ht-fl"><b>' + plural(c.tools, 'tool call') + '</b>' +
      (c.failed ? ', <span class="ht-bad">' + c.failed + ' failed</span>' : '') + (c.denied ? ', <span class="ht-warn">' + c.denied + ' denied</span>' : '') +
      (nImg ? ', ' + plural(nImg, 'image') : '') +
      (rest ? ' <span class="ht-muted">and ' + rest + ' in between</span>' : '') + '</span>' + commentChip(ctx, idxs);
  }
  function lastTurn(ctx) {
    var model = ctx.model, L = model.last, tn = model.turns[model.turns.length - 1];
    var h = '<section class="ht-turn ht-last" data-turn="' + tn.n + '"><div class="ht-last-h"><b>' + (tn.n ? 'Turn ' + tn.n : 'Start') + '</b><span>the last turn</span><span>' +
      dayTime(tn.start) + '</span><span>' + dur(tn.ms) + '</span><span class="ht-rule"></span></div>';
    if (tn.opener !== null) h += row(ctx, model.rows[tn.opener]);
    var fold = L.fold, after = [];
    if (L.state !== 'answered' && L.latestText !== null) {
      var k = L.fold.indexOf(L.latestText);
      fold = L.fold.slice(0, k);
      after = L.fold.slice(k);
    }
    if (fold.length) {
      h += '<button type="button" class="ht-fold" data-act="fold" aria-expanded="' + !!ctx.ps.foldOpen + '">' + foldInner(ctx, fold) + '</button>';
      // closed, the fold still shows the pictures its steps returned
      if (ctx.ps.foldOpen) h += '<div class="ht-fold-body">' + fold.map(function (i) { return row(ctx, model.rows[i]); }).join('') + '</div>';
      else h += foldPictures(ctx, fold);
    }
    if (L.state === 'answered') {
      h += '<div class="ht-answer-h">Last answer</div>';
      L.answer.forEach(function (i) { var r = model.rows[i]; h += row(ctx, r, { final: r.kind === 'message' }); });
    } else {
      if (after.length) h += '<div class="ht-answer-h">Latest text so far</div>' + after.map(function (i) { return row(ctx, model.rows[i], { final: i === L.latestText }); }).join('');
      var lr = L.lastRow !== null ? model.rows[L.lastRow] : null;
      var why = L.state === 'interrupted' ? 'You stopped this turn before it gave a final answer.'
        : L.state === 'waiting' ? 'No reply yet: the transcript ends at your prompt.'
          : 'No final answer yet. The agent is still working' + (lr && lr.kind === 'tool' ? ': its last step is a ' + esc(lr.label) + ' call' + (lr.status === 'pending' ? ' with no result yet' : '') : '') + '.';
      h += '<div class="ht-notice">' + why + '</div>';
    }
    return h + '</section>';
  }
  function stream(ctx) {
    var turns = ctx.model.turns;
    if (!turns.length) return '<div class="ht-empty">This transcript has no prompt and no reply yet.</div>';
    return turns.slice(0, -1).map(function (tn) { return band(ctx, tn); }).join('') + lastTurn(ctx);
  }

  var api = {
    stream: stream, band: band, bandInner: bandInner, lastTurn: lastTurn, row: row, card: card, partLines: partLines,
    fmt: { hhmm: hhmm, dayTime: dayTime, dur: dur, size: size, plural: plural, anchor: anchor },
  };
  if (typeof window !== 'undefined') {
    window.crit = window.crit || {};
    window.crit.herdrTranscriptView = api;
  }
  if (typeof module === 'object' && module.exports) module.exports = api;
})();
