// herdr-transcript-grok.js: turns a grok session's chat history
// (~/.grok/sessions/<cwd>/<session>/chat_history.jsonl, the session herdr's
// grok integration names, as GET /api/herdr/transcript sends it) into the same
// rows and turns herdr-transcript-parse.js makes from a Claude transcript, so
// the view, comments and Finish review work unchanged. A user line with a
// prompt_index is a prompt you sent (its <user_query> wrapper dropped); the
// other user lines are context grok adds (workspace info, system reminders)
// and draw no row. An assistant line is its text plus its tool_calls
// (arguments as a JSON string); a tool_result joins its call by tool_call_id;
// a reasoning line with a summary is thinking.
// A prompt sent after you stopped a turn (prior_turn_interrupt) ends that turn
// with your interrupt. The file keeps no times. Tool names and keys go
// through toolFromAgent (read_file -> Read, run_terminal_command -> Bash).
// Pure: no DOM.
// Mounted on window.crit.herdrTranscriptGrok.
// Dependencies: window.crit.herdrTranscriptRules, window.crit.herdrTranscriptParse
// (require() in Node).
(function () {
  'use strict';
  var node = typeof module === 'object' && module.exports;
  var R = node ? require('./herdr-transcript-rules.js') : window.crit.herdrTranscriptRules;
  var TP = node ? require('./herdr-transcript-parse.js') : window.crit.herdrTranscriptParse;
  var isObj = R.isObj, clip = R.clip, firstLine = R.firstLine, bump = R.bump;

  function textOf(c) {
    if (typeof c === 'string') return c;
    return (Array.isArray(c) ? c : []).map(function (b) { return isObj(b) && typeof b.text === 'string' ? b.text : ''; }).filter(Boolean).join('\n\n');
  }
  function args(a) {
    if (isObj(a)) return a;
    try { var o = JSON.parse(a); return isObj(o) ? o : { arguments: String(a) }; } catch (e) { return { arguments: String(a || '') }; }
  }
  function clock() { return (typeof performance !== 'undefined' ? performance : Date).now(); }

  function parseSession(text) {
    var started = clock();
    var raw = String(text || '').split('\n');
    var rows = [], drawn = {}, toolById = {};
    var ledger = { parsed: 0, malformed: 0, malformedLines: [], drawnLines: 0, notDrawn: 0, rows: 0, notDrawnByKey: {}, unplaced: {}, joinedHere: {}, bytesByKey: {}, supersededThinking: 0, orphanResults: 0 };
    var info = { agent: 'grok', sessionId: null, cwd: null, version: null, aiTitle: null, customTitle: null, first: null, last: null, bytes: text.length };

    function add(r) { r.i = rows.length; rows.push(r); drawn[r.line] = 1; return r; }
    function base(lineNo, uuid, bi, kind) { return { kind: kind, line: lineNo, uuid: uuid || '', key: 'L' + lineNo + (bi > 0 ? '.' + bi : ''), t: null }; }
    function mark(lineNo, bi, tag, full, oneLine) {
      var r = base(lineNo, '', bi, 'mark');
      r.tag = tag;
      r.full = String(full || '');
      r.text = clip(oneLine !== undefined && oneLine !== null ? oneLine : r.full, 200);
      return add(r);
    }

    for (var n = 0; n < raw.length; n++) {
      var s = raw[n];
      if (!s || !s.trim()) continue;
      var ln = n + 1, r;
      ledger.parsed++;
      var ev;
      try { ev = JSON.parse(s); } catch (e) { ledger.malformed++; ledger.malformedLines.push(ln); continue; }
      if (!isObj(ev) || typeof ev.type !== 'string') { ledger.malformed++; ledger.malformedLines.push(ln); continue; }
      bump(ledger.bytesByKey, ev.type, s.length);
      if (ev.type === 'user') {
        // context grok adds itself has no prompt_index
        if (typeof ev.prompt_index !== 'number') continue;
        if (ev.prior_turn_interrupt) mark(ln, 1, 'interrupt', 'You stopped the turn (' + String(ev.prior_turn_interrupt).replace(/_/g, ' ') + ')', 'You stopped the turn');
        // grok wraps what you typed in <user_query>
        var said = textOf(ev.content).replace(/^\s*<user_query>\s*([\s\S]*?)\s*<\/user_query>\s*$/, '$1').trim();
        if (!said) continue;
        r = base(ln, '', 0, 'prompt');
        r.text = said; r.label = R.plainLabel(said) || said; r.typed = true; r.pasted = R.isPasted(said); r.images = [];
        add(r);
      } else if (ev.type === 'reasoning') {
        var thought = textOf(ev.summary).trim();
        if (!thought) continue;
        r = base(ln, ev.id, 0, 'thinking');
        r.text = thought; r.stored = true; r.redacted = false; r.req = ''; r.ms = null;
        add(r);
      } else if (ev.type === 'assistant') {
        var says = textOf(ev.content);
        if (says.trim()) {
          r = base(ln, '', 0, 'message');
          r.text = says; r.msgId = '';
          add(r);
        }
        (Array.isArray(ev.tool_calls) ? ev.tool_calls : []).forEach(function (tc, k) {
          if (!isObj(tc)) return;
          var tf = R.toolFromAgent(tc.name, args(tc.arguments));
          var t = base(ln, tc.id, k + 1, 'tool');
          t.name = tf.name; t.label = R.toolLabel(tf.name); t.toolId = tc.id || ''; t.input = tf.input; t.cwd = '';
          t.headline = R.toolHeadline(tf.name, tf.input, ''); t.server = false; t.status = 'pending'; t.result = null; t.images = [];
          if (tc.id) toolById[tc.id] = t;
          add(t);
        });
      } else if (ev.type === 'tool_result') {
        var out = textOf(ev.content), host = toolById[ev.tool_call_id];
        if (!host) { ledger.orphanResults++; mark(ln, 0, 'result', out, 'Tool result with no call: ' + firstLine(out)); continue; }
        host.result = out || '[no output]'; host.status = 'ok'; host.resultLine = ln; host.resultUuid = '';
        drawn[ln] = 1;
      } else if (ev.type !== 'system') bump(ledger.unplaced, ev.type);
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
    model.parseMs = clock() - started;
    return model;
  }

  var api = { parseSession: parseSession };
  if (typeof window !== 'undefined') {
    window.crit = window.crit || {};
    window.crit.herdrTranscriptGrok = api;
  }
  if (typeof module === 'object' && module.exports) module.exports = api;
})();
