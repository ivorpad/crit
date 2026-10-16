# Drover - Manual Review Workflow

Use this workflow only when the user explicitly asks to use Drover. A generic
request to review code, a plan, a diff, a PR, or a page does not authorize
launching Drover.

## Review with Crit

After the user explicitly asks to use Drover, launch it:

```bash
drover $PLAN_FILE                       # Review a specific file
drover                                  # Review all changed files in the repo
drover --pr <num|url>                   # Review a GitHub PR (range mode)
drover --mr <iid|url>                   # Review a GitLab MR (range mode)
drover --range <baseSHA>..<headSHA>     # Review a commit range (range mode)
```

**CRITICAL — you MUST run `drover` and block until it completes.**

`drover` starts the daemon if needed, opens the browser, and blocks until the user clicks "Finish Review". It prints the review URL on startup (e.g. `Started drover daemon at http://localhost:<port>`) — relay that URL verbatim.

- Do NOT proceed until `drover` completes.
- Do NOT ask the user to type anything.
- Do NOT read the review file early.

## After review

When `drover` completes, read **stdout** and follow its instructions. Check **stderr** for `approved: true` or `approved: false`.

Field guidance:
- `quote`: the specific text the reviewer selected — focus changes on the quoted text rather than the whole range.
- `anchor`: full text of the commented lines when placed — locate content by anchor, line numbers may be stale.
- `drifted: true`: original content was removed or heavily rewritten — line numbers are approximate at best.

For each unresolved comment:
1. Revise the referenced file using your edit tools.
2. Reply with what you did: `drover comment --reply-to <id> --author 'Aider' '<what you did>'` (markdown supported).
3. **Never pass `--resolve`** unless the user explicitly asks. Resolving is the reviewer's call.

When replying to multiple comments, use `--json`:

```bash
echo '[
  {"reply_to": "c_a1b2c3", "body": "Fixed"},
  {"reply_to": "c_d4e5f6", "body": "Refactored as suggested"}
]' | drover comment --json --author 'Aider'
```

For multi-paragraph reply bodies, prefer `--file <path>`. Raw newlines inside JSON strings are invalid, and shell heredocs make it easy to slip one in:

```bash
cat > /tmp/replies.json <<'EOF'
[
  {"reply_to": "c_a1b2c3", "body": "Fixed.\n\nDetails: split helper, added null guard."}
]
EOF
drover comment --json --file /tmp/replies.json --author 'Aider'
```

`--file -` reads stdin (same as the default).

## Next round

The finish prompt on stdout includes the command to run again — use it to start a new round.

Then tell the user only that you replied in Drover and that they can click Finish Review when ready. Do not repeat your replies or list the comments in chat. The reviewer reads them in Drover.

`drover` automatically signals round-complete, then blocks until the next "Finish Review" click. Only proceed after the user approves (a round finishes with zero comments).

## CLI Reference

### `drover comment`

```bash
drover comment --author 'Aider' '<body>'                       # Review-level
drover comment --author 'Aider' <path> '<body>'                # File-level
drover comment --author 'Aider' <path>:<line> '<body>'         # Line
drover comment --author 'Aider' <path>:<start>-<end> '<body>'  # Line range
drover comment --author 'Aider' --selector '<css>' [--route <path>] '<body>'  # Live or HTML preview pin
drover comment --reply-to <id> --author 'Aider' '<body>'       # Reply (c_… or r_…)
```

Hard rules:
- Always pass `--author 'Aider'`.
- Always single-quote the body — double quotes break on backticks and shell metachars.
- Line numbers reference the file on disk (1-indexed), not diff line numbers.
- Reply bodies support markdown.
- Only pass `--resolve` when the user explicitly asks.

If `drover comment` errors with "comment found in multiple files", disambiguate with `--path src/foo.go`.

### Bulk `--json`

For 3+ comments, prefer `--json` (atomic, single write). Synopsis:

```
drover comment --json [--file <path>] [--author <name>]
```

Stdin form (short single-line bodies):

```bash
echo '[
  {"body": "overall feedback", "scope": "review"},
  {"path": "session.go", "body": "restructure", "scope": "file"},
  {"file": "src/auth.go", "line": 42, "body": "Missing null check"},
  {"file": "src/auth.go", "line": "50-55", "body": "Extract to helper"},
  {"reply_to": "c_a1b2c3", "body": "Fixed — added null check"}
]' | drover comment --json --author 'Aider'
```

`--file <path>` form — preferred whenever a body has paragraph breaks (raw newlines in a JSON string are invalid):

```bash
cat > /tmp/crit-bulk.json <<'EOF'
[
  {"file": "src/auth.go", "line": 42, "body": "Para 1.\n\nPara 2."}
]
EOF
drover comment --json --file /tmp/crit-bulk.json --author 'Aider'
```

Scope inference: `reply_to` → reply; no `file`/`line` → review; `path` only → file; `path` + `line` → line.

### Sharing

```bash
drover share <file> [file...]                          # Upload and print URL
drover share --qr <file>                               # Also print QR code (terminal only)
drover share --org <slug> <file>                       # Share under an organization
drover share --org <slug> --visibility unlisted <file> # Org share with explicit visibility
drover unpublish [file...]                              # Remove shared review
```

Always relay the full output (URL, QR) directly in your response — don't make the user dig through tool output.
- **`--org <slug>`** shares under an organization. Visibility defaults to `organization` (members only). Override with `--visibility` (`organization`, `unlisted`, `public`).

### GitHub PR / GitLab MR sync

```bash
drover pull [number|url]                                   # Fetch PR/MR comments
drover push [--dry-run] [--event <type>] [-m <msg>] [pr]   # Post review as PR review
```

Requires `gh` CLI. `--event`: `comment` (default), `approve`, `request-changes`.
