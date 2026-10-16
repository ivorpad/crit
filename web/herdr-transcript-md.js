// herdr-transcript-md.js: a small markdown renderer for agent text on the
// /herdr page. The text comes from agents on other machines, so every
// character is escaped first; the only HTML that comes out is the tags
// written here. Links keep http(s) only. Two tags agents write draw more:
// a picture tag naming a local picture file, ![alt](/abs/shot.png), is an
// <img> on GET /api/herdr/file (only with opt.path, the pane's review path;
// any other picture target stays a link or text), and a "preview" fence of
// localhost URLs is a live frame per URL (blocks() only).
// Covers what agents write: paragraphs, headings, lists (nested), task
// boxes, fences, tables, quotes, rules, inline code, bold, italic, strike.
// opt: { path: the pane's review path, bad: { 'file|<path>': true } for
// pictures that failed to load, drawn as a note }.
// Mounted on window.crit.herdrTranscriptMd. Dependencies: none.
(function () {
  'use strict';

  function esc(s) {
    return String(s === null || s === undefined ? '' : s).replace(/[&<>"']/g, function (c) {
      return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c];
    });
  }

  // Inline: code spans become placeholders first, so nothing inside them is
  // formatted but bold or a link can still wrap one (**How `x` works:**).
  // Picture tags become placeholders next, so the link rule never sees them.
  function inline(src, opt) {
    var held = [];
    function hold(html) { held.push(html); return '\u0000' + (held.length - 1) + '\u0000'; }
    var s = String(src).replace(/\u0000/g, '').replace(/(`+)([^`]|[^`][\s\S]*?[^`])\1(?!`)/g, function (_, _ticks, body) {
      return hold('<code>' + esc(body) + '</code>');
    });
    s = s.replace(PIC, function (whole, alt, target) {
      var h = pictureTag(alt, target, opt);
      return h === null ? whole : hold(h);
    });
    return fmt(s).replace(/\u0000(\d+)\u0000/g, function (_, i) { return held[+i]; });
  }
  var LINK_ATTRS = ' target="_blank" rel="noopener noreferrer"';

  // ---------- picture tags ----------
  var PIC = /!\[([^\]\n\u0000]*)\]\(\s*(<[^>\n\u0000]*>|[^\s)\u0000]+)\s*\)/g;
  var PIC_EXT = /\.(png|jpe?g|gif|webp)$/i;
  function unbracket(t) { return /^<[\s\S]*>$/.test(t) ? t.slice(1, -1) : t; }
  // the absolute path a picture tag names (file:// stripped), or '' when it
  // names anything else: a URL, a relative path, a file that is no picture
  function localPicture(target) {
    var t = unbracket(target);
    if (/^file:\/\//i.test(t)) {
      t = t.slice(7);
      try { t = decodeURIComponent(t); } catch (_) { /* keep it as written */ }
    }
    return t.charAt(0) === '/' && PIC_EXT.test(t) && !/[\u0000-\u001f]/.test(t) ? t : '';
  }
  function pictureTag(alt, target, opt) {
    var file = localPicture(target);
    if (file && opt && opt.path) {
      if (opt.bad && opt.bad['file|' + file]) return '<span class="ht-imgbad">image not available: ' + esc(file) + '</span>';
      var src = esc('/api/herdr/file?path=' + encodeURIComponent(opt.path) + '&file=' + encodeURIComponent(file));
      return '<a class="ht-imglink ht-mdpic" href="' + src + '"' + LINK_ATTRS + '><img class="ht-img ht-mdimg" loading="lazy" decoding="async" src="' + src +
        '" alt="' + esc(alt || file.split('/').pop()) + '" data-img="' + esc('file|' + file) + '" data-file="' + esc(file) + '"></a>';
    }
    var url = unbracket(target);
    if (/^https?:\/\/\S+$/i.test(url)) return '<a href="' + esc(url) + '"' + LINK_ATTRS + '>' + esc(alt || url) + '</a>';
    return null;
  }
  // a paragraph of nothing but local picture tags is drawn as a picture block
  function onlyPictures(para, opt) {
    if (!opt || !opt.path) return false;
    var n = 0;
    var rest = para.join(' ').replace(PIC, function (whole, _alt, target) {
      if (!localPicture(target)) return whole;
      n++;
      return '';
    });
    return n > 0 && !rest.trim();
  }

  // ---------- preview fences ----------
  // ```preview (or ```preview height=800) whose every line is a localhost URL
  var PREVIEW_OPEN = /^\s{0,3}(?:`{3,}|~{3,})\s*preview(?:\s+height=(\d+))?\s*$/;
  var LOCAL_URL = /^http:\/\/(?:localhost|127\.0\.0\.1|\[::1\]):(\d{1,5})(?:[/?#]\S*)?$/;
  function previewOf(open, body) {
    var m = PREVIEW_OPEN.exec(open);
    if (!m) return null;
    var urls = body.map(function (x) { return x.trim(); }).filter(Boolean);
    var ok = urls.length > 0 && urls.every(function (u) { var p = LOCAL_URL.exec(u); return p && +p[1] > 0 && +p[1] < 65536; });
    return ok ? { height: m[1] ? Math.min(2000, Math.max(200, parseInt(m[1], 10))) : 560 } : null;
  }
  // one frame: a caption with the URL and "open", then the page. The frame
  // box is what a drag resizes; herdr-transcript-ctl.js keeps a live one
  // across redraws.
  function previewHTML(url, height) {
    var u = esc(url);
    return '<figure class="ht-preview"><figcaption><span class="ht-pv-url" title="' + u + '">' + u + '</span><a href="' + u + '"' + LINK_ATTRS + '>open</a></figcaption>' +
      '<div class="ht-pv-frame" style="height:' + height + 'px"><iframe src="' + u + '" loading="lazy" sandbox="allow-scripts allow-same-origin allow-forms allow-popups" referrerpolicy="no-referrer" title="Preview of ' + u + '"></iframe></div></figure>';
  }
  function fmt(s) {
    s = esc(s);
    s = s.replace(/\[([^\]\n]+)\]\((https?:\/\/[^\s)\u0000]+)\)/g, '<a href="$2"' + LINK_ATTRS + '>$1</a>');
    s = s.replace(/(^|[\s(])(https?:\/\/[^\s<\u0000]*[^\s<.,;:!?)\]\u0000])/g, '$1<a href="$2"' + LINK_ATTRS + '>$2</a>');
    s = s.replace(/\*\*([^*\n]+)\*\*/g, '<strong>$1</strong>');
    s = s.replace(/__([^_\n]+)__/g, '<strong>$1</strong>');
    s = s.replace(/(^|[^*\w])\*([^*\s][^*\n]*?)\*(?![*\w])/g, '$1<em>$2</em>');
    s = s.replace(/(^|[^_\w])_([^_\s][^_\n]*?)_(?![_\w])/g, '$1<em>$2</em>');
    s = s.replace(/~~([^~\n]+)~~/g, '<del>$1</del>');
    return s;
  }

  function depthOf(indent) { return indent < 2 ? 0 : indent < 5 ? 1 : indent < 8 ? 2 : 3; }
  function indentOf(l) { var m = /^ */.exec(l.replace(/\t/g, '    ')); return m[0].length; }
  var FENCE = /^\s{0,3}(`{3,}|~{3,})\s*([^\s`]*)/;
  var HEADING = /^\s{0,3}(#{1,6})\s+(.*?)\s*#*\s*$/;
  var RULE = /^\s{0,3}([-*_])(\s*\1){2,}\s*$/;
  var ITEM = /^(\s*)([-*+]|\d{1,9}[.)])\s+(.*)$/;
  var QUOTE = /^\s{0,3}>\s?/;
  var TABLE_SEP = /^\s*\|?\s*:?-{1,}:?\s*(\|\s*:?-{1,}:?\s*)+\|?\s*$/;
  function isTableStart(lines, i) { return /\|/.test(lines[i]) && i + 1 < lines.length && TABLE_SEP.test(lines[i + 1]); }
  function startsBlock(lines, i) {
    var l = lines[i];
    return FENCE.test(l) || HEADING.test(l) || RULE.test(l) || ITEM.test(l) || QUOTE.test(l) || isTableStart(lines, i);
  }
  function cells(l) {
    l = l.trim();
    if (l.charAt(0) === '|') l = l.slice(1);
    if (l.slice(-1) === '|' && l.slice(-2) !== '\\|') l = l.slice(0, -1);
    return l.split(/(?<!\\)\|/).map(function (c) { return c.trim().replace(/\\\|/g, '|'); });
  }
  // tableAt: the table whose header is line i: its cells, alignments, body
  // rows, the line after it, and the columns that stay on one line. A column
  // whose every cell is short (a name, a state) gets ht-nw: auto layout would
  // otherwise give nearly all the width to a long prose column and wrap the
  // short ones mid-phrase ("built, / one gap").
  var SHORT_CELL = 30;
  function tableAt(lines, i) {
    var head = cells(lines[i]), rows = [], j = i + 2;
    var aligns = cells(lines[i + 1]).map(function (c) { return /^:-+:$/.test(c) ? 'center' : /-:$/.test(c) ? 'right' : ''; });
    while (j < lines.length && lines[j].trim() && /\|/.test(lines[j])) { rows.push(cells(lines[j])); j++; }
    var nw = head.map(function (_, k) {
      return [head].concat(rows).every(function (r) { return String(r[k] || '').replace(/[`*_]/g, '').length <= SHORT_CELL; });
    });
    return { head: head, aligns: aligns, rows: rows, nw: nw, end: j };
  }
  function cellAttrs(align, nw) { return (nw ? ' class="ht-nw"' : '') + (align ? ' style="text-align:' + align + '"' : ''); }

  function md(text, opt) {
    var lines = String(text || '').replace(/\r\n?/g, '\n').split('\n');
    var out = [], i = 0;
    function inl(x) { return inline(x, opt); }
    while (i < lines.length) {
      var l = lines[i];
      if (!l.trim()) { i++; continue; }
      var f = FENCE.exec(l);
      if (f) {
        var fence = f[1], buf = [];
        i++;
        while (i < lines.length && !(new RegExp('^\\s{0,3}' + fence.charAt(0) + '{' + fence.length + ',}\\s*$')).test(lines[i])) { buf.push(lines[i]); i++; }
        i++;
        out.push('<pre class="ht-code"' + (f[2] ? ' data-lang="' + esc(f[2]) + '"' : '') + '><code>' + esc(buf.join('\n')) + '</code></pre>');
        continue;
      }
      var h = HEADING.exec(l);
      if (h) { var lv = Math.min(h[1].length + 2, 6); out.push('<h' + lv + '>' + inl(h[2]) + '</h' + lv + '>'); i++; continue; }
      if (RULE.test(l)) { out.push('<hr>'); i++; continue; }
      if (isTableStart(lines, i)) {
        var tb = tableAt(lines, i);
        var t = '<div class="ht-table"><table><thead><tr>' + tb.head.map(function (c, k) { return '<th' + cellAttrs(tb.aligns[k], tb.nw[k]) + '>' + inl(c) + '</th>'; }).join('') + '</tr></thead><tbody>';
        tb.rows.forEach(function (row) {
          t += '<tr>' + tb.head.map(function (_, k) { return '<td' + cellAttrs(tb.aligns[k], tb.nw[k]) + '>' + inl(row[k] || '') + '</td>'; }).join('') + '</tr>';
        });
        out.push(t + '</tbody></table></div>');
        i = tb.end;
        continue;
      }
      if (QUOTE.test(l)) {
        var q = [];
        while (i < lines.length && lines[i].trim() && (QUOTE.test(lines[i]) || !startsBlock(lines, i))) { q.push(lines[i].replace(QUOTE, '')); i++; }
        out.push('<blockquote>' + md(q.join('\n'), opt) + '</blockquote>');
        continue;
      }
      if (ITEM.test(l)) { var lst = list(lines, i, opt); out.push(lst.html); i = lst.next; continue; }
      var para = [];
      while (i < lines.length && lines[i].trim() && (para.length === 0 || !startsBlock(lines, i))) { para.push(lines[i].trim()); i++; }
      out.push('<p>' + para.map(inl).join('<br>') + '</p>');
    }
    return out.join('\n');
  }

  function list(lines, i, opt) {
    var first = ITEM.exec(lines[i]);
    var base = indentOf(first[1]);
    var ordered = /\d/.test(first[2]);
    var items = [], cur = null, loose = false;
    while (i < lines.length) {
      var l = lines[i], m = ITEM.exec(l);
      if (m && indentOf(m[1]) === base && /\d/.test(m[2]) === ordered) {
        cur = { lines: [m[3]], pad: m[0].length - m[3].length };
        items.push(cur);
        i++;
        continue;
      }
      if (!l.trim()) {
        var j = i + 1;
        while (j < lines.length && !lines[j].trim()) j++;
        var nm = j < lines.length ? ITEM.exec(lines[j]) : null;
        if (j < lines.length && (indentOf(lines[j]) > base || (nm && indentOf(nm[1]) === base && /\d/.test(nm[2]) === ordered))) {
          if (cur) cur.lines.push('');
          loose = true;
          i++;
          continue;
        }
        break;
      }
      if (cur && indentOf(l) > base) { cur.lines.push(l.slice(Math.min(indentOf(l), cur.pad))); i++; continue; }
      if (cur && !startsBlock(lines, i)) { cur.lines.push(l.trim()); i++; continue; }
      break;
    }
    var start = ordered ? parseInt(first[2], 10) : 1;
    var html = (ordered ? '<ol' + (start !== 1 ? ' start="' + start + '"' : '') + '>' : '<ul>') + items.map(function (it) {
      var src = it.lines.join('\n');
      var box = /^\[([ xX])\]\s+/.exec(src);
      if (box) src = src.slice(box[0].length);
      var body = md(src, opt);
      if (!loose) body = body.replace(/^<p>([\s\S]*?)<\/p>/, '$1');
      return '<li' + (box ? ' class="ht-task"' : '') + '>' + (box ? '<span class="ht-box" aria-hidden="true">' + (box[1] === ' ' ? '☐' : '☑') + '</span> ' : '') + body + '</li>';
    }).join('') + (ordered ? '</ol>' : '</ul>');
    return { html: html, next: i };
  }

  // blocks: the same markdown split the way crit's document view splits it,
  // so each piece takes a comment on its own source lines (1-based): a
  // paragraph, a heading, one list item, one code line, one table row, one
  // blockquote line. Fence lines and table separators are not blocks. Every
  // block's html is escaped the same way as md(). Two kinds take no gutter
  // "+" in the view: 'pic' (a paragraph of local picture tags) and 'preview'
  // (one URL line of a preview fence, b.url).
  function blocks(text, opt) {
    var lines = String(text || '').replace(/\r\n?/g, '\n').split('\n');
    var out = [], i = 0, group = 0, ol = {};
    function inl(x) { return inline(x, opt); }
    function add(ls, le, kind, html, extra) {
      if (kind !== 'li') ol = {};
      var b = { ls: ls + 1, le: le + 1, kind: kind, html: html };
      Object.keys(extra || {}).forEach(function (k) { b[k] = extra[k]; });
      out.push(b);
    }
    function cell(tag, c, align, nw) { return '<' + tag + cellAttrs(align, nw) + '>' + inl(c) + '</' + tag + '>'; }
    while (i < lines.length) {
      var l = lines[i];
      if (!l.trim()) { i++; continue; }
      var f = FENCE.exec(l);
      if (f) {
        var close = new RegExp('^\\s{0,3}' + f[1].charAt(0) + '{' + f[1].length + ',}\\s*$');
        var end = i + 1;
        while (end < lines.length && !close.test(lines[end])) end++;
        var pv = previewOf(l, lines.slice(i + 1, end));
        if (pv) {
          for (var pl = i + 1; pl < end; pl++) if (lines[pl].trim()) add(pl, pl, 'preview', previewHTML(lines[pl].trim(), pv.height), { url: lines[pl].trim() });
          i = end + 1;
          continue;
        }
        group++;
        i++;
        while (i < lines.length && !close.test(lines[i])) { add(i, i, 'code', '<code>' + esc(lines[i]) + '</code>', { group: group, lang: f[2] || '' }); i++; }
        i++;
        continue;
      }
      var h = HEADING.exec(l);
      if (h) { var lv = Math.min(h[1].length + 2, 6); add(i, i, 'h', '<h' + lv + '>' + inl(h[2]) + '</h' + lv + '>'); i++; continue; }
      if (RULE.test(l)) { add(i, i, 'hr', '<hr>'); i++; continue; }
      if (isTableStart(lines, i)) {
        var tb = tableAt(lines, i);
        group++;
        add(i, i, 'trow', tb.head.map(function (c, k) { return cell('th', c, tb.aligns[k], tb.nw[k]); }).join(''), { group: group, cols: tb.head.length });
        for (var r = 0; r < tb.rows.length; r++) {
          var row = tb.rows[r];
          add(i + 2 + r, i + 2 + r, 'trow', tb.head.map(function (_, k) { return cell('td', row[k] || '', tb.aligns[k], tb.nw[k]); }).join(''), { group: group, cols: tb.head.length });
        }
        i = tb.end;
        continue;
      }
      if (QUOTE.test(l)) {
        group++;
        while (i < lines.length && lines[i].trim() && QUOTE.test(lines[i])) { add(i, i, 'quote', inl(lines[i].replace(QUOTE, '')), { group: group }); i++; }
        continue;
      }
      var m = ITEM.exec(l);
      if (m) {
        var start = i, content = [m[3]], depth = depthOf(indentOf(m[1]));
        i++;
        while (i < lines.length && lines[i].trim() && !ITEM.test(lines[i]) && !startsBlock(lines, i)) { content.push(lines[i].trim()); i++; }
        var body = content.join(' '), box = /^\[([ xX])\]\s+/.exec(body);
        if (box) body = body.slice(box[0].length);
        // an ordered list counts on from its first number, as markdown does;
        // the mark is drawn by CSS so a text selection leaves it out
        var deeper = Object.keys(ol), mark;
        for (var q = 0; q < deeper.length; q++) if (+deeper[q] > depth) delete ol[deeper[q]];
        if (/\d/.test(m[2])) { ol[depth] = ol[depth] === undefined ? parseInt(m[2], 10) : ol[depth] + 1; mark = ol[depth] + m[2].slice(-1); }
        else { delete ol[depth]; mark = '•'; }
        if (box) mark = box[1] === ' ' ? '☐' : '☑';
        add(start, i - 1, 'li', '<span class="ht-mark" data-m="' + mark + '" aria-hidden="true"></span><span class="ht-li-t">' + inl(body) + '</span>', { depth: depth });
        continue;
      }
      var pstart = i, para = [];
      while (i < lines.length && lines[i].trim() && (para.length === 0 || !startsBlock(lines, i))) { para.push(lines[i].trim()); i++; }
      if (onlyPictures(para, opt)) add(pstart, i - 1, 'pic', '<div class="ht-pics">' + para.map(inl).join('') + '</div>');
      else add(pstart, i - 1, 'p', '<p>' + para.map(inl).join('<br>') + '</p>');
    }
    return out;
  }

  var api = { md: md, blocks: blocks, inline: inline, esc: esc };
  if (typeof window !== 'undefined') {
    window.crit = window.crit || {};
    window.crit.herdrTranscriptMd = api;
  }
  if (typeof module === 'object' && module.exports) module.exports = api;
})();
