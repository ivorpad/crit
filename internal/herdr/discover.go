package herdr

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// herdrAgent is one entry of `herdr agent list` (or the agent fields of `pane list`).
// herdr is the discovery source: the gateway hides panes outside its allowedRoots,
// and only herdr reports completion_seq and state_change_seq.
type herdrAgent struct {
	PaneID         string        `json:"pane_id"`
	Agent          *string       `json:"agent"`
	Name           *string       `json:"name"`
	AgentStatus    string        `json:"agent_status"`
	CWD            string        `json:"cwd"`
	ForegroundCWD  string        `json:"foreground_cwd"`
	Title          *string       `json:"terminal_title_stripped"`
	WorkspaceID    string        `json:"workspace_id"`
	TabID          string        `json:"tab_id"`
	CompletionSeq  *int64        `json:"completion_seq"`
	StateChangeSeq *int64        `json:"state_change_seq"`
	AgentSession   *agentSession `json:"agent_session"`
}

type agentSession struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

func (a herdrAgent) cwd() string {
	if a.ForegroundCWD != "" {
		return a.ForegroundCWD
	}
	return a.CWD
}

// labelRE keeps a machine label from being read as a flag by the herdr CLI.
var labelRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// herdrArgv runs a read-only herdr API command on the machine: locally, or through
// herdr's own machine forwarding (`--machine <label>`, the saved SSH profile).
func herdrArgv(m machine, args ...string) ([]string, error) {
	if m.Target == "" {
		return append([]string{"herdr"}, args...), nil
	}
	if !labelRE.MatchString(m.Label) {
		return nil, fmt.Errorf("unsafe machine label %q", m.Label)
	}
	return append([]string{"herdr", "--machine", m.Label}, args...), nil
}

// discover lists the machine's agents (with seqs) and its panes (including those
// whose agent is gone) straight from herdr.
func discover(ctx context.Context, run runner, m machine) (agents, panes []herdrAgent, err error) {
	type res struct {
		list []herdrAgent
		err  error
	}
	get := func(kind, key string) <-chan res {
		ch := make(chan res, 1)
		go func() {
			argv, err := herdrArgv(m, kind, "list")
			if err != nil {
				ch <- res{err: err}
				return
			}
			out, err := run(ctx, nil, argv)
			if err != nil {
				ch <- res{err: err}
				return
			}
			var body struct {
				Result map[string]json.RawMessage `json:"result"`
				Error  *struct {
					Code, Message string
				} `json:"error"`
			}
			if err := json.Unmarshal(out, &body); err != nil {
				ch <- res{err: fmt.Errorf("parsing herdr %s list: %w", kind, err)}
				return
			}
			if body.Error != nil {
				ch <- res{err: fmt.Errorf("herdr %s list: %s: %s", kind, body.Error.Code, body.Error.Message)}
				return
			}
			var list []herdrAgent
			if err := json.Unmarshal(body.Result[key], &list); err != nil {
				ch <- res{err: fmt.Errorf("parsing herdr %s list: %w", kind, err)}
				return
			}
			ch <- res{list: list}
		}()
		return ch
	}
	a, p := get("agent", "agents"), get("pane", "panes")
	ra, rp := <-a, <-p
	if ra.err != nil {
		return nil, nil, ra.err
	}
	if rp.err != nil {
		return nil, nil, rp.err
	}
	return ra.list, rp.list, nil
}

// resumeRE matches the line an agent CLI prints when it exits, telling how to come
// back (herdr-tui-control.md S21, G12, S15, and §4 "Leaving a pane" for pi, grok
// and cursor-agent).
var resumeRE = regexp.MustCompile(`(?m)\b(claude --resume [0-9a-f-]{36}|codex resume [0-9A-Za-z-]{8,}|opencode -s ses_[0-9A-Za-z]+|pi --session [0-9a-f-]{36}|grok --resume [0-9a-f-]{36}|agent --resume=[0-9a-f-]{36})`)

// resumeCommand returns the last resume line on a pane's screen and the agent that
// printed it. cursor-agent prints `agent --resume=<id>`, but `agent` on the Mac and
// the workboxes is grok's (its installer ran last), so that one comes back as
// `cursor-agent --resume=<id>`.
func resumeCommand(screen string) (cmd, kind string, ok bool) {
	all := resumeRE.FindAllString(screen, -1)
	if len(all) == 0 {
		return "", "", false
	}
	cmd = all[len(all)-1]
	if strings.HasPrefix(cmd, "agent ") {
		return "cursor-" + cmd, "cursor", true
	}
	kind, _, _ = strings.Cut(cmd, " ")
	return cmd, kind, true
}
