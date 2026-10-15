# herdr control center API

`crit herdr` runs one daemon for every herdr machine. The review page is crit's normal code-review page with `session.review_type === "herdr"`: each agent pane is a synthetic markdown file, grouped by machine in the file tree. `POST /api/herdr/send` is the only route that types into a pane, one pane per call. The page calls it only on a click: **Send now** on a comment form sends that comment, and a pane header's **Finish review** sends every unresolved comment on that pane as one prompt. Other panes are never touched by either.

Fixture: `web/__tests__/fixtures/herdr-sweep.json`, the /api/herdr shape built from a real sweep on 2026-10-15 (step-1 collector, so `completion_seq`/`state_change_seq` are null and there is no dialog, exited or unreadable item in it).

## Running it

```bash
crit herdr                 # start or attach to the daemon, open the page; Ctrl+C stops the daemon
crit herdr --no-open -p 3000
crit herdr --print         # one sweep as text, no daemon
crit herdr --json          # one sweep as JSON (the collector's Snapshot, not /api/herdr)
crit herdr --json --wake   # resume paused instabox boxes first (the daemon never wakes boxes)
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

Both sit behind the usual readiness gate (503 until the first sweep has built the session) and the `Sec-Fetch-Site` check.

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

### Rounds, Finish and Approve

Delivery is per pane, so a herdr session has no review rounds: `POST /api/finish` and `POST /api/round-complete` answer 409, and the header's global Finish Review stays disabled (each pane header has its own Finish review, above). Without this guard an Approve would end the daemon and, with `cleanup_on_approve`, delete the review folder with every read mark. The round stays 1. "What changed" per pane is the read-mark baseline above.

### SSE `herdr-updated`

Sent after a sweep that changed any file or any item/machine field (timings excluded), and after every `POST /api/herdr/viewed`. No payload: re-fetch `/api/herdr` and `/api/session`, and the files. It is deliberately not `file-changed`, which ends a round and clears open comment forms.

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
| `transcript_ref` | `{kind, path?, session?}`? | `kind`: `claude_jsonl`, `cursor_jsonl`, `codex_rollout`, `opencode_sqlite`, `pi_jsonl`. Local Claude panes get a checked `path`; remote ones only `session` until the gateway sends `ref` |
| `input_line` | `{text, ghost}`? | `ghost: true` = dim suggestion, never send; `false` = typed; `null` = cannot tell (gateway without ANSI reads, or mixed) |
| `resume_command` | string? | `exited` only: `claude --resume <id>`, `codex resume <id>`, `opencode -s ses_<id>` |
| `notes` | string[]? | what is missing and why, in plain words; show them |
| `delivery` | `{at, seq_before, kind, acknowledged}`? | the last `POST /api/herdr/send` to this pane since the daemon started; `acknowledged` once `state_change_seq` moved past `seq_before` (`/api/herdr` only) |

Dialog: `{dialog_id, kind, text, options: [{n, label, current?, checked?, free_text?}], multi, free_text, go_ahead}`. `kind` is `question`, `permission`, `gated`, or a go-ahead kind. The option buttons on the page stay disabled: answer a dialog in herdr (Focus), or through `POST /api/herdr/send` with `option`.

LastText: `{text, source: "transcript"|"screen", at?, truncated, in_progress?: {prompt, latest_text?, interrupted}}`. Screen text is the last 12 viewport lines with the input box cut off; earlier lines are lost.

## What degrades until the gateway branch is deployed

Detected per item and stated in `notes`:

- `input_line.ghost` is null everywhere: the deployed gateway strips ANSI on every read.
- Codex, opencode and pi answers come from the screen: no transcript readers.
- Remote Claude panes answer from the screen: herdr's agent integration is not installed on ib1 to ib5, so herdr reports no `agent_session` and the gateway cannot join a transcript.
- Every `read_agent` the collector sends is written to the gateway's audit log.
- Only the last answer is shown. Earlier turns need a transcript read the gateway does not offer.
