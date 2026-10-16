// herdr-transcript-codex.js: turns a Codex rollout (~/.codex/sessions/…/
// rollout-*.jsonl, as GET /api/herdr/transcript sends it for a Codex pane)
// into the same rows and turns herdr-transcript-parse.js makes from a Claude
// transcript, so the view, comments and Finish review work unchanged.
// It reads Codex's own record of what the TUI showed: event_msg lines of type
// item_completed (UserMessage, AgentMessage, CommandExecution, McpToolCall,
// FileChange, ...) plus task_started, task_complete and turn_aborted. The
// model's copy of the conversation (response_item) is dropped by the daemon.
// A prompt sent while a turn runs (Codex steers the turn with it) is a
// "queued" mark, the way Claude's queued_command is. Pure: no DOM.
// Mounted on window.crit.herdrTranscriptCodex.
// Dependencies: window.crit.herdrTranscriptRules, window.crit.herdrTranscriptParse
// (require() in Node).
(function () {
  'use strict';
  var node = typeof module === 'object' && module.exports;
  var R = node ? require('./herdr-transcript-rules.js') : window.crit.herdrTranscriptRules;
  var TP = node ? require('./herdr-transcript-parse.js') : window.crit.herdrTranscriptParse;
  var isObj = R.isObj, timeOf = R.timeOf, clip = R.clip, firstLine = R.firstLine, bump = R.bump, shortPath = R.shortPath;

  function texts(content) {
    return (Array.isArray(content) ? content : []).map(function (c) { return isObj(c) && typeof c.text === 'string' ? c.text : ''; }).filter(Boolean).join('\n\n');
  }
  function ms(v) { return typeof v === 'number' && v > 0 ? v : null; }
  function urlPath(p) { return String(p || '').replace(/^file:\/\//, ''); }
  // ["/bin/zsh", "-lc", "git status"] is the command git status
  function commandText(cmd) {
    if (!Array.isArray(cmd)) return String(cmd || '');
    if (cmd.length === 3 && /(^|\/)(ba|z|da|k)?sh$/.test(cmd[0]) && /^-l?c$/.test(cmd[1])) return String(cmd[2]);
    return cmd.map(function (a) { a = String(a); return /^[\w@%+=:,./-]+$/.test(a) ? a : "'" + a.replace(/'/g, "'\\''") + "'"; }).join(' ');
  }
  function mcpText(result) {
    if (!isObj(result)) return typeof result === 'string' ? result : '';
    return texts(result.content) || (Array.isArray(result.content) && result.content.length ? result.content.length + ' blocks' : '');
  }
  // a FileChange as one text: each file's kind and path, then its diff or content
  function changeText(changes, cwd) {
    return Object.keys(changes || {}).map(function (p) {
      var c = isObj(changes[p]) ? changes[p] : {};
      var head = (c.type || 'change') + ' ' + shortPath(p, cwd) + (c.move_path ? ' → ' + shortPath(c.move_path, cwd) : '');
      var body = typeof c.unified_diff === 'string' ? c.unified_diff : typeof c.content === 'string' ? c.content : '';
      return head + (body ? '\n' + body.replace(/\n$/, '') : '');
    }).join('\n\n');
  }

  function parseRollout(text) {
    var started = (typeof performance !== 'undefined' ? performance : Date).now();
    var raw = String(text || '').split('\n');
    var rows = [];
    var drawn = {};
    var ledger = { parsed: 0, malformed: 0, malformedLines: [], drawnLines: 0, notDrawn: 0, rows: 0, notDrawnByKey: {}, unplaced: {}, joinedHere: {}, bytesByKey: {}, supersededThinking: 0, orphanResults: 0 };
    var info = { agent: 'Codex', sessionId: null, cwd: null, version: null, aiTitle: null, customTitle: null, first: null, last: null, bytes: text.length };
    var turnOpen = false, turnHasPrompt = false;

    function add(r) { r.i = rows.length; rows.push(r); drawn[r.line] = 1; return r; }
    function base(lineNo, uuid, kind, t) { return { kind: kind, line: lineNo, uuid: uuid || '', key: 'L' + lineNo, t: t }; }
    function mark(lineNo, uuid, t, tag, full, oneLine) {
      var r = base(lineNo, uuid, 'mark', t);
      r.tag = tag;
      r.full = String(full || '');
      r.text = clip(oneLine !== undefined && oneLine !== null ? oneLine : r.full, 200);
      return add(r);
    }
    function tool(lineNo, it, p, lineT, name, label, headline, input, result, status) {
      var r = base(lineNo, it.id, 'tool', ms(p.started_at_ms) || lineT);
      r.name = name; r.label = label; r.toolId = it.id || ''; r.input = input; r.cwd = info.cwd || '';
      r.headline = headline; r.server = false; r.status = status; r.result = result; r.images = [];
      r.resultLine = lineNo; r.resultUuid = r.uuid; r.tEnd = ms(p.completed_at_ms);
      return add(r);
    }

    function item(lineNo, p, lineT) {
      var it = isObj(p.item) ? p.item : {};
      var t = ms(p.completed_at_ms) || lineT, r, h;
      switch (it.type) {
        case 'UserMessage':
          var said = texts(it.content).trim();
          var pics = (Array.isArray(it.content) ? it.content : []).filter(function (c) { return isObj(c) && c.type === 'local_image'; }).length;
          if (!said && !pics) return;
          if (turnOpen && turnHasPrompt) { r = mark(lineNo, it.id, t, 'queued', said, said); r.label = firstLine(said) || said; return; }
          turnHasPrompt = true;
          r = base(lineNo, it.id, 'prompt', ms(p.started_at_ms) || t);
          r.text = said || '[image]';
          r.label = R.plainLabel(said) || '[image]';
          r.typed = true; r.pasted = R.isPasted(said); r.images = [];
          add(r);
          return;
        case 'AgentMessage':
          var says = texts(it.content);
          if (!says.trim()) return;
          r = base(lineNo, it.id, 'message', t);
          r.text = says; r.msgId = it.id || ''; r.phase = it.phase || '';
          add(r);
          return;
        case 'Reasoning':
          // Codex keeps most reasoning encrypted: only a stored summary is a row
          var thought = (Array.isArray(it.summary_text) && it.summary_text.length ? it.summary_text : Array.isArray(it.raw_content) ? it.raw_content : [])
            .map(function (s) { return typeof s === 'string' ? s : isObj(s) && typeof s.text === 'string' ? s.text : ''; }).filter(Boolean).join('\n\n').trim();
          if (!thought) return;
          r = base(lineNo, it.id, 'thinking', ms(p.started_at_ms) || t);
          r.text = thought; r.stored = true; r.redacted = false; r.req = '';
          r.ms = ms(p.completed_at_ms) && ms(p.started_at_ms) ? p.completed_at_ms - p.started_at_ms : null;
          add(r);
          return;
        case 'CommandExecution':
          h = commandText(it.command);
          var failed = it.status === 'failed' || (typeof it.exit_code === 'number' && it.exit_code !== 0);
          var out = typeof it.aggregated_output === 'string' ? it.aggregated_output : '';
          if (typeof it.exit_code === 'number' && it.exit_code !== 0) out += (out && !/\n$/.test(out) ? '\n' : '') + '[exit ' + it.exit_code + ']';
          var input = { command: h };
          var wd = urlPath(it.cwd);
          if (wd && wd !== info.cwd) input.workdir = wd;
          tool(lineNo, it, p, lineT, 'Bash', 'Shell', firstLine(h), input, it.status === 'in_progress' ? null : out || '[no output]', failed ? 'error' : 'ok');
          return;
        case 'McpToolCall':
          var res = isObj(it.result) ? it.result : null;
          var bad = it.status === 'failed' || (res && res.isError === true) || it.error !== undefined;
          var name = 'mcp__' + (it.server || '?') + '__' + (it.tool || '?');
          var args = isObj(it.arguments) ? it.arguments : {};
          tool(lineNo, it, p, lineT, name, R.toolLabel(name), R.toolHeadline(name, args, info.cwd), args,
            mcpText(res) || (it.error ? String(isObj(it.error) ? it.error.message || JSON.stringify(it.error) : it.error) : '[no output]'), bad ? 'error' : 'ok');
          return;
        case 'FileChange':
          var paths = Object.keys(it.changes || {});
          h = paths.length ? shortPath(paths[0], info.cwd) + (paths.length > 1 ? ' and ' + (paths.length - 1) + ' more' : '') : '';
          tool(lineNo, it, p, lineT, 'Edit', 'Edit', h, changeText(it.changes, info.cwd), it.status === 'failed' ? 'Patch failed' : 'Applied to ' + paths.length + (paths.length === 1 ? ' file' : ' files'),
            it.status === 'failed' ? 'error' : 'ok');
          return;
        case 'ImageView':
          var img = urlPath(it.path);
          tool(lineNo, it, p, lineT, 'Read', 'View image', shortPath(img, info.cwd), { file_path: img }, '[image: ' + img + ']', 'ok');
          return;
        case 'Extension':
          var act = isObj(it.action) ? it.action : {};
          var q = it.query || act.query || act.url || act.pattern || '';
          var found = Array.isArray(it.results) ? it.results.map(function (x) { return isObj(x) ? [x.title, x.url, x.snippet].filter(Boolean).join(' · ') : String(x); }).join('\n') : '';
          var extIn = {};
          if (q) extIn.query = q;
          if (act.type) extIn.action = act.type;
          tool(lineNo, it, p, lineT, it.kind || 'extension', it.kind || 'extension', firstLine(q), extIn, found || '[no output]', it.status === 'failed' ? 'error' : 'ok');
          return;
        case 'CollabAgentToolCall':
          var who = (Array.isArray(it.receiver_agents) ? it.receiver_agents : []).map(function (a) { return isObj(a) ? a.agent_path || a.agent_nickname || '' : String(a); }).filter(Boolean);
          var states = isObj(it.agents_states) ? Object.keys(it.agents_states).map(function (k) { var s = it.agents_states[k]; return k + ': ' + (isObj(s) ? s.status || JSON.stringify(s) : s); }).join('\n') : '';
          tool(lineNo, it, p, lineT, 'agents.' + (it.tool || 'call'), 'agents.' + (it.tool || 'call'), who.join(', '), who.length ? { agents: who.join(', ') } : {}, states || '[no output]', it.status === 'failed' ? 'error' : 'ok');
          return;
        case 'SubAgentActivity':
          // started and completed are worth a line; "interacted" comes hundreds of times
          if (it.kind === 'started') mark(lineNo, it.id, t, 'info', '', 'Sub-agent ' + (it.agent_path || '?') + ' started' + (it.model ? ' (' + it.model + (it.reasoning_effort ? ' ' + it.reasoning_effort : '') + ')' : ''));
          else if (it.kind === 'completed') mark(lineNo, it.id, t, 'info', '', 'Sub-agent ' + (it.agent_path || '?') + ' finished');
          return;
        case 'ContextCompaction':
          mark(lineNo, it.id, t, 'compact', 'Codex compacted the conversation', 'Context compacted');
          return;
        default:
          bump(ledger.unplaced, 'item:' + it.type);
      }
    }

    for (var n = 0; n < raw.length; n++) {
      var s = raw[n];
      if (!s || !s.trim()) continue;
      var ln = n + 1;
      ledger.parsed++;
      var ev;
      try { ev = JSON.parse(s); } catch (e) { ledger.malformed++; ledger.malformedLines.push(ln); continue; }
      if (!isObj(ev) || typeof ev.type !== 'string' || !isObj(ev.payload)) { ledger.malformed++; ledger.malformedLines.push(ln); continue; }
      var p = ev.payload;
      var key = ev.type === 'event_msg' ? 'event_msg:' + (p.type === 'item_completed' && isObj(p.item) ? 'item:' + p.item.type : p.type) : ev.type;
      bump(ledger.bytesByKey, key, s.length);
      var t = timeOf(ev);
      if (t !== null) { if (info.first === null || t < info.first) info.first = t; if (info.last === null || t > info.last) info.last = t; }
      if (ev.type === 'session_meta') {
        info.sessionId = p.id || p.session_id || null;
        info.cwd = p.cwd || null;
        info.version = p.cli_version ? 'codex ' + p.cli_version : null;
        continue;
      }
      if (ev.type !== 'event_msg') { bump(ledger.unplaced, ev.type); continue; }
      if (p.type === 'item_completed') item(ln, p, t);
      else if (p.type === 'task_started') { turnOpen = true; turnHasPrompt = false; }
      else if (p.type === 'task_complete') turnOpen = false;
      else if (p.type === 'turn_aborted') {
        turnOpen = false;
        var why = p.reason && p.reason !== 'interrupted' ? 'The turn stopped: ' + String(p.reason).replace(/_/g, ' ') : 'You stopped the turn';
        mark(ln, p.turn_id, t, 'interrupt', why, why);
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
    // Codex says when a turn ends (task_complete, turn_aborted). Until then a
    // message after the last tool is an update, not the answer.
    if (turnOpen && model.last.state === 'answered') {
      model.last.state = 'working';
      model.last.fold = model.last.fold.concat(model.last.answer);
      model.last.answer = [];
    }
    model.parseMs = (typeof performance !== 'undefined' ? performance : Date).now() - started;
    return model;
  }

  var api = { parseRollout: parseRollout, commandText: commandText };
  if (typeof window !== 'undefined') {
    window.crit = window.crit || {};
    window.crit.herdrTranscriptCodex = api;
  }
  if (typeof module === 'object' && module.exports) module.exports = api;
})();
