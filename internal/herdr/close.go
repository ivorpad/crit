package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// exitKeys quits each agent CLI from its input line, as measured in the lab
// (herdr-tui-control.md §4, "Leaving a pane"). Each inner list goes out as one
// `agent send-keys` call, the calls keyGap apart. Claude Code and grok answer a
// first ctrl+d with "press again", so both of theirs go in one call: over
// --machine a call takes about a second, longer than Claude's window on
// workbox5. ctrl+d quits only an empty input. With text typed it does nothing
// or deletes forward, so a draft is never sent, where /exit typed in after it
// would be. opencode gets esc first, in a call of its own (esc with a key right
// behind it reads as alt+key), to close the update notice it opens on start,
// which herdr reads as idle; esc does nothing on its idle input.
var exitKeys = map[string][][]string{
	"claude":   {{"ctrl+d", "ctrl+d"}},
	"grok":     {{"ctrl+d", "ctrl+d"}},
	"codex":    {{"ctrl+d"}},
	"pi":       {{"ctrl+d"}},
	"opencode": {{"esc"}, {"ctrl+d"}},
	"cursor":   {{"ctrl+d"}},
}

// How long Exit waits and how often it looks. cursor-agent took 1.5 to 3 s to
// leave after its key and the others under 1 s; herdr dropped the pane 0.22 s
// after its shell's exit.
const (
	exitWait  = 12 * time.Second
	shellWait = 5 * time.Second
	exitPoll  = 300 * time.Millisecond
)

// endRequest is the body of POST /api/herdr/exit and POST /api/herdr/close.
type endRequest struct {
	Path string `json:"path"`
}

// ExitJSON is POST /api/herdr/exit: the agent quits through its own keys, then
// `exit` in its shell closes the pane, answered as HTTP status and body. Nothing
// goes into the shell until herdr shows the shell in front again, and the resume
// line the agent printed is read before the pane goes.
func (c *Controller) ExitJSON(ctx context.Context, body []byte) (int, []byte) {
	it, m, run, release, err := c.endTarget(body)
	if err != nil {
		return marshalStatus(refusalOf(err))
	}
	defer release()
	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	res, err := c.exitPane(callCtx, run, it, m)
	return c.ended(it, "exit", res, err)
}

// CloseJSON is POST /api/herdr/close: `herdr pane close`, which ends the pane and
// everything running in it. The page asks the owner first; nothing else is checked.
func (c *Controller) CloseJSON(ctx context.Context, body []byte) (int, []byte) {
	it, m, run, release, err := c.endTarget(body)
	if err != nil {
		return marshalStatus(refusalOf(err))
	}
	defer release()
	callCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	argv, err := herdrArgv(m, "pane", "close", it.PaneID)
	if err == nil {
		_, err = run(callCtx, nil, argv)
	}
	var res map[string]any
	if err == nil {
		res = map[string]any{"path": itemPath(it), "machine": it.Machine, "pane_id": it.PaneID, "agent": it.Agent, "resume_command": it.ResumeCommand}
	}
	return c.ended(it, "close", res, err)
}

// endTarget finds the pane an exit or close names and marks it in progress, so a
// second click while the first runs is refused. The caller runs release.
func (c *Controller) endTarget(body []byte) (Item, machine, runner, func(), error) {
	var req endRequest
	if err := json.Unmarshal(body, &req); err != nil || req.Path == "" {
		return Item{}, machine{}, nil, nil, refuse(http.StatusBadRequest, `body must be {"path": "<review path>"}`)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	it, ok := c.itemAt(req.Path)
	if !ok {
		return Item{}, machine{}, nil, nil, refuse(http.StatusNotFound, ErrNoPane.Error())
	}
	if !paneIDRE.MatchString(it.PaneID) {
		return Item{}, machine{}, nil, nil, refuse(http.StatusBadRequest, fmt.Sprintf("unsafe pane id %q", it.PaneID))
	}
	key := itemPath(it)
	if c.ending[key] {
		return Item{}, machine{}, nil, nil, refuse(http.StatusConflict, "drover is already closing this pane")
	}
	c.ending[key] = true
	m := machine{Label: it.Machine}
	if it.Machine != localLabel {
		m.Target = it.Machine // any non-empty target makes herdrArgv use --machine <label>
	}
	release := func() {
		c.mu.Lock()
		delete(c.ending, key)
		c.mu.Unlock()
	}
	return it, m, c.col.run, release, nil
}

// exitPane runs Exit's steps on one pane: the agent quits, its resume line is
// read, then `exit` goes into the shell and herdr drops the pane.
func (c *Controller) exitPane(ctx context.Context, run runner, it Item, m machine) (map[string]any, error) {
	agent, err := c.quitAgent(ctx, run, it, m)
	if err != nil {
		return nil, err
	}
	resume := readResume(ctx, run, m, it)
	if err := closeShell(ctx, run, m, it.PaneID); err != nil {
		var r *refusal
		if errors.As(err, &r) {
			r.body["resume_command"] = resume
		}
		return nil, err
	}
	return map[string]any{"path": itemPath(it), "machine": it.Machine, "pane_id": it.PaneID, "agent": agent, "resume_command": resume}, nil
}

// quitAgent presses the agent's exit keys and waits until the shell is in front
// again. With no agent left in the pane it only checks the shell. It returns the
// agent herdr named.
func (c *Controller) quitAgent(ctx context.Context, run runner, it Item, m machine) (string, error) {
	fresh, err := agentGet(ctx, run, m, it.PaneID)
	var r *refusal
	if errors.As(err, &r) && r.status == http.StatusNotFound {
		// The agent has left already; only its shell is there.
		front, err := shellInFront(ctx, run, m, it.PaneID)
		if err == nil && !front {
			err = refuse(http.StatusConflict, "herdr lists no agent in this pane and its shell is not in front: something else runs there. Nothing was typed. Close pane ends it.")
		}
		return it.Agent, err
	}
	if err != nil {
		return "", err
	}
	agent := it.Agent
	if fresh.Agent != nil && *fresh.Agent != "" {
		agent = *fresh.Agent
	}
	steps, err := exitKeysFor(agent, fresh.AgentStatus)
	if err != nil {
		return "", err
	}
	pressErr := c.pressExit(ctx, run, m, it.PaneID, steps)
	// A second ctrl+d can miss an agent that left on the first; the shell check
	// decides.
	front, err := waitFor(ctx, exitWait, func() (bool, error) { return shellInFront(ctx, run, m, it.PaneID) })
	switch {
	case err != nil:
		return "", err
	case !front && pressErr != nil:
		return "", pressErr
	case !front:
		return "", refuse(http.StatusConflict, fmt.Sprintf("%s did not quit within %s. ctrl+d quits only from an empty input line: clear what is typed there, or close a menu that is open, in herdr (Focus), or use Close pane. Nothing was typed into the shell.", agent, exitWait))
	}
	return agent, nil
}

// exitKeysFor is the agent's exit keys, or why Exit refuses it in this status.
func exitKeysFor(agent, status string) ([][]string, error) {
	switch status {
	case "working":
		return nil, refuse(http.StatusConflict, agent+" is working. Stop it in herdr first, or use Close pane.")
	case "blocked":
		return nil, refuse(http.StatusConflict, agent+" is waiting on a dialog. Answer it first, or use Close pane.")
	}
	keys, ok := exitKeys[agent]
	if !ok {
		return nil, refuse(http.StatusConflict, fmt.Sprintf("drover has no measured way to quit %q. Close pane ends it.", agent))
	}
	return keys, nil
}

// pressExit sends each step of an exit sequence as one `agent send-keys` call,
// keyGap apart.
func (c *Controller) pressExit(ctx context.Context, run runner, m machine, paneID string, steps [][]string) error {
	for i, keys := range steps {
		if i > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(c.keyGap):
			}
		}
		argv, err := herdrArgv(m, append([]string{"agent", "send-keys", paneID}, keys...)...)
		if err != nil {
			return err
		}
		if _, err := run(ctx, nil, argv); err != nil {
			return fmt.Errorf("exit keys %v: %w", keys, err)
		}
	}
	return nil
}

// readResume is the resume line the agent left on screen, else the one the
// sweep saw.
func readResume(ctx context.Context, run runner, m machine, it Item) string {
	argv, err := herdrArgv(m, "pane", "read", it.PaneID, "--source", "recent-unwrapped", "--lines", "40")
	if err != nil {
		return it.ResumeCommand
	}
	out, err := run(ctx, nil, argv)
	if err != nil {
		return it.ResumeCommand
	}
	if cmd, _, ok := resumeCommand(string(out)); ok {
		return cmd
	}
	return it.ResumeCommand
}

// closeShell types exit into the pane's shell and waits for herdr to drop the
// pane.
func closeShell(ctx context.Context, run runner, m machine, paneID string) error {
	argv, err := herdrArgv(m, "pane", "run", paneID, "exit")
	if err != nil {
		return err
	}
	if _, err := run(ctx, nil, argv); err != nil && !strings.Contains(err.Error(), "pane_not_found") {
		return err
	}
	gone, err := waitFor(ctx, shellWait, func() (bool, error) { return paneGone(ctx, run, m, paneID) })
	if err == nil && !gone {
		err = refuse(http.StatusConflict, "The agent quit, but its shell is still open after exit: it may have jobs running. Close pane ends it.")
	}
	return err
}

// ended answers an exit or close as HTTP. Whatever reached herdr is logged next
// to the sends; a pane that went is dropped from the latest sweep at once and a
// new sweep is asked for.
func (c *Controller) ended(it Item, kind string, res map[string]any, err error) (int, []byte) {
	entry := map[string]any{
		"time": c.col.now().UTC().Format(time.RFC3339), "path": itemPath(it), "machine": it.Machine,
		"pane": it.PaneID, "kind": kind,
	}
	if err != nil {
		entry["error"] = err.Error()
	} else if cmd, _ := res["resume_command"].(string); cmd != "" {
		entry["resume_command"] = cmd
	}
	c.logSent(entry)
	if err != nil {
		return marshalStatus(refusalOf(err))
	}
	c.forget(it)
	return marshalStatus(&refusal{http.StatusOK, res})
}

// forget drops a closed pane from the latest sweep and publishes that, so the
// page loses it now instead of at the next sweep, then asks for that sweep.
func (c *Controller) forget(it Item) {
	c.mu.Lock()
	if c.snap != nil {
		// A copy: publish reads the old snapshot outside the lock.
		s := *c.snap
		s.Items = nil
		for _, x := range c.snap.Items {
			if x.ID != it.ID {
				s.Items = append(s.Items, x)
			}
		}
		c.snap = &s
	}
	sess := c.sess
	c.mu.Unlock()
	if sess != nil {
		c.publish(sess)
	}
	select {
	case c.kick <- struct{}{}:
	default:
	}
}

// refusalOf is err as the refusal the handler answers: its own, or 502 for
// herdr's failures.
func refusalOf(err error) *refusal {
	var r *refusal
	if errors.As(err, &r) {
		return r
	}
	return refuse(http.StatusBadGateway, err.Error())
}

// waitFor polls cond every exitPoll until it holds or limit passes; false with a
// nil error is the limit.
func waitFor(ctx context.Context, limit time.Duration, cond func() (bool, error)) (bool, error) {
	deadline := time.Now().Add(limit)
	for {
		ok, err := cond()
		if err != nil || ok {
			return ok, err
		}
		if time.Now().After(deadline) {
			return false, nil
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(exitPoll):
		}
	}
}

// shellInFront reports whether the pane's shell has the terminal again, from
// `herdr pane process-info`: an agent, or any command started at the prompt,
// runs in a process group of its own, so the shell is in front when the
// foreground group is the shell's.
func shellInFront(ctx context.Context, run runner, m machine, paneID string) (bool, error) {
	argv, err := herdrArgv(m, "pane", "process-info", "--pane", paneID)
	if err != nil {
		return false, err
	}
	out, err := run(ctx, nil, argv)
	if err != nil {
		// herdr prints its error JSON on stderr, which the runner puts in err.
		if strings.Contains(err.Error(), "pane_not_found") {
			return false, refuse(http.StatusNotFound, fmt.Sprintf("herdr no longer has pane %s on %s", paneID, m.Label))
		}
		return false, err
	}
	var body struct {
		Result struct {
			ProcessInfo struct {
				ShellPID int64 `json:"shell_pid"`
				FgPGID   int64 `json:"foreground_process_group_id"`
			} `json:"process_info"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &body); err != nil {
		return false, fmt.Errorf("parsing herdr pane process-info: %w", err)
	}
	p := body.Result.ProcessInfo
	return p.ShellPID > 0 && p.ShellPID == p.FgPGID, nil
}

// paneGone reports whether herdr has dropped the pane (`pane get` answers
// pane_not_found).
func paneGone(ctx context.Context, run runner, m machine, paneID string) (bool, error) {
	argv, err := herdrArgv(m, "pane", "get", paneID)
	if err != nil {
		return false, err
	}
	if _, err := run(ctx, nil, argv); err != nil {
		if strings.Contains(err.Error(), "pane_not_found") {
			return true, nil
		}
		return false, err
	}
	return false, nil
}
