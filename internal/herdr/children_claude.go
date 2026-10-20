package herdr

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Claude Code's children of a pane (children.go): subagents from the lead's
// subagents/ folder, teammates from its team config and the lead's tmux server.

// claudeSubagents are the lead's Agent-tool subagents:
// <project>/<session>/subagents/agent-<id>.jsonl, each beside a .meta.json with
// agentType, description, model and finished.
func claudeSubagents(lead, session string, now time.Time) []Child {
	dir := filepath.Join(filepath.Dir(lead), session, "subagents")
	metas, _ := filepath.Glob(filepath.Join(dir, "agent-*.meta.json"))
	var out []Child
	for _, mp := range metas {
		id := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(mp), "agent-"), ".meta.json")
		var meta struct {
			AgentType   string `json:"agentType"`
			Description string `json:"description"`
			Model       string `json:"model"`
			Finished    bool   `json:"finished"`
		}
		data, err := os.ReadFile(mp)
		if !agentIDRE.MatchString(id) || err != nil || json.Unmarshal(data, &meta) != nil {
			continue
		}
		ch := Child{ID: "sa:" + id, Kind: "subagent", Name: meta.Description, Type: meta.AgentType, Model: meta.Model,
			Status: "working", Reason: "running", file: filepath.Join(dir, "agent-"+id+".jsonl"), ref: refClaudeJSONL, session: "agent-" + id}
		if ch.Name == "" {
			ch.Name = "subagent " + id
		}
		if meta.Finished || claudeEnded(ch.file) {
			ch.Status, ch.Reason = "finished", "finished its task"
		}
		out = append(out, observed(ch, now))
	}
	return out
}

// claudeEnded reports a transcript whose last conversation line, in its last
// 256 KB, is an assistant message that ended its turn. Only some Claude Code
// versions write finished to a subagent's meta (3 of 40 seen on this Mac); a
// finished one's transcript always ends this way, before hook and request
// records.
func claudeEnded(p string) bool {
	f, err := os.Open(p)
	if err != nil {
		return false
	}
	defer f.Close()
	if fi, err := f.Stat(); err == nil && fi.Size() > 256<<10 {
		_, _ = f.Seek(-256<<10, io.SeekEnd)
	}
	data, _ := io.ReadAll(io.LimitReader(f, 256<<10))
	ended := false
	for _, line := range bytes.Split(data, []byte("\n")) {
		var l struct {
			Type    string `json:"type"`
			Message struct {
				StopReason string `json:"stop_reason"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &l) != nil || (l.Type != "assistant" && l.Type != "user") {
			continue
		}
		ended = l.Type == "assistant" && (l.Message.StopReason == "end_turn" || l.Message.StopReason == "stop_sequence")
	}
	return ended
}

// team is the part of <config>/teams/<team>/config.json the join reads.
type team struct {
	Name          string       `json:"name"`
	CreatedAt     int64        `json:"createdAt"`
	LeadSessionID string       `json:"leadSessionId"`
	Members       []teamMember `json:"members"`
}

type teamMember struct {
	Name        string `json:"name"`
	AgentType   string `json:"agentType"`
	Model       string `json:"model"`
	Prompt      string `json:"prompt"`
	TmuxPaneID  string `json:"tmuxPaneId"`
	BackendType string `json:"backendType"`
	IsActive    bool   `json:"isActive"`
	CWD         string `json:"cwd"`
}

// findTeam is the agent team the session leads. Claude names it
// session-<first 8 of the id>, so that config is read first, then the rest.
func findTeam(root, session string) (team, bool) {
	configs, _ := filepath.Glob(filepath.Join(root, "teams", "*", "config.json"))
	if len(session) >= 8 {
		configs = append([]string{filepath.Join(root, "teams", "session-"+session[:8], "config.json")}, configs...)
	}
	for _, p := range configs {
		var t team
		data, err := os.ReadFile(p)
		if err == nil && json.Unmarshal(data, &t) == nil && t.LeadSessionID == session {
			return t, true
		}
	}
	return team{}, false
}

// claudeTeammates are the members of the team the lead's session leads. A tmux
// teammate's state is its pane's title in the lead's tmux server, `tmux -L
// claude-swarm-<lead pid>`, read with herdr's own title rules.
func (c *Controller) claudeTeammates(ctx context.Context, run runner, root, lead, session string, leadPID int, now time.Time) []Child {
	t, ok := findTeam(root, session)
	if !ok {
		return nil
	}
	panes, tmuxErr := swarmPanes(ctx, run, leadPID, t)
	files := c.teammateFiles(root, filepath.Dir(lead), t)
	var out []Child
	for _, mb := range t.Members {
		if mb.AgentType == "team-lead" || !teammateRE.MatchString(mb.Name) {
			continue
		}
		ch := Child{ID: "tm:" + mb.Name, Kind: "teammate", Name: mb.Name, Task: firstLine(mb.Prompt), Model: mb.Model,
			file: files[mb.Name], ref: refClaudeJSONL}
		ch.session = strings.TrimSuffix(filepath.Base(ch.file), ".jsonl")
		ch.Status, ch.Reason, ch.Where, ch.Attach = teammateState(mb, panes, tmuxErr, leadPID)
		out = append(out, observed(ch, now))
	}
	return out
}

// teammateState is a teammate's status and reason, and for a tmux teammate
// where it runs and the command that shows it live. With no tmux state (an
// in-process teammate, or the lead's pid unknown) isActive is all there is.
func teammateState(mb teamMember, panes map[string]swarmPane, tmuxErr string, leadPID int) (status, reason, where, attach string) {
	status, reason = "idle", "waiting for a message"
	if mb.BackendType != "tmux" || (panes == nil && tmuxErr == "") {
		if mb.IsActive {
			status, reason = "working", "active in the team"
		}
		return status, reason, "", ""
	}
	where = "tmux " + mb.TmuxPaneID
	p, found := panes[mb.TmuxPaneID]
	switch {
	case tmuxErr != "":
		return "gone", tmuxErr, where, ""
	case !found:
		return "gone", "its tmux pane " + mb.TmuxPaneID + " is gone", where, ""
	case p.dead:
		return "gone", "its process has exited", where, ""
	case workingTitleRE.MatchString(p.title):
		status, reason = "working", "working"
	case !idleTitleRE.MatchString(p.title):
		reason = "not working"
	}
	return status, reason, where, "tmux -L claude-swarm-" + strconv.Itoa(leadPID) + " attach -t " + mb.TmuxPaneID
}

type swarmPane struct {
	dead  bool
	title string
}

// swarmPanes lists the panes of the lead's tmux server, when the team has tmux
// members. why says what went wrong when the server could not be read; both
// are empty when there is nothing to read or no pid to name the server by.
func swarmPanes(ctx context.Context, run runner, leadPID int, t team) (panes map[string]swarmPane, why string) {
	if leadPID <= 0 || !slices.ContainsFunc(t.Members, func(mb teamMember) bool { return mb.BackendType == "tmux" }) {
		return nil, ""
	}
	socket := "claude-swarm-" + strconv.Itoa(leadPID)
	out, err := run(ctx, nil, []string{"tmux", "-L", socket, "list-panes", "-a", "-F", "#{pane_id}\t#{pane_dead}\t#{pane_title}"})
	if err != nil {
		return nil, "its tmux server " + socket + " is not running"
	}
	panes = map[string]swarmPane{}
	for _, line := range strings.Split(string(out), "\n") {
		if f := strings.SplitN(line, "\t", 3); len(f) == 3 {
			panes[f[0]] = swarmPane{dead: f[1] == "1", title: f[2]}
		}
	}
	return panes, ""
}

// teammateFiles maps each teammate of t to its own session transcript: a JSONL
// in its project dir, written since the team was made, whose first lines carry
// t's teamName and the member's agentName. The map is kept for the daemon's
// life and looked for again while a member is missing from it.
func (c *Controller) teammateFiles(root, leadDir string, t team) map[string]string {
	key := root + "|" + t.Name + "|" + strconv.FormatInt(t.CreatedAt, 10)
	c.kidsMu.Lock()
	known := c.teamFiles[key]
	c.kidsMu.Unlock()
	missing := false
	dirs := []string{leadDir}
	for _, mb := range t.Members {
		if mb.AgentType != "team-lead" && known[mb.Name] == "" {
			missing = true
		}
		if d := filepath.Join(root, "projects", claudeSlug(mb.CWD)); mb.CWD != "" && !slices.Contains(dirs, d) {
			dirs = append(dirs, d)
		}
	}
	if !missing {
		return known
	}
	found := map[string]string{}
	since := time.UnixMilli(t.CreatedAt)
	for _, d := range dirs {
		files, _ := filepath.Glob(filepath.Join(d, "*.jsonl"))
		for _, f := range files {
			if fi, err := os.Stat(f); err != nil || fi.ModTime().Before(since) {
				continue
			}
			if tn, an := teamOf(f); tn == t.Name && an != "" && found[an] == "" {
				found[an] = f
			}
		}
	}
	c.kidsMu.Lock()
	c.teamFiles[key] = found
	c.kidsMu.Unlock()
	return found
}

// teamOf is the teamName and agentName in a transcript's first lines.
func teamOf(p string) (teamName, agentName string) {
	f, err := os.Open(p)
	if err != nil {
		return "", ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 8<<20)
	for i := 0; i < 12 && sc.Scan(); i++ {
		var l struct {
			TeamName  string `json:"teamName"`
			AgentName string `json:"agentName"`
		}
		if json.Unmarshal(sc.Bytes(), &l) == nil && l.TeamName != "" && l.AgentName != "" {
			return l.TeamName, l.AgentName
		}
	}
	return "", ""
}
