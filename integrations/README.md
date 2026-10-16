# Drover Integrations

Drop-in configuration files that teach your AI coding tool to use Drover for reviewing plans and code changes.

This page is for you if you already use an AI coding agent and want it to hand its plans and diffs to Drover for review. You need the `drover` binary on your PATH first (see the [main README](../README.md)).

**Which do I need?**

- Claude Code or Codex, and you want plans reviewed automatically: use the plugin (the Claude Code plugin marketplace, or `drover install codex-plugin`). Both add a plan-mode hook.
- Any other supported tool: `drover install <tool>` adds the skills, and you ask for a review with `/crit` (or `$crit` in Codex).
- Not sure which tool name to pass: check the table below.

## Quick install

```bash
drover install <tool>     # Install for a specific tool in the current project
drover install all        # Install for all supported tools
```

Safe to re-run. Existing files are skipped (use `--force` to overwrite).

**Global install**: run `cd ~ && drover install <tool>` to install to your home directory. The integration is then available across all projects without per-project setup. Each tool reads from a different global path; `drover install` routes the files to the right place automatically.

| Tool | Install command | Project destination | Global destination |
|------|----------------|---------------------|--------------------|
| Claude Code | `drover install claude-code` | `.claude/skills/crit/SKILL.md` + `crit-cli` + `crit-story` | `~/.claude/skills/crit{,-cli,-story}/SKILL.md` |
| Cursor | `drover install cursor` | `.cursor/skills/crit/SKILL.md` + `crit-cli` + `crit-story` | (project only - Cursor has no stable user-level config dir) |
| GitHub Copilot | `drover install github-copilot` | `.github/skills/crit{,-cli,-story}/SKILL.md` | `~/.agents/skills/crit{,-cli,-story}/SKILL.md` |
| OpenCode | `drover install opencode` | `.opencode/commands/crit.md` + `crit-story.md` + skills + plugin | `~/.config/opencode/commands/` + `~/.agents/skills/` + plugins |
| Codex | `drover install codex` | `.agents/skills/crit{,-cli,-story}/SKILL.md` | `~/.agents/skills/crit{,-cli,-story}/SKILL.md` |
| Codex plugin | `drover install codex-plugin` | loose skills + marketplace + `plugins/crit/` (incl. crit-story) | `~/.agents/` + `~/.codex/plugins/crit/` |
| Pi | `drover install pi` | `.pi/skills/crit{,-cli,-story}/SKILL.md` | `~/.pi/agent/skills/crit{,-cli,-story}/SKILL.md` |
| OMO | `drover install omo` | `.omo/skills/crit{,-cli,-story}/SKILL.md` | `~/.omo/agent/skills/crit{,-cli,-story}/SKILL.md` |
| omp (Oh My Pi) | `drover install omp` | `.omp/skills/crit{,-cli,-story}/SKILL.md` | `~/.omp/agent/skills/crit{,-cli,-story}/SKILL.md` |
| Qwen Code | `drover install qwen` | `.qwen/skills/crit{,-cli,-story}/SKILL.md` | `~/.qwen/skills/crit{,-cli,-story}/SKILL.md` |
| Hermes | `drover install hermes` | `.hermes/skills/crit{,-cli,-story}/SKILL.md` (add `.hermes/skills` to `external_dirs`) | `~/.hermes/skills/crit{,-cli,-story}/SKILL.md` |
| Windsurf | `drover install windsurf` | `.windsurf/workflows/crit.md` + `crit-story.md` + skills | `~/.codeium/windsurf/global_workflows/` + skills |
| Cline | `drover install cline` | `.clinerules/workflows/crit.md` + `crit-story.md` + skills | `~/.cline/data/workflows/` + `~/.cline/skills/` |
| Aider | `drover install aider` | `.crit/aider-conventions.md` + adds entry under `read:` in `.aider.conf.yml` | `~/.crit-conventions.md` + adds entry under `read:` in `~/.aider.conf.yml` |
| Gemini CLI | `drover install gemini` | `crit-cli` + `crit-story` skills + `/crit` + `/crit-story` commands + policy | same under `~/.gemini/` |
| Grok | `drover install grok` | `.grok/skills/crit{,-cli,-story}/SKILL.md` | `~/.grok/skills/crit{,-cli,-story}/SKILL.md` |
| Amp | `drover install ampcode` | `.agents/skills/crit{,-cli,-story}/SKILL.md` | `~/.config/agents/skills/crit{,-cli,-story}/SKILL.md` |

## Plugin marketplace (Claude Code)

For the full experience, install via the plugin marketplace. This gives you:
- A `/crit` slash command for the review loop
- A model-discoverable `crit-cli` skill for review files, `drover comment`, `drover pull/push`, etc.
- A wording-gated `/crit-story` skill for chaptered diff overviews that then continues the `/crit` review loop

```
claude plugin marketplace add tomasz-tomczyk/crit
claude plugin install crit@crit
```

The marketplace manifest lives at the repo root (`.claude-plugin/marketplace.json`) and points to the plugin files in `integrations/claude-code/`.

### `drover install` vs plugin marketplace

| | `drover install claude-code` | Plugin marketplace |
|---|---|---|
| **Scope** | Per-project (committed to repo), or user-wide with `cd ~ && drover install claude-code` | User-wide |
| **What's installed** | `/crit`, `crit-cli` and `crit-story` skills | `/crit`, `crit-cli` and `/crit-story`, plus the plan-mode review hook |
| **Plan mode** | Ask for the review yourself with `/crit` | Hook intercepts `ExitPlanMode` and opens Drover for review automatically |
| **Good for** | Teams - everyone gets the integration when they clone | Individual users - works across all projects |
| **Setup** | Run once per project | Install once, works everywhere |

Both give you the `/crit` review loop and the `crit-cli` skill, which teaches the agent `drover comment`, the review file format, `drover pull/push` and resolving comments without opening the browser loop. Only the plugin marketplace adds the plan-mode hook.

## Claude Code plan approval mode

The Claude Code plugin intercepts `ExitPlanMode` with a narrowly matched
`PermissionRequest` hook. By default, approving in Drover allows the plan exit and
leaves Claude Code to restore its existing permission mode. To choose the mode
deterministically, set `plan_approve_mode` in your global Drover config:

```json
{
  "plan_approve_mode": "acceptEdits"
}
```

Disable automatic plan review per shell or globally with
`export CRIT_PLAN_REVIEW=off`. This only disables the plan-exit hook; manually
running `drover plan` still opens a review.

Supported values are `default`, `manual`, `acceptEdits`, `plan`, `auto`,
`dontAsk`, and `bypassPermissions`. The `manual` alias requires Claude Code
2.1.200 or newer. On approval, Drover returns Claude Code's documented
`decision.updatedPermissions` entry:

```json
{
  "type": "setMode",
  "mode": "acceptEdits",
  "destination": "session"
}
```

`destination: "session"` keeps the change in memory for the current Claude Code
session only. The setting is global-only (`~/.crit.config.json`); a repository's
`.crit.config.json` cannot change your permission policy. Unset preserves the
default hook behavior, and invalid values are ignored with a warning.

The hook approval and mode switch do not override matching deny or ask rules.
Claude Code can also disable `auto` through `permissions.disableAutoMode`.
`bypassPermissions` is intentionally dangerous and should only be used in an
isolated environment. Claude Code applies it only when the session started with
bypass mode available (for example `--allow-dangerously-skip-permissions` or
`--dangerously-skip-permissions`) and managed settings have not disabled it;
otherwise Claude Code treats the update as a no-op.

## OpenCode plugin: conditional sharing instructions

`drover install opencode` also writes a small TypeScript plugin (`crit.ts`) and registers it in `opencode.jsonc`. The plugin shells out to `drover config` on each chat turn and appends sharing instructions to the system prompt only when `share_url` is set. With `share_url: ""` the sharing block is omitted entirely - useful in environments with strict information-sharing policies, and saves tokens otherwise. opencode auto-loads `.ts` files dropped into the plugin directory, so the registration entry is informational.

## Codex plugin

For the full Codex experience, install the plugin. This gives you:

- A `$crit` skill for the review loop (plus loose copies under `.agents/skills/` so bare `$crit` works even outside the plugin)
- A `crit-cli` skill that auto-activates when working with review files, `drover comment`, `drover pull/push`, etc.
- A **proposed-plan review hook** - intercepts Codex's `Stop` hook when the agent proposes a plan in Plan mode, writes it to disk, and opens Drover for inline review before the turn ends

```bash
cd ~ && drover install codex-plugin    # global (recommended)
drover install codex-plugin            # per-project (commit plugins/crit/ for the whole team)
```

`drover install codex-plugin` registers the plugin in a local Codex marketplace (`.agents/plugins/marketplace.json` or `~/.agents/plugins/marketplace.json`), copies plugin files to `plugins/crit/` (project) or `~/.codex/plugins/crit/` (global), enables the plugin in `~/.codex/config.toml`, and turns on `features.plugins`, `features.hooks`, and `features.plugin_hooks`.

Plugin source files live in `integrations/codex/plugin/crit/`. See [`integrations/codex/README.md`](./codex/README.md) for layout and manual setup.

### `drover install codex` vs `drover install codex-plugin`

| | `drover install codex` | `drover install codex-plugin` |
|---|---|---|
| **Scope** | Skills only (project or global) | Skills + Codex plugin + plan hook |
| **What's installed** | `$crit` and `crit-cli` skills under `.agents/skills/` | Same skills, plus `plugins/crit/` (or `~/.codex/plugins/crit/`) with bundled skills and a `Stop` hook |
| **Plan mode** | Agent must write the plan to a file before `$crit` works | Hook captures in-chat proposed plans (`<proposed_plan>`) and reviews them automatically |

Both approaches give you `$crit` and the `crit-cli` skill. Only `codex-plugin` adds the proposed-plan hook - without it, typing `$crit` on an in-chat plan (e.g. after choosing "No and stay in Plan Mode") does nothing useful because there is no file path for `drover` to open.

Disable automatic plan review per shell or globally with
`export CRIT_PLAN_REVIEW=off`. Manual `drover plan` invocations are unaffected.

## OMO

```bash
drover install omo                    # project-local skills
cd ~ && drover install omo            # global skills
```

Invoke `/skill:crit` to start a review loop or `/skill:crit-story` to author
a story and review it. OMO also accepts `$crit` and `$crit-story`. The
`crit-cli` skill is model-discoverable for headless comments and CLI operations.
Project-local skills require OMO's project trust.

The interactive skills use OMO's asynchronous monitor to run Drover and receive
completion notifications when the human clicks Finish Review, rather than
holding an eval cell open or polling the review file.

## omp (Oh My Pi)

```bash
drover install omp                    # project-local skills
cd ~ && drover install omp            # global skills
```

Restart omp after installation to discover the skills. Invoke `/skill:crit`
to start a review loop or `/skill:crit-story` to author a story and review it.
The `crit-cli` skill is model-discoverable for headless comments and CLI
operations; interactive reviews require an explicit request to use Drover.

The review skills use omp's asynchronous `bash` jobs with `timeout: 0`, so
human reviews have no command deadline. Completion notifications deliver the
feedback after Finish Review; `wait` blocks when no independent work remains.
When async jobs are disabled, the skills use foreground `bash` with the same
disabled deadline. Replies use `--author 'omp'` and leave resolution to the
reviewer unless the user explicitly asks otherwise.

## Invocation policy

The interactive `drover` review cycle is wording-gated, not hard-disabled.
Skill descriptions and workflow docs tell agents to launch Drover only when the
user invokes the platform command (`/crit`, `$crit`, `/skill:crit`, `/crit.md`,
or Windsurf's `/crit`, as appropriate) or directly asks to use Drover. A normal
request to review code, a plan, a diff, a PR, or a page does not count.

`crit-cli` is intentionally model-discoverable. It teaches agents how to leave
and reply to Drover comments, interpret review JSON, share reviews, and synchronize
GitHub PR feedback, but it does not start the interactive review cycle.

`crit-story` is wording-gated like `/crit`: agents author a chaptered story
overview only when the user invokes `/crit-story` (or the tool equivalent) or
directly asks to generate a drover story. After ingest they reconnect with bare
`drover` and continue the same wait → address → next-round cycle. It does not run
as part of a normal review request.

The only automatic interactive path is a lifecycle hook immediately after
planning mode. The Claude Code plugin, Codex plugin, and Gemini CLI integration
retain their existing plan-exit hooks.

## What these do

All integrations follow the same pattern:

1. **Pick the review target** - current git changes by default, or an explicit file, plan, PR, or commit range when the user names one
2. **Launch Drover** - the agent runs the matching `drover` command to open the review in your browser
3. **Address feedback** - after review, the agent reads the review file to find your inline comments and revises the target
4. **Continue the review loop** - the agent reruns the printed next-round command until you finish with no unresolved comments

Each integration also teaches the agent about:
- **`drover comment`** - leave inline review comments programmatically without opening the browser
- **review file format** - how to read comments, resolve them with threaded replies
- **`drover pull/push`** - sync reviews with GitHub PRs (push supports `--event approve|request-changes|comment`)

## Next steps

- Run a first review end to end: [crit.md/getting-started](https://crit.md/getting-started).
- Share a review with someone who doesn't have Drover installed: [crit.md/features/share-reviews](https://crit.md/features/share-reviews).
- Something here out of date or wrong for your tool? [Open an issue](https://github.com/tomasz-tomczyk/crit/issues). Include the tool, its version and the command you ran.
