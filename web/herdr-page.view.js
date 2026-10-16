// herdr-page.view.js: HTML for the /herdr page around the transcript: the
// strip, the top line, a pane's header, the sweep's view of a pane (last
// answer, dialog, input line, notes), the reply box, and the moments between
// panes. Every string from a pane goes through esc() or the escaped markdown
// renderer: it comes from agents on other machines.
// Mounted on window.crit.herdrPageView.
// Dependencies: window.crit.herdrModel, herdrPageModel, herdrTranscriptMd, herdrTranscriptView.
(function () {
  'use strict';
  var HM = window.crit.herdrModel, PM = window.crit.herdrPageModel, TM = window.crit.herdrTranscriptMd, F = window.crit.herdrTranscriptView.fmt;
  var esc = TM.esc;

  function ago(iso) {
    var t = Date.parse(iso);
    if (isNaN(t)) return '';
    var s = Math.max(0, Math.round((Date.now() - t) / 1000));
    return s < 60 ? s + 's ago' : s < 3600 ? Math.round(s / 60) + 'm ago' : Math.round(s / 3600) + 'h ago';
  }
  var KEYS = '<p class="hp-keys"><kbd>/</kbd> find a pane · <kbd>J</kbd> next pane · <kbd>K</kbd> previous pane (strip order) · <kbd>N</kbd> next in pass · ' +
    '<kbd>R</kbd> mark read · <kbd>W</kbd> watch · <kbd>⌘</kbd><kbd>↵</kbd> Finish review · <kbd>Esc</kbd> leave the reply box</p>';

  // ---------- the strip ----------
  // a pane's dot: its state's colour, its agent's mark (C claude, X codex,
  // O opencode, π pi) inside
  function dotHTML(state, agent) {
    return '<span class="hp-dot hp-' + state + ' hp-adot"><b class="hp-am">' + esc(PM.agentMark(agent)) + '</b></span>';
  }
  // byPath: the items, for each dot's agent and herdr pane id
  function strip(groups, notices, byPath) {
    var byLabel = {};
    (notices || []).forEach(function (n) { byLabel[n.label] = n; });
    return groups.map(function (g, gi) {
      var n = byLabel[g.machine.label];
      var cls = 'hp-mgroup' + (gi === 0 ? ' hp-first' : '') + (gi >= groups.length - 2 ? ' hp-lastg' : '');
      var where = g.machine.label + (g.machine.instabox ? ' · instabox ' + g.machine.instabox + (g.machine.instabox_state ? ' (' + g.machine.instabox_state + ')' : '') : '');
      return '<div class="' + cls + '" role="group" aria-label="' + esc(g.machine.label) + '"><span class="hp-mlabel" title="' + esc(where) + '">' + esc(g.machine.label) + '</span>' +
        (g.paths.length ? '<span class="hp-mdots">' + g.paths.map(function (p) {
          var it = (byPath && byPath[p]) || {};
          return '<button type="button" class="hp-fdot" data-path="' + esc(p) + '">' + dotHTML('read', it.agent) +
            '<span class="hp-pid">' + esc(it.pane_id || '') + '</span><span class="hp-cb" aria-hidden="true">↩</span></button>';
        }).join('') + '</span>' : '<span class="hp-none" title="' + esc(n ? n.text : 'no agent panes') + '">' + esc(n && n.state === 'paused' ? 'paused' : n && n.state !== 'reachable' ? n.state : 'none') + '</span>') + '</div>';
    }).join('') +
      '<span class="hp-legend" aria-hidden="true"><span><i class="hp-dot hp-asks"></i>asks you</span><span><i class="hp-dot hp-unread"></i>new answer</span>' +
      '<span><i class="hp-dot hp-working"></i>working</span><span><i class="hp-dot hp-sent"></i>sent</span><span><i class="hp-dot hp-read"></i>read</span><span><b class="hp-cbk">↩</b>came back</span>' +
      '<span class="hp-lagents"><b>C</b> claude <b>X</b> codex <b>O</b> opencode <b>π</b> pi <b>G</b> grok <b>Cu</b> cursor</span></span>';
  }
  // the finder's list: one row per pane, the one you are on marked
  function findHTML(list, sel, here) {
    if (!list.length) return '<p class="hp-find-none">No pane matches. Try a name, a pane id like wSJ:pD, an agent, a machine or a project.</p>';
    return list.map(function (p, i) {
      var it = p.item, project = String(it.cwd || '').replace(/\/+$/, '').split('/').pop();
      return '<div class="hp-find-row' + (p.path === here ? ' hp-here' : '') + '" role="option" id="find-' + i + '" aria-selected="' + (i === sel) + '" data-go="' + esc(p.path) + '">' +
        dotHTML(p.state, it.agent) + '<span class="hp-find-name">' + esc(p.name) + '</span>' +
        '<span class="hp-find-meta">' + esc([it.machine, it.pane_id, it.agent, project].filter(Boolean).join(' · ')) + '</span>' +
        '<span class="hp-find-state">' + esc(p.path === here ? 'you are here' : PM.STATE_LABEL[p.state]) + '</span></div>';
    }).join('');
  }
  function nameButton(path, name, state, machine) {
    return '<button type="button" class="hp-lnk" data-go="' + esc(path) + '" title="' + esc(machine + ' · ' + name + ' · ' + PM.STATE_LABEL[state]) + '">' +
      '<span class="hp-dot hp-' + state + '" aria-hidden="true"></span><span class="hp-t">' + esc(name) + '</span></button>';
  }

  // ---------- a pane ----------
  function backHTML(prev) {
    return '<div class="hp-backrow">' + (prev ? '<button type="button" class="hp-linkbtn" id="backLink">← Back to ' + esc(prev.name) + '</button> <span class="hp-muted">(' +
      PM.STATE_LABEL[prev.state].toLowerCase() + ')</span>' : '') + '</div>';
  }
  function metaHTML(p, open) {
    var it = p.item, m = p.machine;
    var where = it.machine + (m && m.instabox ? ' (' + m.instabox + ')' : '');
    var project = String(it.cwd || '').replace(/\/+$/, '').split('/').pop();
    return '<div class="hp-meta"><span class="hp-chip hp-' + p.state + '">' + PM.STATE_LABEL[p.state] + '</span>' +
      (p.came ? '<span class="hp-cameback" title="It is counted in the new-since-you-started button in the top line">came back, see New</span>' : '') +
      (it.status && it.status !== 'idle' && it.status !== 'done' && it.status !== 'working' ? '<span class="hp-status">' + esc(it.status.replace(/_/g, ' ')) + '</span>' : '') +
      '<span class="hp-mono">' + esc([where, it.pane_id, it.agent, project].filter(Boolean).join(' · ')) + '</span>' +
      (it.last && it.last.at ? '<span>last answer ' + ago(it.last.at) + '</span>' : '') +
      (open ? '<button type="button" class="hp-linkbtn" id="toReply">' + F.plural(open, 'comment') + ' to send</button>' : '') + '</div>';
  }
  // the sweep's dialog: options stay off, answer in herdr (Focus)
  function dialogHTML(it) {
    var d = HM.dialogCardModel(it);
    if (!d) return '';
    var h = '<section class="hp-dialog" aria-label="' + esc(d.author + ' ' + d.kind) + '"><div class="hp-sec-h">' + esc(d.author) + ' · ' + esc(d.kind) + '</div>';
    if (d.text) h += '<div class="hp-dialog-text">' + esc(d.text) + '</div>';
    if (d.screen) h += '<pre class="hp-screen">' + esc(d.screen) + '</pre>';
    if (d.options.length) {
      h += '<div class="hp-opts" role="group" aria-label="Options">' + d.options.map(function (o) {
        return '<span class="hp-tip" title="' + esc(d.tooltip) + '"><button type="button" class="btn btn-sm' + (o.current ? ' hp-current' : '') + '" disabled aria-disabled="true">' + esc(o.n + '. ' + o.label) + '</button></span>';
      }).join('') + '</div>';
    }
    return h + '<div class="hp-dialog-note">Answer it in herdr: <button type="button" class="hp-linkbtn" data-focus="1">Focus this pane</button>. The option buttons stay off (' + esc(d.tooltip) + ').</div></section>';
  }
  function extrasHTML(it) {
    var h = dialogHTML(it);
    var g = HM.classifyGhost(it.input_line);
    if (g.state !== 'none') h += '<div class="hp-input hp-input-' + g.state + '"><span class="hp-input-label">On the input line, ' + esc(g.label) + '</span><span class="hp-input-text">❯ ' + esc(g.text) + '</span></div>';
    if (it.status === 'exited' && it.resume_command) h += '<div class="hp-resume">Exited. Resume it in its pane with <code>' + esc(it.resume_command) + '</code></div>';
    var notes = Array.isArray(it.notes) ? it.notes.filter(function (n) { return typeof n === 'string' && n; }) : [];
    if (notes.length) h += '<details class="hp-notes"><summary>' + F.plural(notes.length, 'note') + ' from the sweep</summary><ul>' + notes.map(function (n) { return '<li>' + esc(n) + '</li>'; }).join('') + '</ul></details>';
    return h;
  }
  // the sweep's view: its last answer, and the whole pane on demand
  function sweepHTML(p, docState) {
    var it = p.item, last = it.last || {}, h = '';
    if (last.text && !(p.state === 'asks' && it.dialog)) {
      h += '<section class="hp-msg"><div class="hp-sec-h">Its last answer' + (last.source === 'screen' ? ', from its screen' : '') + (last.truncated ? ' (cut short)' : '') + '</div>' +
        (last.source === 'screen' ? '<pre class="hp-lastmsg hp-screen-text">' + esc(last.text) + '</pre>' : '<div class="hp-lastmsg ht-md">' + TM.md(last.text) + '</div>') + '</section>';
    } else if (!last.text) {
      h += '<section class="hp-msg"><div class="hp-lastmsg hp-empty">Nothing was read from this pane. Its notes say why.</div></section>';
    }
    if (last.in_progress && last.in_progress.prompt) {
      h += '<div class="hp-inprogress">Working on: ' + esc(last.in_progress.prompt) + (last.in_progress.latest_text ? '<br><span class="hp-muted">Latest: ' + esc(last.in_progress.latest_text) + '</span>' : '') + '</div>';
    }
    var d = docState || {};
    h += '<div class="hp-whole"><button type="button" class="btn btn-sm hp-ghost" id="toggleDoc" aria-expanded="' + !!d.open + '">' + (d.open ? '▾ Hide the whole pane' : '▸ Show the whole pane') + '</button>';
    if (d.open) h += '<div class="hp-doc ht-md">' + (d.html || (d.error ? '<p class="hp-err">' + esc(d.error) + '</p>' : '<p class="hp-muted">Reading…</p>')) + '</div>';
    return h + '</div>';
  }
  // the transcript view's frame: loading, error with Retry, or the stream
  function txHTML(p, tx, stream) {
    var where = p.item.machine === 'local' ? 'this Mac' : p.item.machine;
    if (!tx || tx.status === 'loading') return '<p class="hp-txnote"><span class="hp-dot hp-working"></span> Reading the transcript from ' + esc(where) + '…</p>';
    if (tx.status === 'error') {
      return '<div class="hp-why hp-why-err">' + esc(tx.error) + ' <button type="button" class="btn btn-sm" id="txRetry">Retry</button></div>';
    }
    var m = tx.model, turns = m.turns.filter(function (t) { return t.opener !== null; }).length;
    var file = tx.bytes || 0, sent = tx.sent || file;
    var h = '<p class="hp-txnote">From its transcript: ' + F.plural(turns, 'turn') + ', ' + F.plural(m.ledger.parsed, 'line') + ', ' + F.size(sent) +
      (file > sent * 1.1 ? ' of a ' + F.size(file) + ' file (' + ({ codex_rollout: 'model history and token counts', pi_jsonl: 'extension state' }[tx.kind] || 'request logs') + ' left out)' : '') +
      ' · read ' + F.dayTime(tx.fetchedAt) + (tx.ms ? ' in ' + (tx.ms < 1000 ? Math.round(tx.ms) + ' ms' : (tx.ms / 1000).toFixed(1) + ' s') : '') + '</p>';
    if (tx.newer) h += '<div class="hp-newer">This pane has new lines since you opened it. <button type="button" class="hp-linkbtn" id="showNewer">Show them</button></div>';
    return h + '<div class="hp-stream">' + stream() + '</div>';
  }
  function replyHTML(p, open, draft) {
    var can = HM.canSend(p.item);
    return '<section class="hp-reply" aria-label="Reply to this pane"><label class="hp-sr" for="replyText">Reply to ' + esc(p.name) + '</label>' +
      '<textarea id="replyText" placeholder="Reply to ' + esc(p.name) + '…  (⌘↵ opens Finish review)">' + esc(draft || '') + '</textarea><div class="hp-actions">' +
      '<button type="button" class="btn btn-primary" id="finishReview"' + (can ? '' : ' disabled title="' + esc(p.item.status === 'exited' ? 'The agent has exited.' : 'This pane has no herdr state to send to.') + '"') + '>Finish review' + (open ? ' (' + open + ')' : '') + ' <kbd>⌘↵</kbd></button>' +
      '<button type="button" class="btn" id="markRead"' + (p.item.viewed ? ' disabled' : '') + '>Mark read <kbd>R</kbd></button>' +
      '<button type="button" class="btn" id="skip">Skip <kbd>N</kbd></button>' +
      '<button type="button" class="btn" id="watch">' + (p.watched ? '★ Watching' : '☆ Watch') + ' <kbd>W</kbd></button>' +
      '<button type="button" class="btn" id="rename">Rename</button>' +
      '<button type="button" class="btn" data-focus="1">Focus in herdr</button></div>' +
      '<p class="hp-hint">Finish review shows the exact text, then sends this reply' + (p.hasTx ? ' and your row comments' : '') + ' to ' + esc(PM.receiptTo(p.item)) + ' as one prompt and opens the next pane that needs you.' +
      (p.hasTx ? ' Send now on one comment sends only that comment and keeps you here.' : '') + '</p>' +
      (p.sent ? '<p class="hp-sentcopy">Last sent ' + F.dayTime(p.sent.at) + ': <span>' + esc(p.sent.text.length > 160 ? p.sent.text.slice(0, 159) + '…' : p.sent.text) + '</span></p>' : '') + '</section>';
  }
  // o: { prev, open, draft, tx (entry or null), stream (fn), docState }
  function paneHTML(p, o) {
    var h = '<div class="hp-col' + (p.hasTx ? ' hp-wide' : '') + '">' + backHTML(o.prev) +
      '<h1 class="hp-pname">' + esc(p.name) + (p.watched ? ' <span class="hp-pin" title="Watching">★</span>' : '') + '</h1>' + metaHTML(p, o.open);
    if (p.why) h += '<p class="hp-why">' + esc(p.why) + '</p>';
    h += extrasHTML(p.item);
    if (p.hasTx) {
      h += txHTML(p, o.tx, o.stream);
      if (o.tx && o.tx.status === 'error') h += sweepHTML(p, o.docState);
    } else h += sweepHTML(p, o.docState);
    return h + replyHTML(p, o.open, o.draft) + KEYS + '</div>';
  }
  function confirmHTML(c, next) {
    return '<div class="hp-col"><div class="hp-confirm" role="status"><div class="hp-ok">' + (c.kind === 'send' ? 'Sent to ' : 'Marked ') + '<b>' + esc(c.name) + '</b>' + (c.kind === 'send' ? '' : ' read') +
      ' <span class="hp-tick">✓</span></div><div class="hp-nx">' + (next ? 'Next: <b>' + esc(next.name) + '</b> <span class="hp-muted">(' + esc(next.machine) + ')</span>' : 'That was the last pane in this pass.') + '</div></div></div>';
  }
  function doneHTML(prev, info, need, arrivalsHTML) {
    var h = '<div class="hp-col">' + backHTML(prev) + '<div class="hp-alldone"><h1 class="hp-pname">' + (info.total ? 'This pass is empty.' : 'Nothing needed you when this pass started.') + '</h1>';
    if (info.total) h += '<p>You went through all ' + F.plural(info.total, 'pane') + ' that needed you when it started.</p>';
    if (arrivalsHTML) h += '<p>Came in since: ' + arrivalsHTML + '</p>';
    h += need ? '<p>' + (need === 1 ? '1 pane needs' : need + ' panes need') + ' you right now.</p>' : '<p class="hp-muted">Nothing needs you right now. A dot in the strip changes colour when an agent finishes or asks you something.</p>';
    return h + '<button type="button" class="btn btn-primary" id="startPass">Start next pass' + (need ? ' (' + need + ')' : '') + '</button></div>' + KEYS + '</div>';
  }
  function messageHTML(title, text, retry) {
    return '<div class="hp-col"><div class="hp-alldone"><h1 class="hp-pname">' + esc(title) + '</h1><p>' + esc(text) + '</p>' + (retry ? '<button type="button" class="btn" id="bootRetry">Try again</button>' : '') + '</div></div>';
  }

  var api = { strip: strip, findHTML: findHTML, nameButton: nameButton, paneHTML: paneHTML, confirmHTML: confirmHTML, doneHTML: doneHTML, messageHTML: messageHTML };
  if (typeof window !== 'undefined') {
    window.crit = window.crit || {};
    window.crit.herdrPageView = api;
  }
  if (typeof module === 'object' && module.exports) module.exports = api;
})();
