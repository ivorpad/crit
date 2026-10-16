---
name: crit-cli
description: Use when an agent needs to author or reply to drover inline comments programmatically (including multi-agent workflows commenting on shared code/plans/docs/proposals), publish or unpublish a drover review with drover share, sync a drover review to or from a GitHub PR or GitLab MR, or read/interpret a drover review JSON file. Covers drover comment, drover share, drover unpublish, drover pull, drover push, review file format, and resolution workflow. Not for invoking an interactive review loop — that's the `/crit` command.
user-invocable: false
---

# Drover CLI Reference

> If a plan was just written and the user said `/crit` or `drover`, invoke the `/crit` command — do not use this reference skill. This skill covers CLI operations like `drover comment`, `drover pull/push`, and `drover share`.

Comments have three scopes:

- **Line comments** (`scope: "line"`) — tied to specific lines, stored in `files.<path>.comments`
- **File comments** (`scope: "file"`) — about a file overall, stored in `files.<path>.comments` with `start_line: 0`
- **Review comments** (`scope: "review"`) — general feedback, stored in the top-level `review_comments` array

The review file path is shown by `drover status`.

## Reading comments

When `drover` completes a review round, read **stdout** and follow its instructions. Unresolved comments are often embedded in that prompt as JSON. Check **stderr** for `approved: true` or `approved: false`.

When you need to read comments separately:

```bash
drover comments            # human-readable, unresolved only (default)
drover comments --json     # flat JSON for agents
drover comments --all      # include resolved comments
drover comments --plan <slug>   # plan reviews
drover comments [path]     # explicit review.json or .crit directory
```

Review-level comments are listed first — easy to miss in raw `review.json`. Uses the same review resolution as `drover comment` (`--output`, `--plan`, daemon session).

## Multiple active sessions

When more than one review session matches the current directory and branch, headless commands (`drover comment`, `drover comments`, `drover share`, `drover push`, `drover pull`) refuse to guess. Run `drover status` (or `drover status --json`) to list every active session, then target the intended review with `--session <id>`:

```bash
drover comment --session <id> --author <name> <path>:<line> <body>
drover comment --session <id> --json --file comments.json --author <name>
drover comments --session <id>
drover share --session <id> <file>
drover push --session <id>
drover pull --session <id>
```

The JSON status output exposes the candidates in `sessions`.



<important if="you are reading or parsing the review file">

```json
{
  "review_comments": [
    {
      "id": "r_f1e2d3",
      "body": "Overall the architecture looks good",
      "scope": "review",
      "author": "User Name",
      "resolved": false,
      "replies": [
        { "id": "rp_b4a5c6", "body": "Thanks, addressed the minor issues", "author": "Claude" }
      ]
    }
  ],
  "files": {
    "path/to/file.go": {
      "comments": [
        {
          "id": "c_a1b2c3",
          "start_line": 5,
          "end_line": 10,
          "body": "Comment text",
          "quote": "the specific words selected",
          "anchor": "The sessions table needs a complete rewrite...",
          "author": "User Name",
          "resolved": false,
          "replies": [
            { "id": "rp_c7d8e9", "body": "Fixed by extracting to helper", "author": "Claude" }
          ]
        }
      ]
    }
  }
}
```

Field rules:
- `resolved`: `false` or **missing** — both mean unresolved. Only `true` means resolved.
- `quote` (optional): the specific text the reviewer selected — narrows scope within the line range. Focus changes on the quoted text rather than the entire range.
- `anchor` (line comments): full text of the commented lines when placed. When edits shift line numbers, locate content by anchor rather than trusting `start_line`/`end_line`.
- `drifted: true`: original content was removed or heavily rewritten — line numbers are approximate at best.
- Unresolved comments may have `replies` — read them before acting.
</important>

<important if="you are authoring or replying to comments via drover comment">

```bash
# Review-level (general feedback)
drover comment --author 'Claude Code' '<body>'

# File-level (whole file, no line numbers)
drover comment --author 'Claude Code' <path> '<body>'

# Line (single line or range)
drover comment --author 'Claude Code' <path>:<line> '<body>'
drover comment --author 'Claude Code' <path>:<start>-<end> '<body>'

# DOM pin (live review or HTML preview). File:line is rejected there.
drover comment --author 'Claude Code' --selector '<css>' '<body>'
drover comment --author 'Claude Code' --selector '<css>' --route <path> '<body>'

# Reply to an existing comment
drover comment --reply-to <id> --author 'Claude Code' '<body>'
```

Hard rules:
- **Always pass `--author 'Claude Code'`** (or your agent name) so comments are attributed correctly.
- **Always single-quote the body** — double quotes break on backticks and shell metachars.
- **Line numbers reference the file on disk** (1-indexed), not diff line numbers.
- **Reply bodies support markdown** — use code fences and inline code where helpful.
- **Only pass `--resolve` when the user explicitly asks.** Never resolve proactively.
- **Live reviews and HTML preview** do not take `<path>:<line>`. Pin with `--selector <css>`. `--route` is the page path (default `/` for live, `/preview-content` for preview). In `--json`, use `"selector"` and `"route"`.
</important>

<important if="you are leaving 3+ comments in one operation">

Use `--json` for atomicity (single write, no partial state) and speed (one process). Two ways to feed the JSON:

```bash
# Short, single-line bodies — pipe via stdin:
echo '[
  {"body": "overall feedback", "scope": "review"},
  {"path": "session.go", "body": "restructure", "scope": "file"},
  {"file": "src/auth.go", "line": 42, "body": "Missing null check"},
  {"file": "src/auth.go", "line": "50-55", "body": "Extract to helper"},
  {"reply_to": "c_a1b2c3", "body": "Fixed — added null check"},
  {"reply_to": "r_f1e2d3", "body": "Done"}
]' | drover comment --json --author 'Claude Code'
```

**Prefer `--file <path>` for any multi-paragraph body.** Shell-quoted JSON breaks the moment a `"body"` string contains a raw newline — JSON forbids them, and the shell happily passes them through. Use the Write tool to author the JSON to a temp file, then point drover at it:

```bash
# After Write-ing /tmp/replies.json:
drover comment --json --file /tmp/replies.json --author 'Claude Code'
```

`--file -` reads stdin (same as omitting the flag).

Per-entry schema:

| Field | Type | Required | Notes |
|---|---|---|---|
| `file` / `path` | string | line/file comments | Relative path. `path` alone (no `line`) → file-level. |
| `line` | int/string | line comments | `42` or `"45-47"` |
| `end_line` | int | optional | Defaults to `line` |
| `body` | string | always | |
| `author` | string | optional | Per-entry override; falls back to `--author` |
| `scope` | string | optional | `"review"` / `"file"` — usually inferred |
| `reply_to` | string | replies | Comment ID (`c_…` or `r_…`) |
| `resolve` | bool | optional | Only when user explicitly asks |
| `quote` | string | optional | Line comments only: the exact text the comment is about, when it is part of the lines |
| `quote_offset` | int | optional | Where `quote` starts in the lines' text, whitespace collapsed (as the web UI records it); needs `quote` |

Scope inference (when `scope` omitted): has `reply_to` → reply; no `file`/`path` and no `line` → review-level; `path` but no `line` → file-level; `file`/`path` + `line` → line.
</important>

<important if="drover comment errored with 'comment found in multiple files'">
Comment IDs are unique per session, but the same ID can collide across files. Disambiguate with `--path`:

```bash
drover comment --reply-to c_a1b2c3 --path src/auth.go --author 'Claude Code' 'Fixed the null check'
```

In `--json` mode, set the `file` field on the entry. Review-level IDs (`r_…`) are globally unique and never need this.
</important>

<important if="you are responding to plan-mode comments (review file under ~/.crit/plans/)">
Plan reviews (via `drover plan` or the ExitPlanMode hook) store the review file in `~/.crit/plans/<slug>/`. **Always pass `--plan <slug>`** — without it, `drover comment` looks in the project root and won't find the comments. The slug is shown in the review feedback prompt.

```bash
drover comment --plan my-plan-2026-03-23 --reply-to c_a1b2c3 --author 'Claude Code' 'Updated the plan'
```
</important>

<important if="you are syncing with a GitHub PR or GitLab MR (pull or push)">

```bash
drover pull [number|url]                                   # Fetch PR/MR review comments into the review file
drover push [--dry-run] [--event <type>] [-m <msg>] [n]    # Post review comments to a PR/MR
drover pull --forge gitlab 42                              # Force GitLab when auto-detect is ambiguous
```

Requires `gh` (GitHub) or `glab` (GitLab) installed and authenticated. Change number is auto-detected from the current branch when possible. Set `"forge"` / `"gitlab_url"` in config for self-managed hosts, or pass `--forge`.

`--event` values: `comment` (default), `approve`, `request-changes`. `-m` adds a review-level body message.
</important>

<important if="the user asked to share, get a URL, get a QR code, or unpublish a review">

```bash
drover share <file> [file...]                          # Upload and print URL
drover share --share-url <url> <file>                  # Target a specific Drover deployment
drover share --qr <file>                               # Also print QR code (terminal only)
drover share --org <slug> <file>                       # Share under an organization
drover share --org <slug> --visibility unlisted <file> # Org share with explicit visibility
drover unpublish [file...]                              # Remove shared review
drover unpublish --share-url <url> [file...]            # Unpublish from a specific deployment
```

- **No server needed** — reads files directly from disk. If a review file exists, comments for the shared files are included automatically.
- **Always relay the output** — copy the URL (and QR if used) into your response. Don't make the user dig through tool output.
- **`--qr` is terminal-only** — skip in mobile apps, web chat UIs, or anywhere Unicode block characters won't render correctly.
- **`--org <slug>`** shares under an organization. Visibility defaults to `organization` (members only). Override with `--visibility` (`organization`, `unlisted`, `public`).
- **`--share-url`** selects among configured `share_targets` (or overrides for one invocation). Required when multiple targets are configured and none is marked default. Empty `CRIT_SHARE_URL=` disables sharing for the process.
- **Unpublish uses the persisted delete token** in the review file — no extra args needed unless targeting a non-default deployment with `--share-url`.
</important>
