// herdr-transcript-parse.js: turns a Claude Code JSONL transcript into rows
// and turns for the /herdr page, by the rules in herdr-transcript-rules.js.
// A tool call and its result are one row; every prompt you typed opens a
// turn; the last turn says where its final answer starts. Pure: no DOM.
// Mounted on window.crit.herdrTranscriptParse.
// Dependencies: window.crit.herdrTranscriptRules (require() in Node).
(function () {
  'use strict';
  var R = typeof module === 'object' && module.exports ? require('./herdr-transcript-rules.js') : window.crit.herdrTranscriptRules;
  var TOP_LEVEL = R.TOP_LEVEL, SYSTEM_SUBTYPES = R.SYSTEM_SUBTYPES, ATTACHMENTS = R.ATTACHMENTS, BLOCKS = R.BLOCKS, BLOCKS_JOINED_HERE = R.BLOCKS_JOINED_HERE;
  var stripPromptEnvelopes = R.stripPromptEnvelopes, userLineKind = R.userLineKind, innerText = R.innerText;
  var isObj = R.isObj, timeOf = R.timeOf, clip = R.clip, firstLine = R.firstLine, bump = R.bump;
  var textOf = R.textOf, describeToolUseResult = R.describeToolUseResult, toolHeadline = R.toolHeadline, toolLabel = R.toolLabel;

  // ---------- pictures ----------
  // A picture the page asks GET /api/herdr/image for: a base64 png, jpeg, gif
  // or webp block on a line with a uuid, named by the line and the block's
  // place in it ("1", or "0.0" inside a tool result). The text the page gets
  // cuts the data, so it is never decoded here.
  var IMAGE_MEDIA = { 'image/png': 1, 'image/jpeg': 1, 'image/gif': 1, 'image/webp': 1 };
  function imageRef(o, b, block) {
    var src = isObj(b) && b.type === 'image' && isObj(b.source) ? b.source : null;
    if (!src || src.type !== 'base64' || !IMAGE_MEDIA[src.media_type] || typeof o.uuid !== 'string' || !o.uuid) return null;
    return { uuid: o.uuid, block: block, media: src.media_type };
  }
  function imageRefs(o, content, prefix) {
    return Array.isArray(content) ? content.map(function (b, j) { return imageRef(o, b, prefix + j); }).filter(Boolean) : [];
  }
  // the size a Read reports for the picture it returned, shown size first
  function imageDims(o) {
    var f = isObj(o.toolUseResult) && isObj(o.toolUseResult.file) ? o.toolUseResult.file : null;
    var d = f && isObj(f.dimensions) ? f.dimensions : null;
    if (!d) return null;
    var w = d.displayWidth || d.originalWidth, h = d.displayHeight || d.originalHeight;
    return typeof w === 'number' && typeof h === 'number' && w > 0 && h > 0 ? { w: Math.round(w), h: Math.round(h) } : null;
  }

  // ---------- the parse ----------
  function parseTranscript(text) {
    var started = (typeof performance !== 'undefined' ? performance : Date).now();
    var raw = String(text || '').split('\n');
    var rows = [];
    var toolById = {};
    var lineRows = {};        // line number -> rows this line drew (or joined)
    var lineKeys = [];        // [line number, ledger key] for every well-formed line
    var ledger = { parsed: 0, malformed: 0, malformedLines: [], drawnLines: 0, notDrawn: 0, rows: 0, notDrawnByKey: {}, unplaced: {}, joinedHere: {}, bytesByKey: {}, supersededThinking: 0, orphanResults: 0 };
    var info = { sessionId: null, cwd: null, version: null, aiTitle: null, customTitle: null, first: null, last: null, bytes: text.length };

    function addRow(r) { r.i = rows.length; rows.push(r); bump(lineRows, r.line); return r; }
    function base(o, lineNo, bi, kind) {
      return { kind: kind, line: lineNo, uuid: typeof o.uuid === 'string' ? o.uuid : '', key: 'L' + lineNo + (bi > 0 ? '.' + bi : ''), t: timeOf(o) };
    }
    function mark(o, lineNo, bi, tag, full, oneLine) {
      var r = base(o, lineNo, bi, 'mark');
      r.tag = tag;
      r.full = String(full || '');
      r.text = clip(oneLine !== null && oneLine !== undefined ? oneLine : r.full, 200);
      return addRow(r);
    }

    for (var n = 0; n < raw.length; n++) {
      var s = raw[n];
      if (!s || !s.trim()) continue;
      var ln = n + 1;
      ledger.parsed++;
      var ev;
      try { ev = JSON.parse(s); } catch (e) { ledger.malformed++; ledger.malformedLines.push(ln); continue; }
      if (!isObj(ev) || typeof ev.type !== 'string' || ((ev.type === 'user' || ev.type === 'assistant') && !isObj(ev.message))) {
        ledger.malformed++; ledger.malformedLines.push(ln); continue;
      }
      var type = ev.type;
      var t = timeOf(ev);
      if (t !== null) { if (info.first === null || t < info.first) info.first = t; if (info.last === null || t > info.last) info.last = t; }
      if (!info.sessionId && typeof ev.sessionId === 'string') info.sessionId = ev.sessionId;
      if (!info.cwd && typeof ev.cwd === 'string') info.cwd = ev.cwd;
      if (typeof ev.version === 'string') info.version = ev.version;
      var key = type;
      if (type === 'system') key = 'system:' + (ev.subtype || '?');
      else if (type === 'attachment') key = 'attachment:' + ((isObj(ev.attachment) && ev.attachment.type) || '?');
      lineKeys.push([ln, key]);
      bump(ledger.bytesByKey, key, s.length);

      if (type === 'user') parseUser(ev, ln);
      else if (type === 'assistant') parseAssistant(ev, ln);
      else if (type === 'system') parseSystem(ev, ln);
      else if (type === 'attachment') parseAttachment(ev, ln);
      else {
        if (type === 'ai-title' && typeof ev.aiTitle === 'string') info.aiTitle = ev.aiTitle;
        if (type === 'custom-title' && typeof ev.customTitle === 'string') info.customTitle = ev.customTitle;
        if (!TOP_LEVEL[type]) bump(ledger.unplaced, type);
      }
    }

    function parseUser(o, lineNo) {
      var content = o.message.content;
      var hasResult = false;
      if (Array.isArray(content)) {
        content.forEach(function (b, bi) {
          if (!isObj(b)) return;
          if (b.type === 'tool_result') { hasResult = true; joinResult(o, lineNo, b, bi); }
          else if (!BLOCKS[b.type] && !BLOCKS_JOINED_HERE[b.type]) bump(ledger.unplaced, 'block:' + b.type);
        });
      }
      // A picture the page draws leaves no "[image]" placeholder in the text.
      var pics = imageRefs(o, content, '');
      var direct = (typeof content === 'string' ? content : textOf((content || []).filter(function (b, j) {
        return isObj(b) && b.type !== 'tool_result' && !(b.type === 'image' && imageRef(o, b, String(j)));
      }))).trim();
      if (!direct && !pics.length) {
        if (!hasResult && o.toolUseResult !== undefined) {
          var orphan = describeToolUseResult(o.toolUseResult);
          if (orphan) { ledger.orphanResults++; mark(o, lineNo, 0, 'result', orphan, 'Tool result with no call: ' + firstLine(orphan)); }
        }
        return;
      }
      if (o.isCompactSummary) {
        mark(o, lineNo, 0, 'compact', direct, 'Summary the agent kept for the rest of the session, ' + direct.length.toLocaleString('en-US') + ' chars');
        return;
      }
      var kind = userLineKind(direct);
      if (o.isMeta) {
        mark(o, lineNo, 0, 'meta', direct, innerText(direct) || direct);
        return;
      }
      if (kind === 'interrupt') { mark(o, lineNo, 0, 'interrupt', direct, 'You stopped the turn'); return; }
      if (kind === 'notification') {
        var sum = /<summary>([\s\S]*?)<\/summary>/.exec(direct);
        var st = /<status>([\s\S]*?)<\/status>/.exec(direct);
        mark(o, lineNo, 0, 'task', innerText(direct), sum ? sum[1].trim() : 'Background task ' + (st ? st[1].trim() : 'note'));
        return;
      }
      if (kind === 'command-output') {
        var onlyReminder = /^(\s*<system-reminder>[\s\S]*?<\/system-reminder>\s*)+$/.test(direct);
        mark(o, lineNo, 0, onlyReminder ? 'reminder' : 'command', innerText(direct), (onlyReminder ? '' : 'Output: ') + innerText(direct));
        return;
      }
      // Newer Claude Code says who wrote a user line (origin, promptSource,
      // turnOrigin). Where it does, only a human line is a prompt. The Reader's
      // text rules above still decide for older transcripts.
      var human = o.promptSource === 'typed' || o.turnOrigin === 'human' || (isObj(o.origin) && o.origin.kind === 'human');
      var says = o.promptSource !== undefined || o.turnOrigin !== undefined || o.origin !== undefined;
      if (!human && /<(teammate-message|cross-session-message)\b/.test(direct)) {
        var from = /\b(?:teammate_id|from)="([^"]*)"/.exec(direct);
        mark(o, lineNo, 0, 'peer', direct.replace(/^Another Claude session sent a message:\s*/, ''), (from ? from[1] + ': ' : '') + innerText(direct.replace(/^Another Claude session sent a message:\s*/, '').replace(/<\/?(teammate-message|cross-session-message)[^>]*>/g, ' ')));
        return;
      }
      // A slash command you ran locally (/model) echoes as <command-name>; it is
      // not a prompt unless Claude Code says a human sent it to the agent (a
      // skill like /using-agent-skills does start a turn).
      if (!human && /<command-name>/.test(direct)) {
        mark(o, lineNo, 0, 'command', innerText(direct), 'You ran ' + stripPromptEnvelopes(direct));
        return;
      }
      if (says && !human) {
        var who = (isObj(o.origin) && o.origin.kind) || o.turnOrigin || o.promptSource || 'system';
        mark(o, lineNo, 0, 'auto', direct, 'from ' + String(who).replace(/[_-]/g, ' ') + ': ' + innerText(direct));
        return;
      }
      var r = base(o, lineNo, 0, 'prompt');
      r.text = direct;
      r.label = R.plainLabel(direct) || stripPromptEnvelopes(direct) || direct || '[image]';
      r.typed = human;
      r.pasted = R.isPasted(direct);
      r.images = pics;
      addRow(r);
    }

    function joinResult(o, lineNo, b, bi) {
      var row = toolById[b.tool_use_id];
      var textOut = textOf(b.content) || describeToolUseResult(o.toolUseResult) || (b.is_error ? '[tool error, no output]' : '[no output]');
      var imgs = imageRefs(o, b.content, bi + '.');
      var dims = imgs.length === 1 ? imageDims(o) : null;
      if (dims) { imgs[0].w = dims.w; imgs[0].h = dims.h; }
      if (!row) {
        ledger.orphanResults++;
        mark(o, lineNo, bi, 'result', textOut, 'Tool result with no call: ' + firstLine(textOut)).images = imgs;
        return;
      }
      row.result = textOut;
      row.images = imgs;
      row.resultLine = lineNo;
      row.resultUuid = typeof o.uuid === 'string' ? o.uuid : '';
      row.tEnd = timeOf(o);
      var denied = b.is_error === true && (o.toolDenialKind || (isObj(o.permissionDecision) && o.permissionDecision.decision === 'reject') || /doesn't want to proceed with this tool use/.test(textOut));
      if (denied) { row.status = 'denied'; row.denial = typeof o.toolDenialKind === 'string' ? o.toolDenialKind : 'rejected'; }
      else if (/^\s*\[Request interrupted by user/.test(textOut)) row.status = 'interrupted';
      else row.status = b.is_error === true ? 'error' : 'ok';
      bump(lineRows, lineNo);
    }

    function parseAssistant(o, lineNo) {
      var content = Array.isArray(o.message.content) ? o.message.content : [];
      if (o.isApiErrorMessage) {
        mark(o, lineNo, 0, 'error', textOf(content) || String(o.error || 'API error'), 'API error' + (o.apiErrorStatus ? ' ' + o.apiErrorStatus : '') + ': ' + (textOf(content) || o.error || ''));
        return;
      }
      content.forEach(function (b, bi) {
        if (!isObj(b)) return;
        var r;
        switch (b.type) {
          case 'text':
            if (typeof b.text !== 'string' || !b.text.trim()) return;
            r = base(o, lineNo, bi, 'message');
            r.text = b.text;
            r.msgId = o.message.id || '';
            addRow(r);
            return;
          case 'thinking':
          case 'redacted_thinking':
            r = base(o, lineNo, bi, 'thinking');
            r.text = typeof b.thinking === 'string' ? b.thinking.trim() : '';
            r.stored = !!r.text;
            r.redacted = b.type === 'redacted_thinking';
            r.req = o.requestId || '';
            r.ms = typeof o.thinkingDurationMs === 'number' ? o.thinkingDurationMs : null;
            addRow(r);
            return;
          case 'tool_use':
          case 'server_tool_use':
            r = base(o, lineNo, bi, 'tool');
            r.name = typeof b.name === 'string' ? b.name : 'tool';
            r.label = toolLabel(r.name);
            r.toolId = b.id || '';
            r.input = b.input;
            r.cwd = o.cwd || info.cwd || '';
            r.headline = toolHeadline(r.name, b.input, r.cwd);
            r.server = b.type === 'server_tool_use';
            r.status = 'pending';
            r.result = null;
            r.images = [];
            if (b.id) toolById[b.id] = r;
            addRow(r);
            if (r.server) bump(ledger.joinedHere, 'block:server_tool_use');
            return;
          case 'advisor_tool_result':
          case 'web_search_tool_result':
            var host = toolById[b.tool_use_id];
            bump(ledger.joinedHere, 'block:' + b.type);
            if (!host) { ledger.orphanResults++; mark(o, lineNo, bi, 'result', textOf(b.content), b.type); return; }
            var c = b.content;
            if (isObj(c) && c.encrypted_content !== undefined) { host.result = '[result encrypted: the transcript keeps no readable text]'; host.status = 'encrypted'; }
            else if (isObj(c) && c.type && /error/.test(c.type)) { host.result = (c.error_code || c.type) + ''; host.status = 'error'; }
            else { host.result = textOf(c) || (Array.isArray(c) ? c.length + ' results' : '[no output]'); host.status = 'ok'; }
            host.resultLine = lineNo;
            host.resultUuid = typeof o.uuid === 'string' ? o.uuid : '';
            host.tEnd = timeOf(o);
            bump(lineRows, lineNo);
            return;
          case 'image':
            r = base(o, lineNo, bi, 'message');
            r.text = '[image]';
            r.msgId = o.message.id || '';
            r.image = imageRef(o, b, String(bi));
            r.images = r.image ? [r.image] : [];
            addRow(r);
            return;
          case 'fallback':
            mark(o, lineNo, bi, 'system', 'Model fallback', 'Model fallback');
            return;
          default:
            bump(ledger.unplaced, 'block:' + b.type);
        }
      });
    }

    function parseSystem(o, lineNo) {
      var sub = o.subtype || '?';
      var content = typeof o.content === 'string' ? o.content : '';
      if (!SYSTEM_SUBTYPES[sub]) { bump(ledger.unplaced, 'system:' + sub); return; }
      if (sub === 'compact_boundary') {
        var cm = isObj(o.compactMetadata) ? o.compactMetadata : {};
        mark(o, lineNo, 0, 'compact', content || 'Conversation compacted',
          'Context compacted' + (cm.trigger ? ', ' + cm.trigger : '') + (typeof cm.preTokens === 'number' ? ', at ' + cm.preTokens.toLocaleString('en-US') + ' tokens' : ''));
      } else if (sub === 'away_summary') mark(o, lineNo, 0, 'away', content, content || 'Recap while you were away');
      else if (sub === 'informational') mark(o, lineNo, 0, 'info', innerText(content), innerText(content) || 'Notice');
      else if (sub === 'local_command') mark(o, lineNo, 0, 'command', innerText(content), 'You ran ' + (stripPromptEnvelopes(content) || innerText(content)));
      else if (sub === 'scheduled_task_fire') mark(o, lineNo, 0, 'task', content, 'Scheduled task fired' + (content ? ': ' + content : ''));
      else if (SYSTEM_SUBTYPES[sub] === 'mark') mark(o, lineNo, 0, 'system', content || sub, sub.replace(/_/g, ' ') + (content ? ': ' + innerText(content) : ''));
      // panel subtypes (turn_duration, stop_hook_summary) draw no row
    }

    function parseAttachment(o, lineNo) {
      var a = isObj(o.attachment) ? o.attachment : {};
      var at = a.type || '?';
      if (!ATTACHMENTS[at]) { bump(ledger.unplaced, 'attachment:' + at); return; }
      if (at === 'queued_command') {
        var p = R.unwrapPasted(typeof a.prompt === 'string' ? a.prompt : textOf(a.prompt));
        if (p && p.trim()) {
          var r = mark(o, lineNo, 0, 'queued', p.trim(), p.trim());
          r.label = stripPromptEnvelopes(p) || p.trim();
        }
      } else if (at === 'hook_blocking_error') {
        var be = isObj(a.blockingError) ? a.blockingError : {};
        var msg = be.blockingError || a.content || a.stderr || '';
        mark(o, lineNo, 0, 'hook', String(msg), 'A hook blocked ' + (a.toolName || 'a tool') + (msg ? ': ' + msg : ''));
      }
      // every other attachment draws no row
    }

    // Superseded thinking (collapse.ts): an empty thinking block whose request
    // also wrote a stored summary adds nothing, so it goes.
    var storedReq = {};
    rows.forEach(function (r) { if (r.kind === 'thinking' && r.stored && r.req) storedReq[r.req] = 1; });
    rows = rows.filter(function (r) {
      if (r.kind === 'thinking' && !r.stored && r.req && storedReq[r.req]) { lineRows[r.line]--; ledger.supersededThinking++; return false; }
      return true;
    });
    rows.forEach(function (r, i) { r.i = i; });

    // ledger: drawn + not drawn + malformed = parsed
    lineKeys.forEach(function (lk) {
      if (lineRows[lk[0]] > 0) ledger.drawnLines++;
      else { ledger.notDrawn++; bump(ledger.notDrawnByKey, lk[1]); }
    });
    ledger.rows = rows.length;

    var turns = buildTurns(rows);
    var model = { info: info, rows: rows, turns: turns, ledger: ledger };
    model.last = analyzeLast(model);
    model.parseMs = (typeof performance !== 'undefined' ? performance : Date).now() - started;
    return model;
  }

  // Passive marks (a compaction, a recap, a notice) can follow the answer
  // without meaning the agent is still at work.
  var PASSIVE = { compact: 1, away: 1, info: 1, command: 1, reminder: 1, meta: 1, system: 1 };

  // ---------- turns: one per prompt you typed ----------
  function buildTurns(rows) {
    var turns = [];
    var cur = null;
    rows.forEach(function (r) {
      if (r.kind === 'prompt') {
        cur = { n: turns.length + (turns.length && turns[0].opener === null ? 0 : 1), opener: r.i, rows: [] };
        turns.push(cur);
      } else {
        if (!cur) { cur = { n: 0, opener: null, rows: [] }; turns.push(cur); }
        cur.rows.push(r.i);
      }
      r.turn = cur.n;
    });
    turns.forEach(function (tn) {
      var all = (tn.opener !== null ? [tn.opener] : []).concat(tn.rows);
      var start = null, end = null;
      tn.tools = 0; tn.failed = 0; tn.denied = 0; tn.pending = 0; tn.messages = 0; tn.thinking = 0; tn.marks = 0;
      all.forEach(function (i) {
        var r = rows[i];
        if (r.t !== null && (start === null || r.t < start)) start = r.t;
        // a turn lasts until its last real step; a recap or compaction written
        // later does not stretch it
        if (!(r.kind === 'mark' && PASSIVE[r.tag])) [r.t, r.tEnd].forEach(function (x) { if (x !== null && x !== undefined && (end === null || x > end)) end = x; });
        if (r.kind === 'tool') {
          tn.tools++;
          if (r.status === 'error' || r.status === 'interrupted') tn.failed++;
          else if (r.status === 'denied') tn.denied++;
          else if (r.status === 'pending') tn.pending++;
        } else if (r.kind === 'message') tn.messages++;
        else if (r.kind === 'thinking') tn.thinking++;
        else if (r.kind === 'mark') tn.marks++;
      });
      tn.start = start;
      tn.end = end;
      tn.ms = start !== null && end !== null && tn.rows.length ? end - start : null;
      tn.label = tn.opener !== null ? rows[tn.opener].label : 'Before your first prompt';
    });
    return turns;
  }

  // ---------- the last turn: its final answer, or why there is none ----------
  // Passive marks (a compaction, a recap, a notice) can follow the answer
  // without meaning the agent is still at work.
  function analyzeLast(model) {
    var rows = model.rows, turns = model.turns;
    var tn = turns[turns.length - 1];
    if (!tn) return { turn: null, state: 'empty', fold: [], answer: [], latestText: null };
    var tail = tn.rows;
    var lastConv = -1, lastTool = -1, latestText = null;
    tail.forEach(function (i, k) {
      var r = rows[i];
      if (r.kind === 'tool') lastTool = k;
      if (r.kind === 'message') latestText = i;
      if (r.kind !== 'mark' || !PASSIVE[r.tag]) lastConv = k;
    });
    var answerFrom = -1;
    for (var k = lastTool + 1; k < tail.length; k++) if (rows[tail[k]].kind === 'message') { answerFrom = k; break; }
    var state;
    var lastRow = lastConv >= 0 ? rows[tail[lastConv]] : null;
    if (lastRow && lastRow.kind === 'message' && answerFrom >= 0) state = 'answered';
    else if (lastRow && ((lastRow.kind === 'mark' && lastRow.tag === 'interrupt') || (lastRow.kind === 'tool' && lastRow.status === 'interrupted'))) state = 'interrupted';
    else if (!lastRow) state = 'waiting';
    else state = 'working';
    var fold, answer;
    if (state === 'answered') { fold = tail.slice(0, answerFrom); answer = tail.slice(answerFrom); }
    else { fold = tail.slice(); answer = []; }
    return { turn: tn.n, state: state, fold: fold, answer: answer, latestText: latestText, lastRow: lastRow ? lastRow.i : null };
  }

  // buildTurns and analyzeLast also turn a Codex rollout's rows into turns
  // (herdr-transcript-codex.js)
  var api = { parseTranscript: parseTranscript, buildTurns: buildTurns, analyzeLast: analyzeLast };
  if (typeof window !== 'undefined') {
    window.crit = window.crit || {};
    window.crit.herdrTranscriptParse = api;
  }
  if (typeof module === 'object' && module.exports) module.exports = api;
})();
