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

// herdrRead runs a read-only herdr API command on the machine and returns the
// result object of its answer.
func herdrRead(ctx context.Context, run runner, m machine, args ...string) (map[string]json.RawMessage, error) {
	what := strings.Join(args, " ")
	argv, err := herdrArgv(m, args...)
	if err != nil {
		return nil, err
	}
	out, err := run(ctx, nil, argv)
	if err != nil {
		return nil, err
	}
	var body struct {
		Result map[string]json.RawMessage `json:"result"`
		Error  *struct {
			Code, Message string
		} `json:"error"`
	}
	if err := json.Unmarshal(out, &body); err != nil {
		return nil, fmt.Errorf("parsing herdr %s: %w", what, err)
	}
	if body.Error != nil {
		return nil, fmt.Errorf("herdr %s: %s: %s", what, body.Error.Code, body.Error.Message)
	}
	return body.Result, nil
}

// refusedByHerdr tells a request herdr's server answered with an error (herdr
// prints {"error":{"code":…}} and exits 1) from a call that never reached it,
// which fails in plain text.
func refusedByHerdr(err error) bool {
	return strings.Contains(err.Error(), `{"error":{"code":`)
}

// discover lists the machine's agents (with seqs) and its panes (including those
// whose agent is gone) straight from herdr, in one `herdr api snapshot`, which
// holds both lists. Every herdr --machine call is two SSH sessions, and DSM on
// the NAS logs each session as a login. A herdr that refuses the snapshot is
// asked for the two lists.
func discover(ctx context.Context, run runner, m machine) (agents, panes []herdrAgent, err error) {
	res, err := herdrRead(ctx, run, m, "api", "snapshot")
	if err != nil {
		if refusedByHerdr(err) {
			return discoverLists(ctx, run, m)
		}
		return nil, nil, err
	}
	var s struct {
		Agents []herdrAgent `json:"agents"`
		Panes  []herdrAgent `json:"panes"`
	}
	if err := json.Unmarshal(res["snapshot"], &s); err != nil {
		return nil, nil, fmt.Errorf("parsing herdr api snapshot: %w", err)
	}
	return s.Agents, s.Panes, nil
}

// discoverLists is discover through `herdr agent list` and `herdr pane list`.
func discoverLists(ctx context.Context, run runner, m machine) (agents, panes []herdrAgent, err error) {
	type res struct {
		list []herdrAgent
		err  error
	}
	get := func(kind, key string) <-chan res {
		ch := make(chan res, 1)
		go func() {
			result, err := herdrRead(ctx, run, m, kind, "list")
			var list []herdrAgent
			if err == nil {
				if err = json.Unmarshal(result[key], &list); err != nil {
					err = fmt.Errorf("parsing herdr %s list: %w", kind, err)
				}
			}
			ch <- res{list: list, err: err}
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
