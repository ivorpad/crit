// herdr-page.model.js: the pure parts of the /herdr page (web/herdr.html):
// each pane's state from its item, the strip's fixed order, the pass you work
// through (page B's queue), the name a pane goes by, when a reply came back,
// and the one message Finish review sends. No DOM, no fetch.
// Mounted on window.crit.herdrPageModel.
// Dependencies: window.crit.herdrModel (herdr-mode.model.js) for composeMessage.
(function () {
  'use strict';
  var HM = typeof module === 'object' && module.exports ? require('./herdr-mode.model.js') : window.crit.herdrModel;

  // ---------- a pane's state, from the item's own fields ----------
  // sent: you sent and herdr has not moved past it yet. asks: it asks you and
  // you have not marked the turn read. working. unread: a finished turn you
  // have not read. read: everything else (idle panes you have seen, exited).
  // A pane the gateway cannot read still has herdr's own agent status.
  function statusOf(item) { return item.status === 'unreadable_by_gateway' && item.herdr_status ? item.herdr_status : item.status; }
  function stateOf(item) {
    if (!item) return 'read';
    if (item.delivery && !item.delivery.acknowledged) return 'sent';
    if (item.needs_you && !item.viewed) return 'asks';
    if (statusOf(item) === 'working') return 'working';
    if (statusOf(item) === 'done' && !item.viewed) return 'unread';
    return 'read';
  }
  var STATE_LABEL = { asks: 'Asks you', unread: 'New answer', working: 'Working', sent: 'Sent, waiting', read: 'Read' };
  function needsAttention(state) { return state === 'asks' || state === 'unread'; }

  // ---------- the name a pane goes by, in the contract's order ----------
  function baseName(cwd) { var s = String(cwd || '').replace(/\/+$/, ''); return s ? s.split('/').pop() : ''; }
  function paneName(item, tx) {
    var t = tx || {}, it = item || {};
    var c = [it.name, t.customTitle, t.aiTitle, it.title, baseName(it.cwd), it.pane_id];
    for (var i = 0; i < c.length; i++) if (typeof c[i] === 'string' && c[i].trim()) return c[i].trim();
    return '?';
  }

  // ---------- the agent in a pane, as one mark on its dot ----------
  var AGENT_MARK = { claude: 'C', codex: 'X', opencode: 'O', pi: 'π', grok: 'G', cursor: 'Cu' };
  function agentMark(agent) {
    var a = String(agent || '');
    return AGENT_MARK[a] || (a ? a.charAt(0).toUpperCase() + a.slice(1, 2) : '?');
  }

  // ---------- finding a pane ----------
  // findPanes: the panes ({path, name, state, item}) whose words hold every
  // word of the query, best first: the pane id itself, then ids that start or
  // end with it (wSJ:pD for "pd"), then names that start with it, then the
  // rest in the order given. A pane's words: its name, path, pane id, agent,
  // machine, project (last part of cwd), title and state.
  function findPanes(query, panes) {
    var q = String(query || '').toLowerCase().trim(), words = q ? q.split(/\s+/) : [];
    var hits = [];
    (panes || []).forEach(function (p, i) {
      var it = p.item || {};
      var hay = [p.name, p.path, it.pane_id, it.agent, it.machine, baseName(it.cwd), it.title, STATE_LABEL[p.state]].join(' ').toLowerCase();
      if (!words.every(function (w) { return hay.indexOf(w) >= 0; })) return;
      var id = String(it.pane_id || '').toLowerCase(), name = String(p.name || '').toLowerCase();
      var rank = !q ? 3 : id === q ? 0 : id.indexOf(q) === 0 || id.split(':').pop() === q || id.slice(-q.length) === q ? 1 : name.indexOf(q) === 0 ? 2 : 3;
      hits.push({ p: p, rank: rank, i: i });
    });
    return hits.sort(function (a, b) { return a.rank - b.rank || a.i - b.i; }).map(function (h) { return h.p; });
  }

  // ---------- the strip: every pane keeps the place it first got ----------
  // The API lists panes that need you first, every sweep; the strip must not
  // follow that. Machines keep the API's machine order; within a machine,
  // panes keep the order they were first seen in. Gone panes drop out.
  function stripOrder(prev, machines, items) {
    var order = (prev || []).slice(), seen = {}, machineOf = {}, mi = {};
    order.forEach(function (p, i) { seen[p] = i; });
    (items || []).forEach(function (it) {
      if (!it || typeof it.path !== 'string') return;
      machineOf[it.path] = it.machine;
      if (!(it.path in seen)) { seen[it.path] = order.length; order.push(it.path); }
    });
    (machines || []).forEach(function (m, i) { mi[m.label] = i; });
    function rank(p) { var r = mi[machineOf[p]]; return r === undefined ? 1e6 : r; }
    return order.filter(function (p) { return p in machineOf; }).sort(function (a, b) { return rank(a) - rank(b) || seen[a] - seen[b]; });
  }

  // ---------- the pass: the panes that needed you when it started ----------
  function startPass(order, states) {
    return { ids: order.filter(function (p) { return needsAttention(states[p]); }), handled: [], arrivals: [] };
  }
  // noteStates: panes that start needing you after the pass began (outside
  // it, or after you handled them) become arrivals: "N new since you started".
  function noteStates(pass, prev, states) {
    if (!pass) return pass;
    pass.ids = pass.ids.filter(function (p) { return p in states; });
    pass.handled = pass.handled.filter(function (p) { return p in states; });
    Object.keys(states).forEach(function (p) {
      if (!needsAttention(states[p]) || needsAttention(prev[p])) return;
      var inPass = pass.ids.indexOf(p) >= 0, handled = pass.handled.indexOf(p) >= 0;
      if ((!inPass || handled) && pass.arrivals.indexOf(p) < 0) pass.arrivals.push(p);
    });
    pass.arrivals = pass.arrivals.filter(function (p) { return needsAttention(states[p]); });
    return pass;
  }
  function markHandled(pass, path) {
    if (!pass) return;
    if (pass.ids.indexOf(path) >= 0 && pass.handled.indexOf(path) < 0) pass.handled.push(path);
    pass.arrivals = pass.arrivals.filter(function (p) { return p !== path; });
  }
  function passInfo(pass) {
    if (!pass) return { total: 0, done: 0, remaining: [], arrivals: [], ids: [] };
    var remaining = pass.ids.filter(function (p) { return pass.handled.indexOf(p) < 0; });
    return { total: pass.ids.length, done: pass.ids.length - remaining.length, remaining: remaining, arrivals: pass.arrivals.slice(), ids: pass.ids.slice() };
  }
  // queue: what comes after fromId. Open pass panes after it (wrapping), then
  // arrivals. A detour outside the pass resumes at the pass pane you left.
  function queue(pass, fromId, resume) {
    var info = passInfo(pass), ids = info.ids, out = [];
    var anchor = ids.indexOf(fromId) >= 0 ? fromId : resume;
    if (anchor && anchor !== fromId && info.remaining.indexOf(anchor) >= 0) out.push(anchor);
    var start = Math.max(0, ids.indexOf(anchor) + 1);
    for (var k = 0; k < ids.length; k++) {
      var id = ids[(start + k) % ids.length];
      if (id !== fromId && info.remaining.indexOf(id) >= 0 && out.indexOf(id) < 0) out.push(id);
    }
    info.arrivals.forEach(function (a) { if (a !== fromId && out.indexOf(a) < 0) out.push(a); });
    return out;
  }
  function firstToOpen(pass) { var info = passInfo(pass); return info.remaining[0] || info.arrivals[0] || null; }

  // ---------- came back: a pane you sent to finished a turn since ----------
  // First rule: completion_seq moved past the one noted when you sent. Panes
  // without one (opencode, panes the gateway cannot read): the transcript's
  // last turn started after your send and has its answer. Without either:
  // herdr took the send and its own status is idle or done.
  function cameBack(item, sentRec, model) {
    if (!item || !item.delivery || item.viewed) return false;
    if (typeof item.completion_seq === 'number' && sentRec && typeof sentRec.completion_seq === 'number') return item.completion_seq > sentRec.completion_seq;
    if (model && model.last && sentRec && sentRec.at) {
      var tn = model.turns[model.turns.length - 1];
      return !!tn && model.last.state === 'answered' && tn.start !== null && tn.start >= sentRec.at - 60000;
    }
    var st = statusOf(item);
    return !!item.delivery.acknowledged && (st === 'idle' || st === 'done');
  }

  // ---------- the one message a pane gets ----------
  // Your reply first, then the row comments through herdrModel.composeMessage:
  // each one's quote (the selection, else the row's first line; at most 6
  // lines, cut at 200 characters) and "On your … : body", numbered in line
  // order under "N review comments:" when there are several.
  function compose(list, reply) {
    var sorted = (list || []).slice().sort(function (a, b) {
      return (a.line || 0) - (b.line || 0) || (a.ls || 0) - (b.ls || 0) || (a.at || 0) - (b.at || 0);
    });
    var comments = sorted.map(function (c, i) {
      return { start_line: i + 1, quote: c.quote || c.first || '', body: c.on + ': ' + c.body };
    });
    // no document (null): a comment with nothing to quote quotes nothing
    var body = comments.length ? HM.composeMessage(comments, null) : '';
    return [String(reply || '').trim(), body].filter(Boolean).join('\n\n');
  }
  function receiptTo(item) { return item.machine + ' ' + item.pane_id; }
  function previewTitle(item) { return 'Send to ' + receiptTo(item); }
  // whyNoTranscript: the line a pane without a transcript view shows.
  function whyNoTranscript(item, machine) {
    if (machine && (machine.state === 'unreachable' || machine.state === 'error')) {
      return item.machine + ' is ' + machine.state + (machine.error ? ' (' + machine.error + ')' : '') + '. This is what the last sweep read.';
    }
    if (LOCAL_READERS[item.agent] && item.machine !== 'local') return item.agent.charAt(0).toUpperCase() + item.agent.slice(1) + ' transcripts are read on this Mac only so far. This is what the sweep read from its screen.';
    if (LOCAL_READERS[item.agent] && !item.transcript_ref) return 'herdr names no session for this ' + item.agent + ' pane, so its transcript cannot be found. This is what the sweep read from its screen.';
    if (item.agent !== 'claude') return 'No ' + item.agent + ' transcript reader yet. This is what the sweep read from its screen.';
    return '';
  }
  // Codex, pi, opencode, grok and cursor panes are read on this Mac only (no
  // box reader yet); all but Codex only when herdr named their session
  // (transcript_ref)
  var LOCAL_READERS = { codex: 'any', pi: 'ref', opencode: 'ref', grok: 'ref', cursor: 'ref' };
  function wantsTranscript(item) {
    if (!item) return false;
    if (item.agent === 'claude') return true;
    var how = LOCAL_READERS[item.agent];
    return !!how && item.machine === 'local' && (how === 'any' || !!item.transcript_ref);
  }
  // herdrName: what you typed, in the form herdr accepts for an agent name
  // (^[a-z][a-z0-9_-]{0,31}$): accents to plain letters, lowercase, spaces to
  // "-", anything else dropped, one "-" at a time, starting at the first
  // letter, cut at 32, no "-" or "_" at the end. '' when nothing is left.
  function herdrName(s) {
    return String(s || '').normalize('NFKD').replace(/[\u0300-\u036f]/g, '').toLowerCase().trim()
      .replace(/\s+/g, '-').replace(/[^a-z0-9_-]/g, '').replace(/-{2,}/g, '-').replace(/^[^a-z]+/, '')
      .slice(0, 32).replace(/[-_]+$/, '');
  }

  var api = {
    stateOf: stateOf, statusOf: statusOf, STATE_LABEL: STATE_LABEL, needsAttention: needsAttention, paneName: paneName, stripOrder: stripOrder, agentMark: agentMark, findPanes: findPanes,
    startPass: startPass, noteStates: noteStates, markHandled: markHandled, passInfo: passInfo, queue: queue, firstToOpen: firstToOpen,
    cameBack: cameBack, compose: compose, receiptTo: receiptTo, previewTitle: previewTitle, whyNoTranscript: whyNoTranscript, wantsTranscript: wantsTranscript, herdrName: herdrName,
  };
  if (typeof window !== 'undefined') {
    window.crit = window.crit || {};
    window.crit.herdrPageModel = api;
  }
  if (typeof module === 'object' && module.exports) module.exports = api;
})();
