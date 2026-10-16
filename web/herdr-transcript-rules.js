// herdr-transcript-rules.js: how the /herdr page reads a Claude Code
// transcript, taken from the Transcript Reader (catalog.ts, prompts.ts,
// content.ts): which line types draw a row, which user lines nobody typed,
// the input that says what a tool call did, and the words a comment uses to
// say what it is on. Pure: no DOM, no fetch.
// Mounted on window.crit.herdrTranscriptRules. Dependencies: none.
(function () {
  'use strict';

  // ---------- the Reader's catalog (catalog.ts) ----------
  // read: gets a row. fold/panel: never a row. mark: one low-contrast line.
  // Page E draws fewer marks than the Reader: only user lines nobody typed,
  // queued prompts, blocking hooks and a few system lines. Every other
  // attachment and top-level type draws no row and is counted in the ledger.
  var TOP_LEVEL = {
    user: 'read', assistant: 'read', started: 'read', result: 'read',
    'queue-operation': 'mark', 'frame-link': 'mark', 'pr-link': 'mark', 'worktree-state': 'mark',
    relocated: 'mark', 'fork-context-ref': 'mark', 'history-suppression': 'mark',
    mode: 'panel', 'permission-mode': 'panel', 'ai-title': 'panel', 'custom-title': 'panel',
    'last-prompt': 'panel', 'atis-latch': 'panel', 'agent-setting': 'panel', 'agent-name': 'panel',
    'bridge-session': 'panel', 'cost-state': 'panel', 'file-history-snapshot': 'panel',
    'file-history-delta': 'panel', 'artifact-autoreact-ledger': 'panel', 'artifact-comment-monitor': 'panel',
    // legacy types the Reader still knows
    summary: 'panel', progress: 'mark', turn_duration: 'panel', tag: 'panel', 'agent-color': 'panel',
    'attribution-snapshot': 'panel', 'content-replacement': 'panel', 'marble-origami-commit': 'panel',
    'marble-origami-snapshot': 'panel',
  };
  var SYSTEM_SUBTYPES = {
    compact_boundary: 'read', away_summary: 'read', scheduled_task_fire: 'read',
    local_command: 'mark', informational: 'mark', agents_killed: 'mark', model_refusal_fallback: 'mark',
    model_refusal_no_fallback: 'mark', model_consent_fallback: 'mark',
    turn_duration: 'panel', stop_hook_summary: 'panel',
  };
  var ATTACHMENTS = {
    hook_success: 'fold', async_hook_response: 'fold', hook_additional_context: 'fold',
    hook_system_message: 'fold', hook_non_blocking_error: 'fold', hook_cancelled: 'fold',
    bash_output_audience_note: 'fold', read_truncation_notice: 'fold', structured_output: 'fold',
    hook_blocking_error: 'mark', queued_command: 'mark', date_change: 'mark', plan_mode: 'mark',
    plan_mode_exit: 'mark', plan_mode_reentry: 'mark', output_style: 'mark', auto_mode: 'mark',
    remote_session_change: 'mark', opened_file_in_ide: 'mark', selected_lines_in_ide: 'mark',
    edited_text_file: 'mark', nested_memory: 'mark', file: 'mark', invoked_skills: 'mark',
    dynamic_skill: 'mark', team_context: 'mark', goal_status: 'mark', ultra_effort_enter: 'mark',
    ultra_effort_exit: 'mark', workflow_size_guideline_change: 'mark', workflow_keyword_request: 'mark',
    total_tokens_reminder: 'panel', task_reminder: 'panel', silent_turn_reminder: 'panel',
    batching_reminder_sent: 'panel', skill_listing: 'panel', deferred_tools_delta: 'panel',
    deferred_tools_record: 'panel', mcp_instructions_delta: 'panel', agent_listing_delta: 'panel',
    command_permissions: 'panel', prompt_snapshot: 'panel', environment: 'panel', instructions: 'panel',
    session_context: 'panel', date: 'panel', model: 'panel', diagnostics: 'panel',
    compact_file_reference: 'panel',
  };
  // Content blocks. The Reader's table has the first eight; the rest are
  // blocks its parser handles (or this page joins) without a table entry.
  var BLOCKS = {
    text: 'read', thinking: 'read', tool_use: 'read', tool_result: 'read', image: 'read',
    document: 'read', fallback: 'read', tool_reference: 'panel',
  };
  var BLOCKS_JOINED_HERE = {
    redacted_thinking: 1, server_tool_use: 1, web_search_tool_result: 1, advisor_tool_result: 1,
  };

  // ---------- what a user line is (prompts.ts) ----------
  // A prompt as a one-line label: envelopes gone, and no markdown marks or
  // pasted-content tags left over.
  function plainLabel(text) {
    return stripPromptEnvelopes(String(text || '').replace(/<\/?pasted_content[^>]*>/g, ' '))
      .replace(/(^|\s)(>\s*)+/g, '$1').replace(/\*\*|__|`/g, '').replace(/\s+/g, ' ').trim();
  }
  // Text herdr types into a pane (and anything pasted) reaches the transcript
  // wrapped as <pasted_content id="…">…</pasted_content> (Claude Code 2.1.300).
  // The words inside are the prompt; the tags are not.
  var PASTED = /<pasted_content\b[^>]*>([\s\S]*?)<\/pasted_content>/g;
  function unwrapPasted(text) { return String(text || '').replace(PASTED, '$1').replace(/<\/?pasted_content\b[^>]*>/g, ''); }
  function isPasted(text) { return /<pasted_content\b/.test(String(text || '')); }
  function stripPromptEnvelopes(text) {
    var s = unwrapPasted(text);
    s = s.replace(/^Caveat:[\s\S]*?(?:\n\s*\n|$)/u, '');
    s = s.replace(/<(system-reminder|local-command-stdout|local-command-stderr|task-notification)>[\s\S]*?<\/\1>/gu, '');
    var args = /<command-args>([\s\S]*?)<\/command-args>/u.exec(s);
    var message = /<command-message>([\s\S]*?)<\/command-message>/u.exec(s);
    var name = /<command-name>([\s\S]*?)<\/command-name>/u.exec(s);
    if (args || message || name) {
      var argument = args && args[1] ? args[1].trim() : '';
      var label = ((message && message[1]) || (name && name[1]) || '').trim();
      var rest = s.replace(/<\/?command-[a-z-]+>/gu, ' ').trim();
      s = argument ? (label + ' ' + argument).trim() : (label || rest);
    }
    return s.replace(/\s+/gu, ' ').trim();
  }
  var INTERRUPT = /^\s*\[Request interrupted by user(?: for tool use)?\]\s*$/u;
  function userLineKind(text) {
    if (INTERRUPT.test(text)) return 'interrupt';
    if (/^\s*<task-notification>/u.test(text)) return 'notification';
    if (stripPromptEnvelopes(text) === '') return 'command-output';
    return 'prompt';
  }
  var ANSI = new RegExp(String.fromCharCode(27) + '\\[[0-9;]*m', 'gu');
  function innerText(text) {
    return String(text || '')
      .replace(/<\/?(system-reminder|local-command-stdout|local-command-stderr|local-command-caveat|task-notification|command-[a-z-]+)>/gu, ' ')
      .replace(ANSI, '')
      .replace(/\s+/gu, ' ')
      .trim();
  }

  // ---------- small helpers ----------
  function isObj(v) { return v !== null && typeof v === 'object' && !Array.isArray(v); }
  function timeOf(o) { var t = typeof o.timestamp === 'string' ? Date.parse(o.timestamp) : NaN; return isNaN(t) ? null : t; }
  function clip(s, n) { s = String(s || '').replace(/\s+/g, ' ').trim(); return s.length > n ? s.slice(0, n - 1) + '…' : s; }
  function firstLine(s) {
    var lines = String(s || '').split('\n');
    for (var i = 0; i < lines.length; i++) if (lines[i].trim()) return lines[i].trim();
    return '';
  }
  function bump(map, key, by) { map[key] = (map[key] || 0) + (by || 1); }
  function lineCount(s) { return s ? String(s).split('\n').length : 0; }

  // Text of a content value: a string, or blocks with images as placeholders.
  function textOf(content) {
    if (typeof content === 'string') return content;
    if (!Array.isArray(content)) return '';
    var out = [];
    content.forEach(function (b) {
      if (!isObj(b)) return;
      if (b.type === 'text' && typeof b.text === 'string') out.push(b.text);
      else if (b.type === 'image') out.push('[image' + (isObj(b.source) && b.source.media_type ? ' ' + b.source.media_type : '') + ']');
      else if (b.type === 'document') out.push('[document]');
      else if (b.type === 'tool_reference') out.push('[tool reference: ' + (b.tool_name || 'tool') + ']');
      else if (Array.isArray(b.content) || typeof b.content === 'string') out.push(textOf(b.content));
    });
    return out.join('\n');
  }
  // A top-level toolUseResult as text, when the result block itself is empty (content.ts).
  function describeToolUseResult(r) {
    if (r === null || r === undefined) return '';
    if (typeof r === 'string') return r;
    if (typeof r === 'number' || typeof r === 'boolean') return String(r);
    if (Array.isArray(r)) return textOf(r);
    if (!isObj(r)) return '';
    var parts = [];
    if (typeof r.stdout === 'string' && r.stdout) parts.push(r.stdout);
    if (typeof r.stderr === 'string' && r.stderr) parts.push('stderr: ' + r.stderr);
    if (parts.length) return parts.join('\n');
    if (Array.isArray(r.content) || typeof r.content === 'string') { var t = textOf(r.content); if (t) return t; }
    return '';
  }

  // ---------- tool headline: the input that says what a call did ----------
  // display and code come last: pi's fabric_exec and codemode calls have nothing else
  var HEADLINE_KEYS = ['command', 'file_path', 'notebook_path', 'path', 'pattern', 'query', 'url', 'skill', 'prompt', 'description', 'text', 'content', 'task_id', 'shell_id', 'display', 'code'];
  var AGENT_KEYS = ['description', 'prompt', 'subagent_type'];
  var SEARCH_KEYS = ['pattern', 'path', 'glob', 'query'];
  function shortPath(p, cwd) {
    p = String(p || '');
    if (cwd && p.indexOf(cwd + '/') === 0) return p.slice(cwd.length + 1);
    var home = /^\/Users\/[^/]+\//.exec(p);
    return home ? '~/' + p.slice(home[0].length) : p;
  }
  function toolHeadline(name, input, cwd) {
    if (typeof input === 'string') return firstLine(input);
    if (!isObj(input)) return '';
    if (name === 'AskUserQuestion' && Array.isArray(input.questions) && input.questions[0] && input.questions[0].question) return firstLine(input.questions[0].question);
    if ((name === 'TodoWrite' || name === 'TaskCreate') && Array.isArray(input.todos)) return input.todos.length + ' items';
    var keys = (name === 'Agent' || name === 'Task') ? AGENT_KEYS : (name === 'Grep' || name === 'Glob') ? SEARCH_KEYS : HEADLINE_KEYS;
    for (var i = 0; i < keys.length; i++) {
      var v = input[keys[i]];
      if (typeof v === 'string' && v.trim()) {
        if (/path$/.test(keys[i])) return shortPath(v, cwd);
        return firstLine(v);
      }
    }
    var ks = Object.keys(input);
    return ks.length ? ks.slice(0, 4).join(', ') : '';
  }
  function toolLabel(name) {
    var m = /^mcp__(.+?)__(.+)$/.exec(name || '');
    return m ? m[1] + '/' + m[2] : (name || 'tool');
  }
  // toolFromAgent: a pi, opencode, grok or cursor-agent tool call in Claude's
  // names and keys, the ones the view (inputLines) and the comment words
  // (describeRow) know: bash, run_terminal_command and Shell -> Bash,
  // search_replace and StrReplace -> Edit, filePath, target_file or path ->
  // file_path, oldString or oldText -> old_string, and so on. Other tools pass
  // through.
  var AGENT_TOOLS = {
    bash: 'Bash', read: 'Read', edit: 'Edit', write: 'Write', grep: 'Grep', glob: 'Glob', find: 'Glob', webfetch: 'WebFetch', task: 'Agent', todowrite: 'TodoWrite', question: 'AskUserQuestion',
    read_file: 'Read', run_terminal_command: 'Bash', search_replace: 'Edit', todo_write: 'TodoWrite', list_dir: 'LS',
    Shell: 'Bash', StrReplace: 'Edit', ReadFile: 'Read',
  };
  var AGENT_KEYS_TO = {
    filePath: 'file_path', target_file: 'file_path', oldString: 'old_string', oldText: 'old_string', newString: 'new_string', newText: 'new_string', replaceAll: 'replace_all',
    include: 'glob', glob_pattern: 'pattern', target_directory: 'path', contents: 'content', search_term: 'query',
  };
  function toolFromAgent(name, input) {
    var n = AGENT_TOOLS[name] || name || 'tool';
    if (!isObj(input)) return { name: n, input: input };
    var out = {};
    Object.keys(input).forEach(function (k) {
      var to = AGENT_KEYS_TO[k] || (k === 'path' && (n === 'Read' || n === 'Edit' || n === 'Write') ? 'file_path' : k);
      out[to] = input[k];
    });
    // pi's edit with an edits list is a MultiEdit
    if (n === 'Edit' && Array.isArray(out.edits)) {
      n = 'MultiEdit';
      out.edits = out.edits.map(function (e) { return isObj(e) ? toolFromAgent('edit', e).input : e; });
    }
    return { name: n, input: out };
  }

  // ---------- what a comment is on, in words the agent can find ----------
  function code(s) { return '`' + clip(String(s || '').replace(/`/g, "'"), 80) + '`'; }
  function quoted(s, n) { return '"' + clip(String(s || '').replace(/"/g, "'").replace(/[\s.,;:]+$/, ''), n || 120) + '"'; }
  var MARK_WORDS = {
    compact: 'the compaction', away: 'the recap', info: 'the notice', command: 'the command',
    reminder: 'the reminder', meta: 'the injected context', interrupt: 'my interrupt',
    task: 'the background task note', queued: 'my message sent while you worked', hook: 'the blocked hook',
    error: 'the API error', result: 'the tool result', system: 'the system note',
    peer: 'the message from another session', auto: 'the automatic message',
  };
  function isFinalRow(r, model) {
    var L = model && model.last;
    return !!(L && r.kind === 'message' && L.state === 'answered' && L.answer.indexOf(r.i) >= 0);
  }
  // describeRow: "On your Bash call `rg -l x` (turn 3)", "On your last answer".
  function describeRow(r, model) {
    var turnTxt = 'turn ' + r.turn;
    switch (r.kind) {
      case 'prompt': return 'On my prompt in ' + turnTxt;
      case 'message': return isFinalRow(r, model) ? 'On your last answer' : 'On your message in ' + turnTxt;
      case 'thinking': return 'On your thinking in ' + turnTxt;
      case 'tool':
        var h = r.headline, what;
        if (r.name === 'Edit' || r.name === 'MultiEdit' || r.name === 'Write' || r.name === 'Read' || r.name === 'NotebookEdit') what = 'your ' + r.name + ' of ' + code(h);
        else if (r.name === 'Grep' || r.name === 'Glob') what = 'your ' + r.name + ' for ' + code(h);
        else if (r.name === 'Agent' || r.name === 'Task') what = 'your Agent call ' + quoted(h, 80);
        else if (r.name === 'advisor') what = 'your advisor call';
        else what = 'your ' + r.label + ' call' + (h ? ' ' + code(h) : '');
        return 'On ' + what + ' (' + turnTxt + ')';
      case 'mark': return 'On ' + (MARK_WORDS[r.tag] || 'the note') + ' in ' + turnTxt;
    }
    return 'On ' + turnTxt;
  }
  // ---------- line ranges from the gutter "+" (crit's drag and shift-click) ----------
  // spanOf: the range from an anchor block to another block, either direction.
  function spanOf(a, b) { return { ls: Math.min(a.ls, b.ls), le: Math.max(a.le, b.le) }; }
  // sliceLines: the source lines a range covers, as the comment quotes them.
  function sliceLines(lines, ls, le) { return (lines || []).slice(ls - 1, le).join('\n'); }
  function linesText(ls, le) { return ls === le ? 'line ' + ls : 'lines ' + ls + '-' + le; }
  // describeRange: "On your last answer (lines 4-9)", "On your Bash call `x`
  // (turn 3), output lines 2-5". Lines count within that text, from 1.
  function describeRange(r, model, part, ls, le) {
    var base = describeRow(r, model);
    if (r.kind === 'tool') return base + ', ' + (part === 'in' ? 'input' : 'output') + ' ' + linesText(ls, le);
    return base + ' (' + linesText(ls, le) + ')';
  }

  // quoteFor: the line a comment quotes when no text was selected. A tool call
  // is already named by its input, so it quotes nothing.
  function quoteFor(r) {
    if (r.kind === 'tool') return '';
    if (r.kind === 'prompt' || (r.kind === 'mark' && r.tag === 'queued')) return firstLine(r.label || r.text);
    return firstLine(r.text);
  }

  var api = {
    TOP_LEVEL: TOP_LEVEL, SYSTEM_SUBTYPES: SYSTEM_SUBTYPES, ATTACHMENTS: ATTACHMENTS, BLOCKS: BLOCKS,
    BLOCKS_JOINED_HERE: BLOCKS_JOINED_HERE,
    stripPromptEnvelopes: stripPromptEnvelopes, unwrapPasted: unwrapPasted, isPasted: isPasted, plainLabel: plainLabel, userLineKind: userLineKind, innerText: innerText,
    isObj: isObj, timeOf: timeOf, clip: clip, firstLine: firstLine, bump: bump, lineCount: lineCount,
    textOf: textOf, describeToolUseResult: describeToolUseResult,
    shortPath: shortPath, toolHeadline: toolHeadline, toolLabel: toolLabel, toolFromAgent: toolFromAgent, describeRow: describeRow, quoteFor: quoteFor, spanOf: spanOf, sliceLines: sliceLines, describeRange: describeRange,
  };
  if (typeof window !== 'undefined') {
    window.crit = window.crit || {};
    window.crit.herdrTranscriptRules = api;
  }
  if (typeof module === 'object' && module.exports) module.exports = api;
})();
