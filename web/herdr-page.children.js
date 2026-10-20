// herdr-page.children.js: a pane's sub-agents on the /herdr page. The lead pane
// lists them (GET /api/herdr/children): Claude subagents and teammates, Codex
// sub-agents, each with its state and when it last wrote. Opening one shows its
// transcript (GET /api/herdr/transcript?child=) with row comments, and "Ask
// <lead>" sends a question about it to the lead through POST /api/herdr/send:
// drover types into panes only, and a sub-agent has none of its own.
// Mounted on window.crit.herdrPageChildren.
// Dependencies: window.crit.herdrModel, herdrTranscriptMd, herdrTranscriptView, herdrPageView.
(function () {
  'use strict';
  var C = window.crit, HM = C.herdrModel, TM = C.herdrTranscriptMd, F = C.herdrTranscriptView.fmt, PV = C.herdrPageView;
  var esc = TM.esc;

  // the five states and how each reads at a glance
  var STATE = {
    stale: { icon: '⚠', word: 'stale' },
    gone: { icon: '✕', word: 'gone' },
    working: { icon: '●', word: 'working' },
    idle: { icon: '○', word: 'idle' },
    finished: { icon: '✓', word: 'finished' },
  };
  function st(c) { return STATE[c.status] || { icon: '?', word: c.status || 'unknown' }; }
  function kindWord(c) { return c.kind === 'teammate' ? 'teammate' : c.kind === 'codex_subagent' ? 'sub-agent' : 'subagent'; }
  function kindLabel(c) { return [kindWord(c), c.type, c.model].filter(Boolean).join(' · '); }
  function ago(iso) {
    var t = Date.parse(iso);
    if (isNaN(t)) return '';
    var s = Math.max(0, Math.round((Date.now() - t) / 1000));
    return s < 60 ? s + ' s ago' : s < 3600 ? Math.round(s / 60) + ' min ago' : s < 86400 ? Math.round(s / 3600) + ' h ago' : Math.round(s / 86400) + ' d ago';
  }
  // the row's last column: when it finished or last wrote, or what is wrong
  function when(c) {
    if (c.status === 'stale' || c.status === 'gone') return c.reason;
    if (c.status === 'finished') return c.last_output_at ? 'finished ' + ago(c.last_output_at) : 'finished';
    return c.last_output_at ? 'last output ' + ago(c.last_output_at) : c.reason;
  }
  function clip(s, n) { s = String(s || ''); return s.length > n ? s.slice(0, n - 1) + '…' : s; }
  // how a question about the child starts, in the prompt the lead gets
  function prefix(c) { return 'About your ' + kindWord(c) + ' ' + c.name + (c.task ? ' (task: ' + clip(c.task, 80) + ')' : '') + ':'; }

  // ---------- the lead pane's list ----------
  function rowHTML(c) {
    var s = st(c);
    return '<button type="button" class="hp-kid hp-ks-' + esc(c.status) + '" data-child="' + esc(c.id) + '" title="' + esc(s.word + ': ' + c.reason) + '">' +
      '<span class="hp-kid-st"><b aria-hidden="true">' + s.icon + '</b> ' + esc(s.word) + '</span>' +
      '<span class="hp-kid-name">' + esc(c.name) + '</span>' +
      '<span class="hp-kid-kind">' + esc(kindLabel(c)) + '</span>' +
      '<span class="hp-kid-task">' + esc(c.task || '') + '</span>' +
      '<span class="hp-kid-when">' + esc(when(c)) + '</span></button>';
  }
  // state: one entry of create()'s store; nothing until the pane has children
  function sectionHTML(state, leadName) {
    if (!state) return '';
    var list = state.list || [];
    var head = '<div class="hp-sec-h">Sub-agents' + (list.length ? ' · ' + list.length : '') + '</div>';
    if (!state.list && state.status === 'error') {
      return '<section class="hp-kids">' + head + '<p class="hp-kids-note hp-err">' + esc(state.error) + ' <button type="button" class="btn btn-sm" id="kidsRetry">Retry</button></p></section>';
    }
    if (!state.list) return '<section class="hp-kids">' + head + '<p class="hp-kids-note">Reading its sub-agents…</p></section>';
    if (!list.length) return '';
    return '<section class="hp-kids" aria-label="Sub-agents of ' + esc(leadName) + '">' + head +
      '<div class="hp-kid-list">' + list.map(rowHTML).join('') + '</div>' +
      '<p class="hp-kids-note">Stale means still working with no output for ' + Math.round((state.staleAfter || 600) / 60) + ' min. Read ' + F.hhmm(state.at) + ', from this Mac only.</p></section>';
  }

  // ---------- one child ----------
  function askHTML(o) {
    var lead = o.lead, c = o.child, can = HM.canSend(lead.item), to = esc(lead.name) + ' about ' + esc(c.name);
    return '<section class="hp-reply" aria-label="Ask ' + to + '"><label class="hp-sr" for="replyText">Ask ' + to + '</label>' +
      '<textarea id="replyText" placeholder="Ask ' + to + '…  (⌘↵ shows the exact text)">' + esc(o.draft || '') + '</textarea><div class="hp-actions">' +
      '<button type="button" class="btn btn-primary" id="finishReview"' + (can ? '' : ' disabled title="' + esc(lead.name) + ' cannot take text right now."') + '>Ask ' + esc(lead.name) +
      (o.open ? ' (' + o.open + ')' : '') + ' <kbd>⌘↵</kbd></button>' +
      '<button type="button" class="btn" data-lead="1">Back to ' + esc(lead.name) + '</button>' +
      '<button type="button" class="btn" data-focus="1">Focus ' + esc(lead.name) + ' in herdr</button></div>' +
      '<p class="hp-hint">A sub-agent has no pane of its own, so this goes to <b>' + esc(lead.name) + '</b>, its lead, as one prompt that starts “' + esc(prefix(c)) + '”, with your comments on ' +
      esc(c.name) + '’s lines quoted.</p></section>';
  }
  // o: { lead (a pane as herdr-page.js builds it), child, tx, stream, draft, open }
  function childHTML(o) {
    var c = o.child, s = st(c), lead = o.lead;
    var h = '<div class="hp-col hp-wide"><div class="hp-backrow"><button type="button" class="hp-linkbtn" data-lead="1">← Back to ' + esc(lead.name) + '</button></div>' +
      '<h1 class="hp-pname"><span class="hp-crumb">' + esc(lead.name) + ' ›</span> ' + esc(c.name) + '</h1>' +
      '<div class="hp-meta"><span class="hp-chip hp-ks-' + esc(c.status) + '">' + s.icon + ' ' + esc(s.word) + '</span>' +
      '<span class="hp-mono">' + esc([kindLabel(c), c.where].filter(Boolean).join(' · ')) + '</span>' +
      (c.started_at ? '<span>started ' + esc(F.dayTime(Date.parse(c.started_at))) + '</span>' : '') +
      (c.last_output_at ? '<span>last output ' + esc(ago(c.last_output_at)) + '</span>' : '') + '</div>' +
      '<p class="hp-kid-why hp-ks-' + esc(c.status) + '">' + esc(c.reason) + '</p>';
    if (c.task) h += '<p class="hp-kid-line"><b>Task</b> ' + esc(c.task) + '</p>';
    if (c.attach) h += '<p class="hp-kid-line"><b>See it live</b> <code>' + esc(c.attach) + '</code> <button type="button" class="btn btn-sm" data-copy="' + esc(c.attach) + '">Copy</button></p>';
    h += c.transcript ? PV.txHTML(lead, o.tx, o.stream) : '<p class="hp-why">' + esc(c.name) + ' has written no transcript yet.</p>';
    return h + askHTML(o) + '</div>';
  }
  // the view when the child is no longer listed (finished and cleaned up, or the lead left)
  function missingHTML(lead, id) {
    return '<div class="hp-col"><div class="hp-backrow"><button type="button" class="hp-linkbtn" data-lead="1">← Back to ' + esc(lead.name) + '</button></div>' +
      '<p class="hp-why">' + esc(lead.name) + ' lists no sub-agent ' + esc(id) + ' now.</p></div>';
  }

  // ---------- the strip's count on a lead's dot ----------
  function badge(sum) {
    if (!sum || !sum.count) return null;
    var parts = [F.plural(sum.count, 'sub-agent')];
    if (sum.stale) parts.push(sum.stale + ' stale');
    if (sum.gone) parts.push(sum.gone + ' gone');
    if (sum.working) parts.push(sum.working + ' working');
    return { text: String(sum.count), warn: !!(sum.stale || sum.gone), tip: parts.join(', ') };
  }

  // ---------- the lists, one per lead pane, read when shown ----------
  // o: { changed(path) } when a list arrives
  function create(o) {
    var lists = {};
    function done(path, s) {
      if (s.again) { s.again = false; load(path); return; }
      o.changed(path);
    }
    function load(path) {
      var s = lists[path] || (lists[path] = { status: 'loading', list: null });
      if (s.inflight) { s.again = true; return; }
      s.inflight = true;
      fetch('/api/herdr/children?path=' + encodeURIComponent(path)).then(function (res) {
        return res.json().catch(function () { return {}; }).then(function (b) { return { ok: res.ok, status: res.status, body: b || {} }; });
      }).then(function (r) {
        s.inflight = false;
        if (r.ok) {
          s.status = 'ok'; s.list = r.body.children || []; s.staleAfter = r.body.stale_after_s; s.at = Date.parse(r.body.checked_at) || Date.now(); s.error = '';
        } else if (!s.list) {
          s.status = 'error'; s.error = r.body.reason || r.body.error || 'HTTP ' + r.status;
        }
        done(path, s);
      }, function (err) {
        s.inflight = false;
        if (!s.list) { s.status = 'error'; s.error = 'Could not read its sub-agents: ' + err.message; }
        done(path, s);
      });
    }
    function get(path) { return lists[path] || null; }
    function child(path, id) {
      var s = lists[path];
      return s && s.list ? s.list.filter(function (c) { return c.id === id; })[0] || null : null;
    }
    return { load: load, get: get, child: child };
  }

  var api = { create: create, sectionHTML: sectionHTML, childHTML: childHTML, missingHTML: missingHTML, prefix: prefix, badge: badge, kindLabel: kindLabel };
  if (typeof window !== 'undefined') {
    window.crit = window.crit || {};
    window.crit.herdrPageChildren = api;
  }
  if (typeof module === 'object' && module.exports) module.exports = api;
})();
