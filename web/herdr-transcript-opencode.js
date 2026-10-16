// herdr-transcript-opencode.js: turns an opencode session (read out of
// opencode's database by GET /api/herdr/transcript: a session line, then every
// message and part in the order opencode created them) into the same rows and
// turns herdr-transcript-parse.js makes from a Claude transcript, so the view,
// comments and Finish review work unchanged. A user message is a prompt (its
// text parts, minus the ones opencode adds itself); an assistant message's
// parts are its rows: reasoning, text, and tool calls with their state. A
// message that failed is an error mark, or your interrupt when you stopped
// it. An assistant message with no completed time is a turn still running.
// Tool names and keys go through toolFromAgent (bash -> Bash, filePath ->
// file_path). Pure: no DOM.
// Mounted on window.crit.herdrTranscriptOpencode.
// Dependencies: window.crit.herdrTranscriptRules, window.crit.herdrTranscriptParse
// (require() in Node).
(function () {
  'use strict';
  var node = typeof module === 'object' && module.exports;
  var R = node ? require('./herdr-transcript-rules.js') : window.crit.herdrTranscriptRules;
  var TP = node ? require('./herdr-transcript-parse.js') : window.crit.herdrTranscriptParse;
  var isObj = R.isObj, clip = R.clip, firstLine = R.firstLine, bump = R.bump;

  function ms(v) { return typeof v === 'number' && v > 0 ? v : null; }
  function clock() { return (typeof performance !== 'undefined' ? performance : Date).now(); }
  function errorText(e) {
    if (!isObj(e)) return String(e || '');
    var d = isObj(e.data) ? e.data : {};
    return String(d.message || e.message || e.name || 'error');
  }

  function parseSession(text) {
    var started = clock();
    var raw = String(text || '').split('\n');
    var rows = [], drawn = {}, msgs = {}, order = [];
    var ledger = { parsed: 0, malformed: 0, malformedLines: [], drawnLines: 0, notDrawn: 0, rows: 0, notDrawnByKey: {}, unplaced: {}, joinedHere: {}, bytesByKey: {}, supersededThinking: 0, orphanResults: 0 };
    var info = { agent: 'opencode', sessionId: null, cwd: null, version: null, aiTitle: null, customTitle: null, first: null, last: null, bytes: text.length };
    var running = false;

    function add(r) { r.i = rows.length; rows.push(r); drawn[r.line] = 1; return r; }
    function base(lineNo, uuid, kind, t) { return { kind: kind, line: lineNo, uuid: uuid || '', key: 'L' + lineNo, t: t }; }
    function mark(lineNo, uuid, t, tag, full, oneLine) {
      var r = base(lineNo, uuid, 'mark', t);
      r.tag = tag;
      r.full = String(full || '');
      r.text = clip(oneLine !== undefined && oneLine !== null ? oneLine : r.full, 200);
      return add(r);
    }
    function seen(t) { if (t !== null) { if (info.first === null || t < info.first) info.first = t; if (info.last === null || t > info.last) info.last = t; } }

    // the lines, grouped: each message with its parts, in the order they came
    for (var n = 0; n < raw.length; n++) {
      var s = raw[n];
      if (!s || !s.trim()) continue;
      var ln = n + 1;
      ledger.parsed++;
      var ev;
      try { ev = JSON.parse(s); } catch (e) { ledger.malformed++; ledger.malformedLines.push(ln); continue; }
      if (!isObj(ev) || typeof ev.k !== 'string' || (ev.k !== 's' && !isObj(ev.data))) { ledger.malformed++; ledger.malformedLines.push(ln); continue; }
      bump(ledger.bytesByKey, ev.k === 'p' ? 'part:' + ev.data.type : ev.k === 'm' ? 'message:' + ev.data.role : 'session', s.length);
      if (ev.k === 's') {
        info.sessionId = ev.id || null;
        info.cwd = ev.dir || null;
        info.version = ev.version ? 'opencode ' + ev.version : null;
        if (typeof ev.title === 'string' && ev.title.trim() && !/^New session - /.test(ev.title)) info.aiTitle = ev.title.trim();
        drawn[ln] = 1;
      } else if (ev.k === 'm') {
        if (!msgs[ev.id]) { msgs[ev.id] = { parts: [] }; order.push(ev.id); }
        msgs[ev.id].line = ln; msgs[ev.id].data = ev.data; msgs[ev.id].id = ev.id; msgs[ev.id].t = ms(ev.t);
      } else if (ev.k === 'p') {
        if (!msgs[ev.mid]) { msgs[ev.mid] = { parts: [] }; order.push(ev.mid); }
        msgs[ev.mid].parts.push({ line: ln, id: ev.id, t: ms(ev.t), data: ev.data });
      }
    }

    order.forEach(function (id) {
      var m = msgs[id], d = m.data;
      if (!d) { m.parts.forEach(function () { ledger.orphanResults++; }); return; }
      var t = (isObj(d.time) && ms(d.time.created)) || m.t;
      seen(t);
      if (d.role === 'user') {
        var said = m.parts.filter(function (p) { return p.data.type === 'text' && !p.data.synthetic && !p.data.ignored && typeof p.data.text === 'string'; })
          .map(function (p) { return p.data.text; }).join('\n\n').trim();
        var files = m.parts.filter(function (p) { return p.data.type === 'file'; }).length;
        if (!said && !files) return;
        var r = base(m.line, id, 'prompt', t);
        r.text = said + (files ? (said ? '\n' : '') + '[' + files + (files === 1 ? ' file]' : ' files]') : '');
        r.label = R.plainLabel(said) || '[file]';
        r.typed = true; r.pasted = R.isPasted(said); r.images = [];
        add(r);
        m.parts.forEach(function (p) { drawn[p.line] = 1; });
        return;
      }
      if (d.role !== 'assistant') { bump(ledger.unplaced, 'role:' + d.role); return; }
      if (isObj(d.path) && d.path.cwd && !info.cwd) info.cwd = d.path.cwd;
      m.parts.forEach(function (p) { part(p); });
      if (d.error) {
        var why = errorText(d.error);
        if (d.error.name === 'MessageAbortedError') mark(m.line, id, t, 'interrupt', why, 'You stopped the turn');
        else mark(m.line, id, ms(d.time && d.time.completed) || t, 'error', why, 'Error: ' + firstLine(why));
      }
      running = !(isObj(d.time) && ms(d.time.completed)) && !d.error;
    });

    function part(p) {
      var d = p.data, r;
      switch (d.type) {
        case 'text':
          if (d.synthetic || typeof d.text !== 'string' || !d.text.trim()) return;
          r = base(p.line, p.id, 'message', (isObj(d.time) && ms(d.time.end)) || p.t);
          r.text = d.text; r.msgId = p.id;
          add(r);
          return;
        case 'reasoning':
          if (typeof d.text !== 'string' || !d.text.trim()) return;
          r = base(p.line, p.id, 'thinking', (isObj(d.time) && ms(d.time.start)) || p.t);
          r.text = d.text.trim(); r.stored = true; r.redacted = false; r.req = '';
          r.ms = isObj(d.time) && ms(d.time.start) && ms(d.time.end) ? d.time.end - d.time.start : null;
          add(r);
          return;
        case 'tool':
          var st = isObj(d.state) ? d.state : {}, tf = R.toolFromAgent(d.tool, st.input);
          var tm = isObj(st.time) ? st.time : {};
          r = base(p.line, p.id, 'tool', ms(tm.start) || p.t);
          r.name = tf.name; r.label = R.toolLabel(tf.name); r.toolId = d.callID || ''; r.input = tf.input; r.cwd = info.cwd || '';
          r.headline = R.toolHeadline(tf.name, tf.input, r.cwd) || (typeof st.title === 'string' ? st.title : '');
          r.server = false; r.images = [];
          if (st.status === 'completed') { r.status = 'ok'; r.result = typeof st.output === 'string' ? st.output || '[no output]' : JSON.stringify(st.output); }
          else if (st.status === 'error') { r.status = 'error'; r.result = errorText(st.error) || '[tool error, no output]'; }
          else { r.status = 'pending'; r.result = null; }
          r.resultLine = p.line; r.resultUuid = p.id; r.tEnd = ms(tm.end);
          add(r);
          return;
        case 'subtask':
          mark(p.line, p.id, p.t, 'task', d.prompt || d.description || '', 'Sub-agent: ' + (d.description || firstLine(d.prompt) || 'task'));
          return;
        case 'compaction':
          mark(p.line, p.id, p.t, 'compact', 'opencode compacted the conversation', 'Context compacted');
          return;
        case 'step-start': case 'step-finish': case 'patch': case 'snapshot': case 'file': case 'agent': case 'retry':
          return;
        default:
          bump(ledger.unplaced, 'part:' + d.type);
      }
    }

    raw.forEach(function (line, k) {
      if (!line || !line.trim()) return;
      if (drawn[k + 1]) ledger.drawnLines++;
      else ledger.notDrawn++;
    });
    ledger.rows = rows.length;
    var turns = TP.buildTurns(rows);
    var model = { info: info, rows: rows, turns: turns, ledger: ledger };
    model.last = TP.analyzeLast(model);
    // a reply with no completed time is still streaming: an answer so far is
    // an update, not the answer
    if (running && model.last.state === 'answered') {
      model.last.state = 'working';
      model.last.fold = model.last.fold.concat(model.last.answer);
      model.last.answer = [];
    }
    model.parseMs = clock() - started;
    return model;
  }

  var api = { parseSession: parseSession };
  if (typeof window !== 'undefined') {
    window.crit = window.crit || {};
    window.crit.herdrTranscriptOpencode = api;
  }
  if (typeof module === 'object' && module.exports) module.exports = api;
})();
