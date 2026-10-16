# herdr control center API

`drover herdr` runs one daemon for every herdr machine. The review page is drover's normal code-review page with `session.review_type === "herdr"`: each agent pane is a synthetic markdown file, grouped by machine in the file tree. `POST /api/herdr/send` and `POST /api/herdr/exit` are the only routes that type into a pane, and `POST /api/herdr/close` the only one that closes one, each one pane per call. The page calls them only on a click: **Send now** on a comment form sends that comment, a pane header's **Finish review** sends every unresolved comment on that pane as one prompt, and **Exit** or **Close pane** ends that pane once its dialog is confirmed. Other panes are never touched.

Fixture: `web/__tests__/fixtures/herdr-sweep.json`, the /api/herdr shape built from a real sweep on 2026-10-15 (step-1 collector, so `completion_seq`/`state_change_seq` are null and there is no dialog, exited or unreadable item in it).

## The `/herdr` page

`drover herdr` opens `/herdr` (`web/herdr.html`, no `app.js`). One pane fills the page. The strip at the top keeps every pane in a fixed place, grouped by machine, so panes never reorder while you read. Each dot is the pane's state colour with its agent's mark inside (C claude, X codex, O opencode, π pi, G grok, Cu cursor; other agents their first two letters), and the herdr pane id under it. `/` or ⌘K (or Find) opens a finder that matches every word you type against a pane's name, pane id, agent, machine, project and state; the pane id itself ranks first, then ids that start or end with what you typed (`pD` finds `wSJ:pD`). The URL names the pane you are on (`/herdr#local/wSJ:pD`), so a reload, a link or another tab opens it; sessionStorage keeps the pass, the Back link and how far down the pane you had scrolled, for that tab. A Claude pane, and a Codex, pi, opencode, grok or cursor-agent pane on this Mac, shows its transcript (`GET /api/herdr/transcript`): earlier turns folded to one band each, then the last turn's prompt, its tool calls folded, and its final answer open. Any row (a message, a tool call, a selection inside one) takes a comment. **Finish review** shows exactly what will be typed, then sends the pane's comments and the reply box as one prompt through `POST /api/herdr/send` and moves on to the next pane that needs you, with a link back. Other panes (those agents on a box, a pane of theirs herdr names no session for, other agents, unreachable boxes) show what the sweep has: the screen answer, dialog, input line and notes. **Exit** and **Close pane**, at the right end of the reply buttons, end a pane after a dialog that says what will happen: Exit quits the agent with its own key and types `exit` into its shell (`POST /api/herdr/exit`), Close pane is `herdr pane close` (`POST /api/herdr/close`). Exit is off while herdr says the agent is working or blocked. The dialog then becomes the receipt, with the resume command the agent printed and a Copy button. Drafts and sent copies stay in the browser (`localStorage`, `crit-herdr:v1:` keys), not in `review.json`. `/` still serves the file-per-pane page described below.

A pane's name in the strip is the first of: herdr's agent `name`, the transcript's custom title, its AI title, the terminal `title`, the last part of `cwd`, the pane id.

## Running it

```bash
drover herdr                 # start or attach to the daemon, open the page; Ctrl+C stops the daemon
drover herdr --no-open -p 3000
drover herdr --print         # one sweep as text, no daemon
drover herdr --json          # one sweep as JSON (the collector's Snapshot, not /api/herdr)
drover herdr --json --wake   # resume paused instabox boxes first (the daemon never wakes boxes)
```

The daemon key is fixed (`daemon.HerdrSessionKey()`), so there is one herdr daemon per user. It runs from `$HOME`, records an empty branch, and its review folder is `~/.crit/reviews/<key>/` with `review.json` (`review_type: "herdr"`), `herdr-read.json` (read marks) and an empty `herdr-root/` used as `RepoRoot`.

A sweep runs every 15 seconds, one at a time, and takes about 6 seconds across 8 machines (local 1 s, each instabox 4 to 5 s). Machines are polled in parallel.

## Files

- Path: `<machine label>/<pane id>.md`, for example `workbox3/w3:p4.md`, `local/wNR:pC.md`. It stays the same while the pane lives, so comments survive title changes. Colons are fine in `/api/file?path=` (URL-encode) and in DOM ids.
- A pane at Claude's ExitPlanMode whose plan could be read gets a second file right after it: `<machine>/<pane id>.plan.md`, the plan markdown exactly as written, so its lines take comments of their own. The pane file's `## Plan` section is then a one-line pointer to it.
- `file_type: "markdown"`, `status: "modified"`, Document view.
- Order: machines in `machines` order; within a machine, panes that need the owner first.
- The Document/Diff toggle compares a pane with its content when you last marked it read (`POST /api/herdr/viewed`). A pane never marked read compares with its previous sweep. The baselines live in `herdr-baseline.json` next to `herdr-read.json`.
- A pane that disappears is dropped, unless it has comments; then it stays with its last content.

Body sections, in this order, each only when it applies: `# <title or pane id>`, a Where/Directory/Turn list, `## Needs you: …`, `## Dialog (<kind>)` (menu text in a fence, then numbered options), `## Plan \`<path>\``, `## Turn in progress`, `## Last answer (<time>)`, `## On the input line`, `## Exited`, `## Notes`. Transcript answers are inserted as markdown; screen answers go in a ```` ```text ```` fence.

The page must render herdr documents with markdown-it `html: false` and images disabled: the text comes from agents on other machines.

## Endpoints

All of them sit behind the usual readiness gate (503 until the first sweep has built the session) and the `Sec-Fetch-Site` check.

### `GET /api/herdr`

| Status | When |
|---|---|
| 200 | the latest sweep, shape below |
| 404 | the daemon is not a herdr session |
| 405 | not GET |
| 503 | no sweep yet |

```jsonc
{
  "collected_at": "2026-10-15T16:23:34Z",
  "interval_seconds": 15,
  "need_you": 4,                  // items with needs_you && !viewed
  "instabox_error": "…",          // only when `instabox ls --json` failed
  "machines": [ MachineStatus ],
  "items": [ Item & { "path": "workbox3/w3:p4.md", "viewed": false } ]
}
```

### `POST /api/herdr/viewed`

Body `{"path": "<review path>", "viewed": true|false}`. The path is a pane file or a plan file (`….plan.md`); a plan file is read-marked on its own, bound to its pane's current turn. 200 `{"path","viewed"}`; 400 bad body; 404 path not in the latest sweep or not a herdr session; 405 not POST. Sends `herdr-updated` afterwards.

### `GET /api/herdr/plan?path=<review path>`

The plan of a pane whose dialog is Claude's ExitPlanMode. 200 `{"path", "plan_path", "text", "error", "source"}`: `source` is `gateway` (its `read_file`) or `local_disk` (this Mac's own `~/.claude/plans/<name>.md`, read by the daemon when the gateway refuses it); `error` is set and `text` empty when neither worked, which is the case for every remote plan today. 404 when the path has no plan or is not in the latest sweep, or not a herdr session; 405 not GET.

### `POST /api/herdr/focus`

Body `{"path": "<review path>"}` (a pane file or its plan file). Focuses that pane in the owner's herdr TUI on this Mac: `herdr agent focus <pane_id>` for `local`, `herdr --machine <label> agent focus <pane_id>` for any other machine. It moves focus only and types nothing into the pane. 200 `{"path", "machine", "pane_id"}`; 400 bad body; 404 path not in the latest sweep or not a herdr session; 405 not POST; 502 `{"error": "<herdr's stderr>"}` when herdr fails (paused box, pane gone). Same `Sec-Fetch-Site` rule as every POST. It waits up to 20 s: a remote machine answers in about 1 s, a paused box hangs.

### `POST /api/herdr/send`

Body `{"path", "expect_state_change_seq", "text"}`, or `"keys": [...]`, or `"option": n` instead of `text` (exactly one). `path` is a pane file or its plan file; `expect_state_change_seq` is the seq of the sweep the owner was reading.

Before typing anything the daemon reads the pane fresh with `herdr [--machine <label>] agent get <pane>` and refuses with 409 when:

| Body | When |
|---|---|
| `{"error": "stale", "state_change_seq": N}` | the pane's seq is no longer `expect_state_change_seq`: it moved since that sweep |
| `{"error": "blocked"}` | `text` while a dialog is up: an enter would answer the dialog |
| `{"error": "working"}` | `keys` or `option` while the agent works |

Text goes out as one `herdr [--machine <label>] agent prompt <pane> <text>` (no `--wait`): a working Claude takes it mid-turn, Codex queues it. Keys go one `agent send-keys` call each, 150 ms apart, from a fixed allowlist (`enter esc up down left right tab shift+tab shift+left shift+right space backspace ctrl+] y n p 1-9`; no `ctrl+c`). `option` is turned into keys by the per-agent table in `internal/herdr/keys.go` (see `/api/herdr/keys`); the page does not offer option sends yet. A one-word text starting with `-` is a 400, because herdr would read it as a flag; `- a list` is fine.

200 `{"path", "machine", "pane_id", "sent": "text"|"keys", "state_change_seq_before"}`. Every send is appended to `herdr-sent.jsonl` in the review folder, and the item's `delivery` field shows it until the daemon restarts. 400 bad body or input; 404 path not in the latest sweep, or herdr no longer knows the pane; 405 not POST; 502 herdr failed. Same `Sec-Fetch-Site` rule as every POST.

After a 200 the page adds a reply "Sent to <machine> <pane> at HH:MM" to each comment it sent and resolves it, so a later Finish review does not send it again. The prompt is built by `composeMessage` in `web/herdr-mode.model.js`: one comment is its quoted lines (`> `, at most 6, cut at 200 characters) and its body; several are numbered in line order under `N review comments:`.

### `GET /api/herdr/keys?path=<review path>&option=<n>`

The keys `POST /api/herdr/send` would press for option `n` of the pane's parsed dialog, without pressing them. 200 `{"path", "machine", "pane_id", "option", "keys"}`; 400 when the table has no sequence for that agent, dialog kind or option (free-text options, pi); 404 path not in the latest sweep.

### `GET /api/herdr/transcript?path=<review path>[&since=<version>]`

The pane's Claude Code transcript, Codex rollout, or pi, opencode, grok or cursor-agent session, for the `/herdr` page. Claude panes anywhere; the others on this Mac only. Other agents, and those on a box, answer 404 with a reason.

How the pane is joined to its file, on every machine the same way and with nothing installed: `herdr [--machine <label>] pane process-info --pane <id>` lists the pane's foreground processes. The Claude one has `name`, `argv0` or `basename(argv[0])` equal to `claude` (locally `name` is the version, e.g. `2.1.300`, and the boxes' herdr sends no `argv0`); an interactive one is tried before a `claude -p` a tool started in the same process group, and a pid that is not a plain integer is skipped. Claude Code keeps `<config dir>/sessions/<pid>.json` with `sessionId` and `cwd`, where the config dir is any `~/.claude*` (here `~/.claude` and `~/.claude-work`) or, on this Mac, `$CLAUDE_CONFIG_DIR`. When two config dirs have a file for the pid, the newest wins (a killed claude leaves its file). The transcript is `<config dir>/projects/<cwd with every non-alphanumeric character as "-">/<sessionId>.jsonl`, else `<config dir>/projects/*/<sessionId>.jsonl` (a session resumed from another directory). Locally that is a file read. Elsewhere it is `ssh -T -o BatchMode=yes -o ConnectTimeout=8 <ssh_target> "sh -c '<script>'"` (sh, whatever the login shell is), the script built only from the pids (integers), the config dir name (`^\.claude[A-Za-z0-9._-]*$`), the slug (`^[A-Za-z0-9-]+$`), the session id (UUID) and a byte offset (integer). One script loops over `~/.claude*/sessions/<pid>.json` and prints each file's dir name and JSON. The other prints the file's path and size, then `gzip -c <file>` for the first read or `tail -c +<offset+1> <file> | gzip -c` for new bytes (everything again when the size is below the offset); the daemon gunzips. A path the box sends back is used only if it matches the same patterns. When the pid join finds no session, the session the sweep named is the fallback: herdr's `agent_session`, or an exited pane's `claude --resume <id>` line, looked up by id in every `~/.claude*`.

A Codex pane (on this Mac) is joined through the files its process holds open. Its process has `name`, `argv0` or `basename(argv[0])` equal to `codex` (the `node` launcher in front of it holds nothing). `lsof -nP -Fn -p <pids>` lists its open files; the rollouts are `…/sessions/YYYY/MM/DD/rollout-<time>-<thread uuid>.jsonl`. Codex keeps one open for the session and one for every sub-agent it spawned (a pane with three sub-agents holds four), so the pane's own is the one whose first line, `session_meta`, has the file's thread id, no `parent_thread_id` and a `thread_source` other than `subagent`; when two qualify the one written last wins. The cwd never picks a rollout: two Codex panes can share one. The page parses the rollout itself (`web/herdr-transcript-codex.js`) into the same turns and rows as a Claude transcript, from Codex's `event_msg` lines (`item_completed` items, `task_started`, `task_complete`, `turn_aborted`).

pi and opencode panes are joined through herdr's own `agent_session`, which herdr's integrations report for both and the sweep turns into the item's `transcript_ref`. For pi it is the session file (`kind: "path"`), used only when it is a clean absolute `.jsonl` path under `~/.pi/agent/sessions/`; the daemon reads it like a Claude transcript, continuing from the last offset, and the page parses it with `web/herdr-transcript-pi.js` (user, assistant and toolResult `message` lines; `compaction`; `custom_message` lines pi displays). For opencode it is the session id (`kind: "id"`, `^ses_[A-Za-z0-9]{8,64}$`). opencode keeps every session in one SQLite database, `$XDG_DATA_HOME/opencode/opencode.db` or `~/.local/share/opencode/opencode.db`, so the daemon runs `sqlite3 -readonly -bail -batch <db> <query>` with the checked id in the query. It prints one JSON object per line: the session (`{"k":"s"}`, title, directory, version), then every message (`{"k":"m"}`) and part (`{"k":"p"}`) of it in the order they were created, each with its `data` as opencode wrote it. A part changes in place while a turn streams, so every read is the whole session from byte 0 (a new `version` each time). The page parses it with `web/herdr-transcript-opencode.js`. grok and cursor-agent panes are joined the same way, by the session id herdr's integrations report (`kind: "id"`, a UUID): grok's chat history is the newest `<grok home>/sessions/*/<id>/chat_history.jsonl` (`$GROK_HOME`, else `~/.grok`), parsed by `web/herdr-transcript-grok.js` (user lines with a `prompt_index` are prompts, the rest is context grok adds; `assistant` text and `tool_calls`; `tool_result` joined by `tool_call_id`; reasoning summaries); cursor-agent's transcript is the newest `~/.cursor/projects/*/agent-transcripts/<id>/<id>.jsonl`, parsed by `web/herdr-transcript-cursor.js` (`role` user and assistant lines, `turn_ended` closing each turn). Both files are read whole on every read, since grok rewrites its history on a rewind (the biggest seen: 1.2 MB grok, 0.8 MB cursor). grok keeps no times; cursor-agent keeps only each prompt's `<timestamp>` and no tool output, so its calls say "output not kept". All four parsers send tool calls through one mapping to Claude's names and keys (`bash` → `Bash`, `filePath` or `path` → `file_path`, `oldString` → `old_string`, …), so the view draws and words them the same way.

One read takes at most 256 MB of transcript, counted after gunzip for a box, so a planted or broken file cannot fill the daemon's memory (the biggest real transcript is about 60 MB). What is sent: the transcript's lines in file order, minus `api-request`, `api-request-blob` and `api-request-shape` lines (request debug, most of the bytes); for a rollout, minus `response_item` (the model's own copy of the conversation), `token_usage_record`, `turn_context`, `world_state`, `inter_agent_communication_metadata`, `realtime_item`, `compacted` and `event_msg` `token_count` lines (60% and 75% of the bytes in the two rollouts measured); for a pi session, minus `{"type":"custom",…}` lines (extension state; one pi sub-agent registry snapshot is rewritten whole after every change, 84 of 88 MB in one session), with any JSON string longer than 64 KB cut to 64 KB plus ` [… N bytes cut]`. A string that long made only of base64 characters (a picture's data) keeps the note alone; the page gets the picture from `GET /api/herdr/image`. A last line with no newline yet (a write in progress) is held back until it is complete.

```jsonc
{
  "path": "local/wNR:pC.md", "machine": "local", "pane_id": "wNR:pC",
  "kind": "claude_jsonl",         // or "codex_rollout", "pi_jsonl", "opencode_sqlite", "grok_jsonl", "cursor_jsonl"
  "source": "local",               // or "ssh"
  "session_id": "c8533f45-a0d4-4f60-851f-a184947673e0",
  "file": "~/.claude/projects/-Users-ivor-src-tries-2026-08-26-relay/c8533f45-….jsonl",
  "bytes": 7512345,                // transcript bytes read, up to the last newline
  "dropped_bytes": 4500123,        // api-request* lines (a rollout: the lines above) left out
  "clipped": 3,                    // strings cut at 64 KB
  "version": "c8533f45-…:mfx2k9a1.3:2988114", // opaque: pass it back as since
  "from": 0,                       // where jsonl starts in the filtered text; 0 = all of it
  "jsonl": "{…}\n{…}\n",
  "fetched_at": "2026-10-16T01:02:03Z"
}
```

The version names the session, the read from byte 0 that the text continues, and the text's length. With `since` set to a `version` the page holds: `from` is that version's length and `jsonl` has only the lines added since (often empty). A different session, a file that shrank (read again from byte 0), a daemon restart, or a `since` that is no version sends everything with `from: 0`. The page replaces its text when `from` is 0 and appends otherwise.

| Status | When |
|---|---|
| 200 | the shape above |
| 404 | `{"error": "unknown pane"}`: not a herdr session, or the path is not in the latest sweep. `{"error": "no transcript", "reason": "…"}`: no transcript for this pane, the reason in plain words (`No gemini reader yet`, `No codex reader for workbox3 yet, only for codex panes on this Mac`, `No codex process in this pane`, `herdr reports no session file for this pi pane`, `herdr reports no session for this opencode pane`, `No messages yet in opencode session ses_…`, `No grok transcript yet for session …`, `No claude process in this pane`, `No session file for pid 1234 on workbox3`, `No transcript yet for session … on workbox3` for a claude with no prompt yet, `herdr no longer knows pane w3:p4 on workbox3`) |
| 405 | not GET |
| 502 | `{"error": "…"}`: the read failed (paused box, ssh error, herdr error, or a transcript read before that is gone), or one read passed 256 MB (`stopped reading the transcript at 256 MB, far past any real one`) |

It waits up to 20 s. On this Mac the join runs on every read (one herdr call, two small file reads). A cold read from a box is three calls and takes 5 to 7 s (process-info and the sessions script, about 3 s, then 2.5 to 4 s to fetch gzipped). A later read is one SSH call, about 2 s, most of it the login, run while herdr lists the pane's processes: it prints the pid's sessions file again, so a `/clear` (a new `sessionId` in the same file) or a new pid starts over, and fetches the bytes after the offset. Requests for one pane share one read, and a read that finished less than 3 s ago answers without reading again. The daemon keeps each read pane's text in memory and drops it when the pane leaves the sweep. After a sweep in which a box pane's `completion_seq` moved, a pane read before is refreshed in the background and `herdr-updated` is sent when that read succeeds.

### `GET /api/herdr/image?path=<review path>&uuid=<line uuid>&block=<i>|<i>.<j>`

One picture of the pane's transcript, decoded. `uuid` is the line's top-level `uuid`; `block` `i` is `message.content[i]`, a pasted image, and `i.j` is `content[j]` of the `tool_result` at `message.content[i]` (a Read of a PNG, a screenshot tool). The block must be `{"type": "image", "source": {"type": "base64", "media_type", "data"}}`. The daemon finds the line in the file the transcript join found (reading the transcript first when the pane has none in memory): locally a line-by-line read, on a box one SSH call, `grep -F -m 8 -e <uuid> <file> | gzip -c`, built only from the checked path and uuid. It takes the line whose own `uuid` matches (the next lines name it as `parentUuid`). Both reads stop at 256 MB like a transcript read.

| Status | When |
|---|---|
| 200 | the picture; `Content-Type` is its `media_type`, `X-Content-Type-Options: nosniff`, `Cache-Control: private, max-age=86400` |
| 400 | `uuid` is not a lowercase UUID, or `block` is not `<i>` or `<i>.<j>` (0 to 999) |
| 403 | `Sec-Fetch-Site` is `same-site` or `cross-site` (`same-origin`, `none` and no header pass) |
| 404 | `{"error": "unknown pane"}`; `{"error": "no transcript", "reason"}` as for the transcript; `{"error": "no image", "reason"}` when no line has that uuid or the block is not a base64 image |
| 405 | not GET |
| 413 | the picture decodes to more than 32 MB |
| 415 | `media_type` is not `image/png`, `image/jpeg`, `image/gif` or `image/webp` (SVG is never served), the data is not base64, or the bytes are another type than `media_type` says |
| 502 | the read failed (ssh, a transcript that is gone, the 256 MB cap) |

### `GET /api/herdr/file?path=<review path>&file=<absolute path>`

A picture an agent names by its absolute path in an answer (`![alt](/abs/path.png)`), read from the pane's machine. `file` must start with `/`, be at most 4096 bytes and hold no NUL or newline; it is cleaned with `path.Clean`. On this Mac it must be a regular file. On a box it is one SSH call, `f=<path>; [ -f "$f" ] || { echo "== missing"; exit 0; }; echo "== file"; head -c 33554433 "$f"`, and the path must match `^/[A-Za-z0-9._@+=,:/-]+$` so nothing in it reaches a shell as more than a word. The bytes are sniffed (`http.DetectContentType`) and only `image/png`, `image/jpeg`, `image/gif` and `image/webp` are sent, so no other file leaves the machine.

| Status | When |
|---|---|
| 200 | the picture; `Content-Type` from the sniff, `X-Content-Type-Options: nosniff`, `Cache-Control: no-cache` |
| 400 | `{"error": "bad file", "reason"}`: not absolute, too long, a NUL or newline, or on a box `this path has characters drover does not pass to ssh` |
| 403 | `Sec-Fetch-Site` is `same-site` or `cross-site`, as for `/api/herdr/image` |
| 404 | `{"error": "unknown pane"}`; `{"error": "no such file"}` (this Mac) or `{"error": "no such file on <machine>"}` |
| 405 | not GET |
| 413 | the file is over 32 MB |
| 415 | the bytes are not one of the four picture types |
| 502 | the read failed (ssh, permissions) |

### `POST /api/herdr/rename`

Body `{"path", "name"}` or `{"path", "clear": true}` (exactly one of the two): `herdr [--machine <label>] agent rename <pane_id> <name>` (or `--clear`). The name is trimmed and must then pass herdr's own rule, `^[a-z][a-z0-9_-]{0,31}$`; any other name is a 400 with herdr's sentence, `agent name must start with a lowercase letter and contain only lowercase letters, digits, '-' or '_' (1-32 characters)`, and herdr is not called. 200 `{"path", "machine", "pane_id", "name"}` (`name` is `""` after a clear); 400 bad body or name; 404 path not in the latest sweep, or not a herdr session; 405 not POST; 502 `{"error": "<herdr's stderr>"}` when herdr failed (paused box, no agent in the pane). It waits up to 20 s. The new name shows from the next sweep (`name` on the item).

### `POST /api/herdr/exit`

Body `{"path"}` (a pane file or its plan file). The page sends it only after its dialog is confirmed. The agent quits the way it quits itself, then its shell closes the pane:

1. `herdr [--machine <label>] agent get <pane>`, fresh. 409 with a sentence when the agent is `working` (stop it first) or `blocked` (answer the dialog first), or when drover has no measured exit for that agent; each says Close pane is the other way.
2. The agent's exit keys (`exitKeys` in `internal/herdr/close.go`), each step one `agent send-keys` call, steps 150 ms apart: `ctrl+d ctrl+d` in one call for claude and grok, which answer the first with "press again" (over `--machine` a call takes about a second, longer than Claude's window, so two calls failed on workbox5); `esc`, then `ctrl+d`, as two calls for opencode, whose `esc` closes the update notice it opens on start; one `ctrl+d` for codex, pi and cursor. ctrl+d quits only from an empty input line. With text typed it does nothing or deletes one character forward, so a draft is never sent the way it would be in front of a typed `/exit`.
3. `pane process-info` every 300 ms, for up to 12 s, until the foreground process group is the shell's (`foreground_process_group_id == shell_pid`). If it never is, 409 `"<agent> did not quit within 12s. …"` and nothing is typed into the shell.
4. `pane read <pane> --source recent-unwrapped --lines 40` for the resume line the agent printed (`claude --resume`, `codex resume`, `opencode -s`, `pi --session`, `grok --resume`; cursor-agent prints `agent --resume=<id>`, returned as `cursor-agent --resume=<id>` because `agent` on PATH is grok's). Falls back to the sweep's `resume_command`.
5. `pane run <pane> exit`, then `pane get` every 300 ms for up to 5 s until herdr answers `pane_not_found`. A shell that stays open (running jobs) is a 409 that still carries `resume_command`.

A pane whose agent has already left (`exited`, or herdr lists no agent) skips steps 1 and 2; when its shell is not in front either, 409 and nothing is typed.

200 `{"path", "machine", "pane_id", "agent", "resume_command"}`. `resume_command` is `""` when the agent printed none: Claude, opencode and pi print none for a session with no messages. The pane is dropped from the latest sweep at once (SSE `herdr-updated`) and a new sweep starts. A sweep that was already running was read before the pane closed, so the pane can come back for that one sweep before the new one drops it again. 400 bad body; 404 path not in the latest sweep, not a herdr session, or herdr no longer has the pane; 405 not POST; 409 as above, or an exit or close already running on that pane; 502 herdr failed. Locally it took 0.3 to 0.9 s, on workbox5 7.3 to 7.6 s; it waits up to about 20 s. bash closes the pane over a running background job and leaves the job running (orphaned); a stopped job makes it refuse the `exit`, which is the 409 above. Every call is appended to `herdr-sent.jsonl` as `{"kind": "exit", "time", "path", "machine", "pane"}` plus `resume_command` or `error`.

### `POST /api/herdr/close`

Body `{"path"}`: `herdr [--machine <label>] pane close <pane>`, which ends the pane and everything running in it. Nothing is checked first: the page's dialog is the check. 200 `{"path", "machine", "pane_id", "agent", "resume_command"}` (`resume_command` only for a pane the sweep saw as exited); 400 bad body; 404 as for exit; 405 not POST; 409 an exit or close already running on that pane; 502 herdr failed. It waits up to 20 s, is logged as `{"kind": "close", …}`, and drops the pane from the sweep like exit.

### Rounds, Finish and Approve

Delivery is per pane, so a herdr session has no review rounds: `POST /api/finish` and `POST /api/round-complete` answer 409, and the header's global Finish Review stays disabled (each pane header has its own Finish review, above). Without this guard an Approve would end the daemon and, with `cleanup_on_approve`, delete the review folder with every read mark. The round stays 1. "What changed" per pane is the read-mark baseline above.

### SSE `herdr-updated`

Sent after a sweep that changed any file or any item/machine field (timings excluded), after every `POST /api/herdr/viewed`, and after a background transcript refresh. No payload: re-fetch `/api/herdr` and `/api/session`, and the files. It is deliberately not `file-changed`, which ends a round and clears open comment forms.

## MachineStatus

| Field | Type | Meaning |
|---|---|---|
| `label` | string | herdr label (`local` for this Mac, else the saved profile label) |
| `ssh_target` | string? | profile's SSH target, e.g. `workdone-ib5` |
| `instabox` | string? | instabox box name (inferred `workdone-ibN` → `ibN`, or `herdr_instabox_boxes`) |
| `instabox_state` | string? | `running`, `suspended`, `stopped` |
| `state` | string | `reachable`, `paused` (not contacted), `unreachable`, `error` |
| `error` | string? | why, when not reachable |
| `gateway_error` / `discovery_error` | string? | one of the two sources failed on a reachable machine |
| `woken` | bool? | `--wake` resumed it this sweep |
| `ms` | int | time the machine took |
| `agents` | int | items from this machine |
| `shell_panes` | int | agent-less panes with no resume line |
| `uninspected_panes` | int | agent-less panes the gateway would not read (outside its allowedRoots); an exited agent there is missed |

## Item

| Field | Type | Meaning |
|---|---|---|
| `id` | string | `<machine>/<pane_id>` |
| `path` | string | review file path (`/api/herdr` only) |
| `viewed` | bool | owner marked the current turn read (`/api/herdr` only) |
| `plan_file` | string? | review path of the pane's plan file, when one is emitted (`/api/herdr` only) |
| `plan_viewed` | bool? | the plan file's own read mark, when `plan_file` is set (`/api/herdr` only) |
| `machine`, `instabox` | string | |
| `pane_id` | string | herdr pane id, unique per machine only |
| `agent` | string | `claude`, `codex`, `opencode`, `pi`, `cursor`, … |
| `name` | string? | herdr agent name |
| `status` | string | herdr's `idle` `working` `blocked` `done`, or `background`, `exited`, `unreadable_by_gateway` |
| `herdr_status` | string? | herdr's own `agent_status` |
| `cwd`, `title`, `workspace_id`, `tab_id` | string? | |
| `completion_seq` | int\|null | from herdr. Null for exited/background panes, and for hook-driven agents (opencode) where herdr itself reports null |
| `state_change_seq` | int\|null | from herdr. Null only for exited/background panes. Use this as the turn binding when `completion_seq` is null |
| `attention` | string? | `dialog` or `question` (gateway `attention.ts`) |
| `needs_you` | bool | `attention` is set |
| `dialog` | Dialog? | menu parsed by the gateway's `dialog.ts` |
| `dialog_screen` | string? | blocked but the menu could not be parsed: the screen |
| `plan` | `{path, text?, error?}`? | ExitPlanMode plan file. The gateway's `read_file` refuses it (`path_not_allowed`: its allowedRoots do not include `~/.claude/plans`), so local plans are read from this Mac's disk and remote plans carry `error` |
| `last` | LastText? | last answer |
| `transcript_ref` | `{kind, path?, session?}`? | `kind`: `claude_jsonl`, `cursor_jsonl`, `codex_rollout`, `opencode_sqlite`, `pi_jsonl`. Local Claude panes get a `path` checked in every `~/.claude*` and `$CLAUDE_CONFIG_DIR`; remote ones only `session` until the gateway sends `ref`. From herdr's `agent_session`: a local pi pane gets `pi_jsonl` with the session file as `path`; opencode, grok and cursor panes get `opencode_sqlite`, `grok_jsonl` and `cursor_jsonl` with the session id as `session` |
| `input_line` | `{text, ghost}`? | `ghost: true` = dim suggestion, never send; `false` = typed; `null` = cannot tell (gateway without ANSI reads, or mixed) |
| `resume_command` | string? | `exited` only: `claude --resume <id>`, `codex resume <id>`, `opencode -s ses_<id>` |
| `notes` | string[]? | what is missing and why, in plain words; show them |
| `delivery` | `{at, seq_before, kind, acknowledged}`? | the last `POST /api/herdr/send` to this pane since the daemon started; `acknowledged` once `state_change_seq` moved past `seq_before` (`/api/herdr` only) |

Dialog: `{dialog_id, kind, text, options: [{n, label, current?, checked?, free_text?}], multi, free_text, go_ahead}`. `kind` is `question`, `permission`, `gated`, or a go-ahead kind. The option buttons on the page stay disabled: answer a dialog in herdr (Focus), or through `POST /api/herdr/send` with `option`.

LastText: `{text, source: "transcript"|"screen", at?, truncated, in_progress?: {prompt, latest_text?, interrupted}}`. Screen text is the last 12 viewport lines with the input box cut off; earlier lines are lost.

## What degrades until the gateway branch is deployed

Detected per item and stated in `notes`:

- `input_line.ghost` is null everywhere: the deployed gateway strips ANSI on every read.
- Codex, opencode and pi answers in the sweep come from the screen: the gateway has no readers for them. The `/herdr` page reads a local Codex, pi, opencode, grok or cursor-agent pane's transcript itself (`GET /api/herdr/transcript`).
- Remote Claude panes answer from the screen: herdr's agent integration is not installed on ib1 to ib5, so herdr reports no `agent_session` and the gateway cannot join a transcript.
- Every `read_agent` the collector sends is written to the gateway's audit log.
- Only the last answer is shown. Earlier turns need a transcript read the gateway does not offer.
