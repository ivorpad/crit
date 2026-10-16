// herdr-transcript-cursor.js: turns a cursor-agent transcript
// (~/.cursor/projects/<cwd>/agent-transcripts/<chat>/<chat>.jsonl, the chat
// herdr's cursor integration names, as GET /api/herdr/transcript sends it)
// into the same rows and turns herdr-transcript-parse.js makes from a Claude
// transcript, so the view, comments and Finish review work unchanged. A user
// line is a prompt: the words inside <user_query>, its time from <timestamp>.
// An assistant line is text and tool_use blocks. A turn_ended line closes the
// turn: "aborted" is your interrupt, "error" an error mark. cursor-agent keeps
// no tool output in this file, so a call says so instead of showing one, and
// the only times are the prompts'. Tool names and keys go through
// toolFromAgent (Shell -> Bash, StrReplace -> Edit). Pure: no DOM.
// Mounted on window.crit.herdrTranscriptCursor.
// Dependencies: window.crit.herdrTranscriptRules, window.crit.herdrTranscriptParse
// (require() in Node).
(function () {
  'use strict';
  var node = typeof module === 'object' && module.exports;
  var R = node ? require('./herdr-transcript-rules.js') : window.crit.herdrTranscriptRules;
  var TP = node ? require('./herdr-transcript-parse.js') : window.crit.herdrTranscriptParse;
  var isObj = R.isObj, clip = R.clip, firstLine = R.firstLine, bump = R.bump;
  var NO_OUTPUT = 'cursor-agent keeps no tool output in its transcript.';
  var MONTHS = { Jan: 0, Feb: 1, Mar: 2, Apr: 3, May: 4, Jun: 5, Jul: 6, Aug: 7, Sep: 8, Oct: 9, Nov: 10, Dec: 11 };

  function textOf(c) {
    if (typeof c === 'string') return c;
    return (Array.isArray(c) ? c : []).map(function (b) { return isObj(b) && b.type === 'text' && typeof b.text === 'string' ? b.text : ''; }).filter(Boolean).join('\n\n');
  }
  // "<timestamp>Friday, Oct 16, 2026, 1:16 PM (UTC+2)</timestamp>" as ms
  function stampTime(s) {
    var m = /<timestamp>\s*(?:\w+,\s*)?([A-Z][a-z]{2})\w*\s+(\d{1,2}),\s*(\d{4}),\s*(\d{1,2}):(\d{2})\s*(AM|PM)\s*\(UTC([+-])(\d{1,2})(?::?(\d{2}))?\)\s*<\/timestamp>/.exec(s);
    if (!m || MONTHS[m[1]] === undefined) return null;
    var h = (+m[4] % 12) + (m[6] === 'PM' ? 12 : 0), off = (m[7] === '-' ? -1 : 1) * (+m[8] * 60 + (+m[9] || 0));
    return Date.UTC(+m[3], MONTHS[m[1]], +m[2], h, +m[5]) - off * 60000;
  }
  function query(s) {
    var m = /<user_query>\s*([\s\S]*?)\s*<\/user_query>/.exec(s);
    return (m ? m[1] : s.replace(/<timestamp>[\s\S]*?<\/timestamp>/, '')).trim();
  }
  function errText(e) { return isObj(e) ? String(e.message || e.error || JSON.stringify(e)) : String(e || ''); }
  function clock() { return (typeof performance !== 'undefined' ? performance : Date).now(); }

  function parseSession(text) {
    var started = clock();
    var raw = String(text || '').split('\n');
    var rows = [], drawn = {};
    var ledger = { parsed: 0, malformed: 0, malformedLines: [], drawnLines: 0, notDrawn: 0, rows: 0, notDrawnByKey: {}, unplaced: {}, joinedHere: {}, bytesByKey: {}, supersededThinking: 0, orphanResults: 0 };
    var info = { agent: 'cursor', sessionId: null, cwd: null, version: null, aiTitle: null, customTitle: null, first: null, last: null, bytes: text.length };
    var turnOpen = false;

    function add(r) { r.i = rows.length; rows.push(r); drawn[r.line] = 1; return r; }
    function base(lineNo, bi, kind, t) { return { kind: kind, line: lineNo, uuid: '', key: 'L' + lineNo + (bi > 0 ? '.' + bi : ''), t: t }; }
    function mark(lineNo, tag, full, oneLine) {
      var r = base(lineNo, 0, 'mark', null);
      r.tag = tag;
      r.full = String(full || '');
      r.text = clip(oneLine !== undefined && oneLine !== null ? oneLine : r.full, 200);
      return add(r);
    }

    for (var n = 0; n < raw.length; n++) {
      var s = raw[n];
      if (!s || !s.trim()) continue;
      var ln = n + 1;
      ledger.parsed++;
      var ev;
      try { ev = JSON.parse(s); } catch (e) { ledger.malformed++; ledger.malformedLines.push(ln); continue; }
      if (!isObj(ev) || (typeof ev.role !== 'string' && typeof ev.type !== 'string')) { ledger.malformed++; ledger.malformedLines.push(ln); continue; }
      bump(ledger.bytesByKey, ev.role || ev.type, s.length);
      if (ev.type === 'turn_ended') {
        turnOpen = false;
        if (ev.status === 'aborted') mark(ln, 'interrupt', 'You stopped the turn', 'You stopped the turn');
        else if (ev.status === 'error') { var why = errText(ev.error) || 'cursor-agent stopped on an error'; mark(ln, 'error', why, 'Error: ' + firstLine(why)); }
        else drawn[ln] = 1;
        continue;
      }
      var content = isObj(ev.message) ? ev.message.content : null;
      if (ev.role === 'user') {
        var all = textOf(content), said = query(all), t = stampTime(all);
        if (t !== null) { if (info.first === null || t < info.first) info.first = t; if (info.last === null || t > info.last) info.last = t; }
        if (!said) continue;
        var p = base(ln, 0, 'prompt', t);
        p.text = said; p.label = R.plainLabel(said) || said; p.typed = true; p.pasted = R.isPasted(said); p.images = [];
        add(p);
        turnOpen = true;
      } else if (ev.role === 'assistant') {
        (Array.isArray(content) ? content : []).forEach(function (b, bi) {
          if (!isObj(b)) return;
          var r;
          if (b.type === 'text' && typeof b.text === 'string' && b.text.trim()) {
            r = base(ln, bi, 'message', null);
            r.text = b.text; r.msgId = '';
            add(r);
          } else if (b.type === 'tool_use') {
            var tf = R.toolFromAgent(b.name, isObj(b.input) ? b.input : {});
            r = base(ln, bi, 'tool', null);
            r.name = tf.name; r.label = R.toolLabel(tf.name); r.toolId = ''; r.input = tf.input; r.cwd = '';
            r.headline = R.toolHeadline(tf.name, tf.input, ''); r.server = false; r.status = 'nooutput'; r.result = null; r.why = NO_OUTPUT; r.images = [];
            add(r);
          } else if (b.type !== 'text') bump(ledger.unplaced, 'block:' + b.type);
        });
      } else bump(ledger.unplaced, ev.role || ev.type);
    }

    raw.forEach(function (line, k) {
      if (!line || !line.trim()) return;
      if (drawn[k + 1]) ledger.drawnLines++;
      else ledger.notDrawn++;
    });
    // a turn with no turn_ended yet is still running: its last call has no
    // result yet, and text after it is an update, not the answer
    var lastRow = rows[rows.length - 1];
    if (turnOpen && lastRow && lastRow.kind === 'tool') { lastRow.status = 'pending'; lastRow.why = ''; }
    ledger.rows = rows.length;
    var turns = TP.buildTurns(rows);
    // only prompts carry a time, so how long a turn took is not known
    turns.forEach(function (tn) { tn.ms = null; });
    var model = { info: info, rows: rows, turns: turns, ledger: ledger };
    model.last = TP.analyzeLast(model);
    if (turnOpen && model.last.state === 'answered') {
      model.last.state = 'working';
      model.last.fold = model.last.fold.concat(model.last.answer);
      model.last.answer = [];
    }
    model.parseMs = clock() - started;
    return model;
  }

  var api = { parseSession: parseSession, stampTime: stampTime };
  if (typeof window !== 'undefined') {
    window.crit = window.crit || {};
    window.crit.herdrTranscriptCursor = api;
  }
  if (typeof module === 'object' && module.exports) module.exports = api;
})();
