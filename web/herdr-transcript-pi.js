// herdr-transcript-pi.js: turns a pi session file (~/.pi/agent/sessions/<cwd>/
// <time>_<id>.jsonl, the file herdr's pi integration names, as GET
// /api/herdr/transcript sends it) into the same rows and turns
// herdr-transcript-parse.js makes from a Claude transcript, so the view,
// comments and Finish review work unchanged. pi writes one message line per
// prompt, per assistant reply (text, thinking and toolCall blocks) and per tool
// result, joined to its call by toolCallId. A reply that stopped as "aborted"
// is your interrupt and one that stopped as "error" an error mark; a
// compaction is a compact mark; a custom_message pi shows is a notice. Tool
// names and keys go through toolFromAgent (bash -> Bash, path -> file_path).
// Pure: no DOM.
// Mounted on window.crit.herdrTranscriptPi.
// Dependencies: window.crit.herdrTranscriptRules, window.crit.herdrTranscriptParse
// (require() in Node).
(function () {
  'use strict';
  var node = typeof module === 'object' && module.exports;
  var R = node ? require('./herdr-transcript-rules.js') : window.crit.herdrTranscriptRules;
  var TP = node ? require('./herdr-transcript-parse.js') : window.crit.herdrTranscriptParse;
  var isObj = R.isObj, timeOf = R.timeOf, clip = R.clip, firstLine = R.firstLine, bump = R.bump;

  function blockText(content) {
    if (typeof content === 'string') return content;
    return (Array.isArray(content) ? content : []).map(function (b) { return isObj(b) && b.type === 'text' && typeof b.text === 'string' ? b.text : ''; }).filter(Boolean).join('\n\n');
  }
  function imageCount(content) { return (Array.isArray(content) ? content : []).filter(function (b) { return isObj(b) && b.type === 'image'; }).length; }
  function clock() { return (typeof performance !== 'undefined' ? performance : Date).now(); }

  function parseSession(text) {
    var started = clock();
    var raw = String(text || '').split('\n');
    var rows = [], drawn = {}, toolById = {};
    var ledger = { parsed: 0, malformed: 0, malformedLines: [], drawnLines: 0, notDrawn: 0, rows: 0, notDrawnByKey: {}, unplaced: {}, joinedHere: {}, bytesByKey: {}, supersededThinking: 0, orphanResults: 0 };
    var info = { agent: 'pi', sessionId: null, cwd: null, version: null, aiTitle: null, customTitle: null, first: null, last: null, bytes: text.length };

    function add(r) { r.i = rows.length; rows.push(r); drawn[r.line] = 1; return r; }
    function base(lineNo, uuid, bi, kind, t) { return { kind: kind, line: lineNo, uuid: uuid || '', key: 'L' + lineNo + (bi > 0 ? '.' + bi : ''), t: t }; }
    function mark(lineNo, uuid, bi, t, tag, full, oneLine) {
      var r = base(lineNo, uuid, bi, 'mark', t);
      r.tag = tag;
      r.full = String(full || '');
      r.text = clip(oneLine !== undefined && oneLine !== null ? oneLine : r.full, 200);
      return add(r);
    }

    function message(lineNo, ev, t) {
      var m = ev.message, r;
      if (m.role === 'user') {
        var said = blockText(m.content).trim(), pics = imageCount(m.content);
        if (!said && !pics) return;
        r = base(lineNo, ev.id, 0, 'prompt', t);
        r.text = said + (pics ? (said ? '\n' : '') + '[' + pics + (pics === 1 ? ' image]' : ' images]') : '');
        r.label = R.plainLabel(said) || '[image]';
        r.typed = true; r.pasted = R.isPasted(said); r.images = [];
        add(r);
      } else if (m.role === 'assistant') {
        var content = Array.isArray(m.content) ? m.content : [];
        content.forEach(function (b, bi) {
          if (!isObj(b)) return;
          if (b.type === 'text' && typeof b.text === 'string' && b.text.trim()) {
            r = base(lineNo, ev.id, bi, 'message', t);
            r.text = b.text; r.msgId = ev.id || '';
            add(r);
          } else if (b.type === 'thinking' && typeof b.thinking === 'string' && b.thinking.trim()) {
            r = base(lineNo, ev.id, bi, 'thinking', t);
            r.text = b.thinking.trim(); r.stored = true; r.redacted = false; r.req = ''; r.ms = null;
            add(r);
          } else if (b.type === 'toolCall') {
            var tf = R.toolFromAgent(b.name, b.arguments);
            r = base(lineNo, ev.id, bi, 'tool', t);
            r.name = tf.name; r.label = R.toolLabel(tf.name); r.toolId = b.id || ''; r.input = tf.input; r.cwd = info.cwd || '';
            r.headline = R.toolHeadline(tf.name, tf.input, r.cwd); r.server = false; r.status = 'pending'; r.result = null; r.images = [];
            if (b.id) toolById[b.id] = r;
            add(r);
          } else if (b.type !== 'thinking' && b.type !== 'text') bump(ledger.unplaced, 'block:' + b.type);
        });
        var why = typeof m.errorMessage === 'string' ? m.errorMessage : '';
        if (m.stopReason === 'aborted') mark(lineNo, ev.id, content.length + 1, t, 'interrupt', why || 'You stopped the turn', 'You stopped the turn');
        else if (m.stopReason === 'error') mark(lineNo, ev.id, content.length + 1, t, 'error', why || 'pi stopped on an error', 'Error: ' + (firstLine(why) || 'pi stopped on an error'));
      } else if (m.role === 'toolResult') {
        var out = blockText(m.content), n = imageCount(m.content);
        if (n) out += (out ? '\n' : '') + '[' + n + (n === 1 ? ' image]' : ' images]');
        var host = toolById[m.toolCallId];
        if (!host) { ledger.orphanResults++; mark(lineNo, ev.id, 0, t, 'result', out, 'Tool result with no call: ' + firstLine(out)); return; }
        host.result = out || '[no output]';
        host.status = m.isError === true ? 'error' : 'ok';
        host.resultLine = lineNo; host.resultUuid = ev.id || ''; host.tEnd = t;
        drawn[lineNo] = 1;
      }
      // role system is pi's system prompt: not a row
    }

    for (var n = 0; n < raw.length; n++) {
      var s = raw[n];
      if (!s || !s.trim()) continue;
      var ln = n + 1;
      ledger.parsed++;
      var ev;
      try { ev = JSON.parse(s); } catch (e) { ledger.malformed++; ledger.malformedLines.push(ln); continue; }
      if (!isObj(ev) || typeof ev.type !== 'string' || (ev.type === 'message' && !isObj(ev.message))) { ledger.malformed++; ledger.malformedLines.push(ln); continue; }
      bump(ledger.bytesByKey, ev.type === 'message' ? 'message:' + ev.message.role : ev.type, s.length);
      var t = timeOf(ev);
      if (t !== null) { if (info.first === null || t < info.first) info.first = t; if (info.last === null || t > info.last) info.last = t; }
      if (ev.type === 'session') {
        info.sessionId = ev.id || null; info.cwd = ev.cwd || null; info.version = ev.version ? 'pi session v' + ev.version : null;
      } else if (ev.type === 'message') message(ln, ev, t);
      else if (ev.type === 'compaction') {
        mark(ln, ev.id, 0, t, 'compact', ev.summary || '', 'Context compacted' + (typeof ev.tokensBefore === 'number' ? ', at ' + ev.tokensBefore.toLocaleString('en-US') + ' tokens' : ''));
      } else if (ev.type === 'custom_message') {
        if (ev.display) { var said = blockText(ev.content); mark(ln, ev.id, 0, t, 'info', said, (ev.customType ? ev.customType + ': ' : '') + R.innerText(said)); }
      } else if (ev.type === 'session_info') {
        if (typeof ev.name === 'string' && ev.name.trim()) info.customTitle = ev.name.trim();
      } else bump(ledger.unplaced, ev.type);
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
    window.crit.herdrTranscriptPi = api;
  }
  if (typeof module === 'object' && module.exports) module.exports = api;
})();
