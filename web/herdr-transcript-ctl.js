// herdr-transcript-ctl.js: the transcript area of the /herdr page. It draws
// the open pane's transcript into a container and handles folds, the "+" on
// every row, the gutter "+" on every line (click, drag or shift-click for a
// range, like crit's document view), quotes from a text selection, comment
// cards, the send preview, and pictures that fail to load. The page plugs in
// which pane is open, how its message is composed and delivered, and what
// happens after a send. Row comments stay in localStorage
// (crit-herdr:v1:comments:<pane>|<session>) until sent, and keep their
// receipt after.
// Mounted on window.crit.herdrTranscriptCtl.
// Dependencies: window.crit.herdrTranscriptRules, window.crit.herdrTranscriptView.
(function () {
  'use strict';
  var inNode = typeof module === 'object' && module.exports;
  var R = inNode ? require('./herdr-transcript-rules.js') : window.crit.herdrTranscriptRules;
  var V = inNode ? require('./herdr-transcript-view.js') : window.crit.herdrTranscriptView;
  var F = V.fmt;
  var STORE = 'crit-herdr:v1:';
  var cache = {};

  // ---------- row comments: localStorage, one list per pane and session ----------
  function comments(key) {
    if (!cache[key]) {
      try { cache[key] = JSON.parse(localStorage.getItem(STORE + 'comments:' + key)) || []; } catch (_) { cache[key] = []; }
    }
    return cache[key];
  }
  function saveComments(key) { try { localStorage.setItem(STORE + 'comments:' + key, JSON.stringify(comments(key))); } catch (_) { /* full: keep in memory */ } }
  function openCount(key) { return comments(key).filter(function (c) { return !c.resolved; }).length; }
  if (typeof window !== 'undefined' && window.addEventListener) {
    window.addEventListener('storage', function (e) {
      var k = STORE + 'comments:';
      if (e.key && e.key.indexOf(k) === 0) delete cache[e.key.slice(k.length)];
    });
  }
  // transcript order: by row, then by line inside the row
  function byLine(a, b) { return a.line - b.line || (a.ls || 0) - (b.ls || 0) || a.at - b.at; }
  // a whole row's form is keyed by the row; a line range by row, text and lines
  function formKey(key, part, ls, le) { return part ? key + '|' + part + '|' + ls + '-' + le : key; }
  function $(id) { return document.getElementById(id); }
  function elOf(n) { return n && n.nodeType !== 1 ? n.parentElement : n; }
  function spanAttr(el) { return { ls: +el.getAttribute('data-ls'), le: +el.getAttribute('data-le') }; }

  // o: { root, current() -> { pane: { id, store }, model } or null,
  //      title(pane), compose(list, extra) -> text, receiptTo(pane),
  //      deliver(pane, text, info) -> Promise<{ ok, at, message }>,
  //      changed(pane), sent(pane, info), toast(msg, kind) }
  function create(o) {
    var root = o.root, psById = {}, pending = null, sending = false;
    var dlg = $('preview'), selBtn = $('selbtn');
    var selKey = null, selText = '';
    var drag = null, lastRange = null;

    function ps(id) {
      return psById[id] || (psById[id] = { openBands: {}, foldOpen: false, openRows: {}, showAll: {}, forms: {}, drafts: {}, editing: {}, badImg: {} });
    }
    function ctxOf(c) { return { model: c.model, ps: ps(c.pane.id), comments: comments(c.pane.store), pane: c.pane }; }
    function stream() { var c = o.current(); return c && c.model ? V.stream(ctxOf(c)) : ''; }
    function rowByKey(m, key) { for (var i = 0; i < m.rows.length; i++) if (m.rows[i].key === key) return m.rows[i]; return null; }
    function isFinal(m, r) {
      if (r.kind !== 'message') return false;
      return m.last.state === 'answered' ? m.last.answer.indexOf(r.i) >= 0 : r.i === m.last.latestText;
    }
    function redrawRow(key) {
      var c = o.current(), el = root.querySelector('.ht-row[data-key="' + CSS.escape(key) + '"]'), r = c && c.model && rowByKey(c.model, key);
      if (el && r) keepFrames(function () { el.outerHTML = V.row(ctxOf(c), r, { final: isFinal(c.model, r) }); });
    }
    function redrawTurn(n) {
      var c = o.current();
      if (!c || !c.model) return;
      var el = root.querySelector('.ht-turn[data-turn="' + n + '"]');
      var turns = c.model.turns, tn = turns.filter(function (t) { return t.n === n; })[0];
      if (!el || !tn) return;
      keepFrames(function () { el.outerHTML = tn === turns[turns.length - 1] ? V.lastTurn(ctxOf(c)) : V.band(ctxOf(c), tn); });
    }

    // ---------- preview frames live through a redraw ----------
    // A redraw replaces the HTML, which would reload every preview frame.
    // Where the browser moves a node without unloading it (moveBefore), each
    // frame box waits outside the drawing and takes its copy's place when the
    // copy shows the same URL on the same line of the same row; its page and
    // the height you dragged it to stay. Elsewhere a redraw reloads it.
    var hold = null;
    function frameKey(box) {
      var row = box.closest('.ht-row'), lb = box.closest('.ht-lb'), f = box.querySelector('iframe');
      return (row ? row.getAttribute('data-key') : '') + '|' + (lb ? lb.getAttribute('data-ls') : '') + '|' + (f ? f.getAttribute('src') : '');
    }
    function keepFrames(redraw) {
      var boxes = root.querySelectorAll('.ht-pv-frame'), kept = {};
      if (!boxes.length || typeof root.moveBefore !== 'function') { redraw(); return; }
      if (!hold) { hold = document.createElement('div'); hold.hidden = true; document.body.appendChild(hold); }
      boxes.forEach(function (box) {
        var k = frameKey(box);
        try { hold.moveBefore(box, null); kept[k] = box; } catch (_) { /* this one reloads */ }
      });
      redraw();
      root.querySelectorAll('.ht-pv-frame').forEach(function (box) {
        var k = frameKey(box), old = kept[k];
        if (!old) return;
        try { box.parentNode.moveBefore(old, box); box.remove(); delete kept[k]; } catch (_) { /* the new copy loads */ }
      });
      Object.keys(kept).forEach(function (k) { kept[k].remove(); });
    }
    function unresolved() {
      var c = o.current();
      return c ? comments(c.pane.store).filter(function (x) { return !x.resolved; }) : [];
    }

    // ---------- comments ----------
    function selectionIn(el) {
      var sel = window.getSelection();
      if (!sel || sel.isCollapsed || !sel.rangeCount) return '';
      var a = sel.anchorNode, f = sel.focusNode;
      if (!el.contains(a) || !el.contains(f)) return '';
      if (elOf(a).closest('.ht-cards') || elOf(f).closest('.ht-cards')) return '';
      return sel.toString().replace(/[ \t]+/g, ' ').trim().slice(0, 4000);
    }
    function focusForm(fk) {
      var ta = root.querySelector('.ht-form[data-fk="' + CSS.escape(fk) + '"] textarea');
      if (ta) { ta.focus(); ta.scrollIntoView({ block: 'nearest' }); }
    }
    function openForm(key, quote) {
      var c = o.current(), r = c && c.model && rowByKey(c.model, key);
      if (!r) return;
      ps(c.pane.id).forms[key] = { key: key, part: '', ls: 0, le: 0, quote: quote || '', on: R.describeRow(r, c.model) };
      redrawRow(key);
      hideSelBtn();
      focusForm(key);
    }
    // openRange: a form on lines ls..le of one text in a row, quoting them
    function openRange(key, part, ls, le, draft) {
      var c = o.current(), r = c && c.model && rowByKey(c.model, key);
      if (!r) return;
      var s = ps(c.pane.id), fk = formKey(key, part, ls, le);
      s.forms[fk] = { key: key, part: part, ls: ls, le: le, quote: R.sliceLines(V.partLines(r, part), ls, le), on: R.describeRange(r, c.model, part, ls, le) };
      if (draft && !s.drafts[fk]) s.drafts[fk] = draft;
      lastRange = { pane: c.pane.id, key: key, part: part, ls: ls, le: le, fk: fk };
      redrawRow(key);
      hideSelBtn();
      focusForm(fk);
    }
    function closeForm(fk) {
      var c = o.current();
      if (!c) return;
      delete ps(c.pane.id).forms[fk];
      delete ps(c.pane.id).drafts[fk];
    }
    function addComment(fk) {
      var c = o.current();
      if (!c || !c.model) return null;
      var f = ps(c.pane.id).forms[fk], r = f && rowByKey(c.model, f.key);
      var ta = root.querySelector('.ht-form[data-fk="' + CSS.escape(fk) + '"] textarea');
      var body = ta ? ta.value.trim() : '';
      if (!r) return null;
      if (!body) { if (ta) ta.focus(); o.toast('Write the comment first.'); return null; }
      var cm = {
        id: 'c' + Date.now().toString(36) + Math.floor(Math.random() * 1e4).toString(36), key: f.key, line: r.line, uuid: r.uuid,
        anchor: F.anchor(r), kind: r.kind, turn: r.turn, part: f.part || '', ls: f.ls || 0, le: f.le || 0,
        quote: f.quote, first: f.part ? '' : R.quoteFor(r),
        on: f.part ? R.describeRange(r, c.model, f.part, f.ls, f.le) : R.describeRow(r, c.model),
        body: body, at: Date.now(), resolved: false, receipt: null,
      };
      comments(c.pane.store).push(cm);
      saveComments(c.pane.store);
      closeForm(fk);
      o.changed(c.pane);
      return cm;
    }

    // ---------- the send preview: exactly what herdr types into the pane ----------
    function setSending(on, label) {
      sending = on;
      $('pv-send').disabled = on;
      $('pv-cancel').disabled = on;
      $('pv-send').textContent = label;
    }
    function showError(msg) {
      $('pv-error').textContent = msg || '';
      $('pv-error').hidden = !msg;
    }
    function preview(list, extra, tag) {
      var c = o.current();
      if (!c) return;
      list = list.slice().sort(byLine);
      extra = String(extra || '').trim();
      var text = o.compose(list, extra);
      if (!text) { o.toast('Comment on a row or write a reply first.'); return; }
      pending = { pane: c.pane, ids: list.map(function (x) { return x.id; }), text: text, extra: extra, tag: tag || '' };
      var what = [list.length ? F.plural(list.length, 'review comment') : '', extra ? 'your reply' : ''].filter(Boolean).join(' and ');
      $('pv-title').textContent = o.title(c.pane);
      $('pv-sub').textContent = what.charAt(0).toUpperCase() + what.slice(1) + ', ' + F.plural(text.length, 'character') + '. This is the exact text herdr types into the pane.';
      $('pv-text').textContent = text;
      showError('');
      setSending(false, 'Send');
      dlg.showModal();
    }
    function send() {
      if (!pending || sending) return;
      var p = pending;
      setSending(true, 'Sending…');
      showError('');
      Promise.resolve(o.deliver(p.pane, p.text, p)).then(function (res) {
        if (!res || !res.ok) { setSending(false, 'Send'); showError((res && res.message) || 'Send failed.'); return; }
        var at = res.at || Date.now(), to = o.receiptTo(p.pane);
        comments(p.pane.store).forEach(function (x) { if (p.ids.indexOf(x.id) >= 0) { x.resolved = true; x.receipt = { to: to, at: at }; } });
        saveComments(p.pane.store);
        if (p.tag === 'finish') { ps(p.pane.id).forms = {}; ps(p.pane.id).drafts = {}; }
        pending = null;
        setSending(false, 'Send');
        dlg.close();
        o.sent(p.pane, { at: at, ids: p.ids, text: p.text, extra: p.extra, tag: p.tag, to: to, res: res });
      });
    }
    $('pv-send').addEventListener('click', send);
    $('pv-cancel').addEventListener('click', function () { if (!sending) { pending = null; dlg.close(); } });
    dlg.addEventListener('cancel', function (e) { if (sending) e.preventDefault(); });
    dlg.addEventListener('close', function () { if (!sending) pending = null; });

    // ---------- the gutter "+" on a line: click, drag, shift-click (crit's app.js) ----------
    // Mousedown on a "+" anchors a range; moving over other blocks of the same
    // text grows it either way; mouseup opens the form under its last line.
    // Shift-click on another "+" of the text whose range form you opened last
    // stretches that form to reach it, keeping what you typed.
    function paint() {
      var span = R.spanOf(drag.a, drag.cur);
      drag.lines.querySelectorAll('.ht-lb').forEach(function (el) {
        var b = spanAttr(el);
        el.classList.toggle('ht-ldrag', b.ls >= span.ls && b.le <= span.le);
        el.classList.toggle('ht-lend', b.ls <= span.le && b.le >= span.le);
      });
    }
    function onDragMove(e) {
      if (!drag) return;
      var el = document.elementFromPoint(e.clientX, e.clientY), lb = el && el.closest('.ht-lb');
      if (!lb || lb.closest('.ht-lines') !== drag.lines) return;
      var b = spanAttr(lb);
      if (b.ls === drag.cur.ls && b.le === drag.cur.le) return;
      drag.cur = b;
      paint();
    }
    function onDragEnd() {
      document.removeEventListener('mousemove', onDragMove);
      document.removeEventListener('mouseup', onDragEnd);
      document.body.classList.remove('ht-dragging');
      if (!drag) return;
      var d = drag, span = R.spanOf(d.a, d.cur);
      drag = null;
      openRange(d.key, d.part, span.ls, span.le);
    }
    function startLine(plusEl, shift) {
      var c = o.current(), lb = plusEl.closest('.ht-lb'), lines = lb && lb.closest('.ht-lines'), row = lines && lines.closest('.ht-row');
      if (!c || !row) return;
      var key = row.getAttribute('data-key'), part = lines.getAttribute('data-part'), b = spanAttr(lb), s = ps(c.pane.id);
      var sel = window.getSelection();
      if (sel && !sel.isCollapsed) sel.removeAllRanges();
      if (shift && lastRange && lastRange.pane === c.pane.id && lastRange.key === key && lastRange.part === part && s.forms[lastRange.fk]) {
        var span = R.spanOf(lastRange, b), draft = s.drafts[lastRange.fk] || '';
        closeForm(lastRange.fk);
        openRange(key, part, span.ls, span.le, draft);
        return;
      }
      drag = { lines: lines, key: key, part: part, a: b, cur: b };
      paint();
      document.body.classList.add('ht-dragging');
      document.addEventListener('mousemove', onDragMove);
      document.addEventListener('mouseup', onDragEnd);
    }

    // ---------- events inside the transcript area ----------
    root.addEventListener('mousedown', function (e) {
      if (e.target.closest('.ht-add')) { e.preventDefault(); return; }
      var p = e.target.closest('.ht-lplus');
      if (!p || !root.contains(p) || e.button !== 0) return;
      e.preventDefault();
      startLine(p, e.shiftKey);
    });
    root.addEventListener('click', function (e) {
      var t = e.target.closest('[data-act]');
      if (!t || !root.contains(t)) return;
      var c = o.current();
      if (!c || !c.model) return;
      var act = t.getAttribute('data-act'), s = ps(c.pane.id);
      var row = t.closest('.ht-row'), key = row ? row.getAttribute('data-key') : null;
      var formEl = t.closest('.ht-form'), fk = formEl ? formEl.getAttribute('data-fk') : null;
      var cardEl = t.closest('.ht-card');
      var cm = cardEl ? comments(c.pane.store).filter(function (x) { return x.id === cardEl.getAttribute('data-cid'); })[0] : null;
      if (act === 'band') { var n = +t.closest('.ht-turn').getAttribute('data-turn'); s.openBands[n] = !s.openBands[n]; redrawTurn(n); return; }
      if (act === 'fold') { s.foldOpen = !s.foldOpen; redrawTurn(+t.closest('.ht-turn').getAttribute('data-turn')); return; }
      if (!key) return;
      if (act === 'toggle') { s.openRows[key] = !s.openRows[key]; redrawRow(key); }
      else if (act === 'all') { s.showAll[t.getAttribute('data-id')] = true; redrawRow(key); }
      else if (act === 'add') openForm(key, selectionIn(row));
      else if (act === 'form-cancel') { closeForm(fk); redrawRow(key); }
      else if (act === 'form-send') { var nc = addComment(fk); if (nc) preview([nc], '', 'one'); }
      else if (!cm) return;
      else if (act === 'send-one') preview([cm], '', 'one');
      else if (act === 'edit') { s.editing[cm.id] = true; redrawRow(key); var ed = root.querySelector('.ht-card[data-cid="' + CSS.escape(cm.id) + '"] textarea'); if (ed) ed.focus(); }
      else if (act === 'edit-cancel') { delete s.editing[cm.id]; redrawRow(key); }
      else if (act === 'edit-save') {
        var v = cardEl.querySelector('textarea').value.trim();
        if (v) { cm.body = v; saveComments(c.pane.store); }
        delete s.editing[cm.id];
        redrawRow(key);
      } else if (act === 'del') {
        cache[c.pane.store] = comments(c.pane.store).filter(function (x) { return x !== cm; });
        saveComments(c.pane.store);
        o.changed(c.pane);
        o.toast('Comment deleted.');
      }
    });
    // A picture that fails to load turns into a note, once: ps.badImg tells
    // every later redraw of the pane to draw the note and not ask again.
    // (error does not bubble, so this listens on the way down.)
    root.addEventListener('error', function (e) {
      var img = e.target, c = o.current();
      if (!img || img.tagName !== 'IMG' || !img.classList.contains('ht-img')) return;
      if (c) ps(c.pane.id).badImg[img.getAttribute('data-img')] = true;
      var note = document.createElement('span'), file = img.getAttribute('data-file');
      note.className = 'ht-imgbad';
      note.textContent = 'image not available' + (file ? ': ' + file : '');
      (img.closest('.ht-imglink') || img).replaceWith(note);
    }, true);
    root.addEventListener('submit', function (e) {
      var f = e.target.closest('.ht-form');
      if (!f) return;
      e.preventDefault();
      addComment(f.getAttribute('data-fk'));
    });
    root.addEventListener('input', function (e) {
      var f = e.target.closest && e.target.closest('.ht-form'), c = o.current();
      if (f && c && e.target.tagName === 'TEXTAREA') ps(c.pane.id).drafts[f.getAttribute('data-fk')] = e.target.value;
    });
    root.addEventListener('keydown', function (e) {
      var f = e.target.closest && e.target.closest('.ht-form');
      if (!f) return;
      var fk = f.getAttribute('data-fk');
      if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) { e.preventDefault(); addComment(fk); }
      else if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); closeForm(fk); redrawRow(f.getAttribute('data-key')); }
    });

    // a selection inside one row's text gets its own "+" next to it
    function hideSelBtn() { selBtn.hidden = true; selKey = null; }
    function checkSelection() {
      var sel = window.getSelection();
      if (!sel || sel.isCollapsed || !sel.rangeCount || !o.current()) return hideSelBtn();
      var node = elOf(sel.anchorNode);
      var row = node && node.closest('.ht-row');
      var text = row && root.contains(row) ? selectionIn(row) : '';
      if (!text) return hideSelBtn();
      var rect = sel.getRangeAt(0).getBoundingClientRect(), minTop = root.getBoundingClientRect().top + 4;
      selKey = row.getAttribute('data-key');
      selText = text;
      if (rect.bottom < minTop || rect.top > window.innerHeight - 60) { selBtn.hidden = true; return; }
      selBtn.hidden = false;
      selBtn.style.top = Math.max(minTop, Math.min(window.innerHeight - 90, rect.bottom + 6)) + 'px';
      selBtn.style.left = Math.max(8, Math.min(window.innerWidth - 200, rect.right - 60)) + 'px';
    }
    root.addEventListener('mouseup', function () { setTimeout(checkSelection, 0); });
    root.addEventListener('keyup', function (e) { if (e.shiftKey) checkSelection(); });
    root.addEventListener('scroll', function () { if (selKey) checkSelection(); }, { passive: true });
    selBtn.addEventListener('mousedown', function (e) { e.preventDefault(); });
    selBtn.addEventListener('click', function () { if (selKey) openForm(selKey, selText); });
    document.addEventListener('keydown', function (e) { if (e.key === 'Escape') hideSelBtn(); });

    return { ps: ps, stream: stream, redrawRow: redrawRow, redrawTurn: redrawTurn, preview: preview, unresolved: unresolved, hideSelBtn: hideSelBtn, openRange: openRange, keepFrames: keepFrames };
  }

  var api = { create: create, comments: comments, saveComments: saveComments, openCount: openCount, formKey: formKey, STORE: STORE };
  if (typeof window !== 'undefined') {
    window.crit = window.crit || {};
    window.crit.herdrTranscriptCtl = api;
  }
  if (typeof module === 'object' && module.exports) module.exports = api;
})();
