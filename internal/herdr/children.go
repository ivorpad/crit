package herdr

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// A pane's agent can run others that herdr never lists: Claude Code's subagents
// (the Agent tool, inside the lead's process) and teammates (agent teams, each a
// Claude Code of its own in a tmux server the lead started), and Codex's
// sub-agents (threads in Codex's daemon). They are read from the files the lead
// leaves (herdr-tui-control.md §4, "Sub-agents and teammates"), on this Mac only.

// staleAfter is how long a child that is still working can go without writing
// to its transcript before it is called stale.
const staleAfter = 10 * time.Minute

// Child is one sub-agent of a pane's agent, as GET /api/herdr/children lists it.
type Child struct {
	ID         string `json:"id"`             // sa:<agent id>, tm:<name>, cx:<thread>
	Kind       string `json:"kind"`           // subagent, teammate, codex_subagent
	Name       string `json:"name"`           // a subagent's description, a teammate's or Codex nickname
	Type       string `json:"type,omitempty"` // a subagent's agent type, a Codex child's role
	Task       string `json:"task,omitempty"` // a teammate's prompt (first line), a Codex child's agent path
	Model      string `json:"model,omitempty"`
	Status     string `json:"status"` // working, idle, finished, stale, gone
	Reason     string `json:"reason"` // the status in a few words
	Where      string `json:"where,omitempty"`
	Attach     string `json:"attach,omitempty"` // a tmux teammate: the command that shows it live
	LastOutput string `json:"last_output_at,omitempty"`
	Started    string `json:"started_at,omitempty"`
	Transcript bool   `json:"transcript"`

	file    string // its transcript on this Mac
	ref     string // refClaudeJSONL or refCodexRollout
	session string // names its text in transcript versions
}

// ChildSummary is what each sweep puts on a pane's item for the strip.
type ChildSummary struct {
	Count   int `json:"count"`
	Working int `json:"working"`
	Stale   int `json:"stale"`
	Gone    int `json:"gone"`
}

var (
	// herdr's own Claude title rules (agent-detection claude.toml): a braille or
	// half-circle spinner is working, ✳ is idle.
	workingTitleRE = regexp.MustCompile(`^[\x{2800}-\x{28FF}\x{25D0}-\x{25D3}] `)
	idleTitleRE    = regexp.MustCompile(`^\x{2733} `)
	agentIDRE      = regexp.MustCompile(`^[0-9a-f]{6,40}$`)
	teammateRE     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)
)

// statusRank orders the list: what needs the owner first.
var statusRank = map[string]int{"stale": 0, "gone": 1, "working": 2, "idle": 3, "finished": 4}

// children lists the pane's sub-agents, stale and gone ones first.
func (c *Controller) children(ctx context.Context, run runner, m machine, it Item) ([]Child, error) {
	if m.Label != localLabel {
		return nil, &noTranscript{"Sub-agents are read on this Mac only so far"}
	}
	now := c.col.now()
	var kids []Child
	switch it.Agent {
	case "claude":
		j, lead, err := claudeJoin(ctx, run, m, it)
		if err != nil {
			return nil, err
		}
		if lead == "" {
			return nil, nil
		}
		root := j.dir
		if root == "" {
			root = filepath.Dir(filepath.Dir(filepath.Dir(lead))) // <root>/projects/<slug>/<session>.jsonl
		}
		kids = append(claudeSubagents(lead, j.session, now), c.claudeTeammates(ctx, run, root, lead, j.session, j.pid, now)...)
	case "codex":
		_, _, thread, err := codexJoin(ctx, run, m, it)
		if err != nil {
			return nil, err
		}
		if kids, err = codexChildren(ctx, run, thread, now); err != nil {
			return nil, err
		}
	default:
		return nil, nil
	}
	slices.SortStableFunc(kids, func(a, b Child) int { return statusRank[a.Status] - statusRank[b.Status] })
	return kids, nil
}

// observed adds what the child's transcript file says: when it last wrote and
// when it started. A working child that has written nothing for staleAfter is
// stale; an idle one is waiting and never stale.
func observed(ch Child, now time.Time) Child {
	fi, err := os.Stat(ch.file)
	if ch.file == "" || err != nil || !fi.Mode().IsRegular() {
		if ch.Status == "working" {
			ch.Reason = "running, no transcript yet"
		}
		return ch
	}
	ch.Transcript = true
	ch.LastOutput = fi.ModTime().UTC().Format(time.RFC3339)
	if ch.Started == "" {
		ch.Started = firstStamp(ch.file)
	}
	if quiet := now.Sub(fi.ModTime()); ch.Status == "working" && quiet >= staleAfter {
		ch.Status, ch.Reason = "stale", fmt.Sprintf("working, no output for %d min", int(quiet.Minutes()))
	}
	return ch
}

// firstStamp is the first "timestamp" in the transcript's first lines.
func firstStamp(p string) string {
	f, err := os.Open(p)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 8<<20)
	for i := 0; i < 5 && sc.Scan(); i++ {
		var l struct {
			Timestamp string `json:"timestamp"`
		}
		if json.Unmarshal(sc.Bytes(), &l) == nil && l.Timestamp != "" {
			return l.Timestamp
		}
	}
	return ""
}

// firstLine is the first line of a prompt, cut at 200 characters.
func firstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	if r := []rune(s); len(r) > 200 {
		s = string(r[:199]) + "…"
	}
	return s
}

// ChildrenJSON is GET /api/herdr/children: the sub-agents of the pane at path,
// as HTTP status and body.
func (c *Controller) ChildrenJSON(ctx context.Context, p string) (int, []byte) {
	c.mu.Lock()
	it, ok := c.itemAt(p)
	m, run := c.machineOf(it), c.col.run
	c.mu.Unlock()
	if !ok {
		return marshalStatus(refuse(http.StatusNotFound, "unknown pane"))
	}
	callCtx, cancel := context.WithTimeout(ctx, transcriptTimeout)
	defer cancel()
	kids, err := c.children(callCtx, run, m, it)
	if err != nil {
		return marshalStatus(transcriptRefusal(err))
	}
	if kids == nil {
		kids = []Child{}
	}
	return marshalStatus(&refusal{http.StatusOK, map[string]any{
		"path": p, "machine": it.Machine, "pane_id": it.PaneID, "agent": it.Agent, "children": kids,
		"stale_after_s": int(staleAfter.Seconds()), "checked_at": c.col.now().UTC().Format(time.RFC3339),
	}})
}

// ChildTranscriptJSON is GET /api/herdr/transcript?child=<id>: the transcript
// of one of the pane's sub-agents, in the pane transcript's shape. The id is
// looked up in the pane's children as listed now, never taken as a path.
func (c *Controller) ChildTranscriptJSON(ctx context.Context, p, child, since string) (int, []byte) {
	c.mu.Lock()
	it, ok := c.itemAt(p)
	m, run := c.machineOf(it), c.col.run
	c.mu.Unlock()
	if !ok {
		return marshalStatus(refuse(http.StatusNotFound, "unknown pane"))
	}
	callCtx, cancel := context.WithTimeout(ctx, transcriptTimeout)
	defer cancel()
	kids, err := c.children(callCtx, run, m, it)
	if err != nil {
		return marshalStatus(transcriptRefusal(err))
	}
	i := slices.IndexFunc(kids, func(k Child) bool { return k.ID == child })
	switch {
	case i < 0:
		return marshalStatus(transcriptRefusal(&noTranscript{"This pane has no sub-agent " + strconv.Quote(child) + " now"}))
	case !kids[i].Transcript:
		return marshalStatus(transcriptRefusal(&noTranscript{kids[i].Name + " has written no transcript yet"}))
	}
	e, err := c.childTranscript(it, kids[i])
	if err != nil {
		return marshalStatus(transcriptRefusal(err))
	}
	return answerTranscript(p, child, it, kids[i].ref, e, since)
}

// childTranscript reads a sub-agent's transcript on from where the last read of
// it stopped. A child is read only while its page asks, so there is no shared
// flight as for panes.
func (c *Controller) childTranscript(it Item, ch Child) (*transcriptEntry, error) {
	key := it.ID + "#" + ch.ID
	c.mu.Lock()
	prev := c.childTx[key]
	c.mu.Unlock()
	if prev != nil && prev.path != ch.file {
		prev = nil
	}
	offset := int64(0)
	if prev != nil {
		offset = prev.offset
	}
	data, from, err := readLocalFrom(ch.file, offset)
	if err != nil {
		return nil, err
	}
	e := c.advance(prev, transcriptRead{source: "local", kind: ch.ref, path: ch.file, file: tildePath(ch.file), session: ch.session, from: from, data: data})
	c.mu.Lock()
	c.childTx[key] = e
	c.mu.Unlock()
	return e, nil
}

// summarizeChildren puts each local Claude or Codex pane's sub-agent counts on
// its item, for the strip. A pane whose children cannot be read gets none.
func (c *Controller) summarizeChildren(ctx context.Context, items []Item) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for i := range items {
		it := &items[i]
		if it.Machine != localLabel || (it.Agent != "claude" && it.Agent != "codex") || it.Status == statusExited {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			kids, err := c.children(ctx, c.col.run, machine{Label: localLabel}, *it)
			if err != nil || len(kids) == 0 {
				return
			}
			s := &ChildSummary{Count: len(kids)}
			for _, k := range kids {
				switch k.Status {
				case "working":
					s.Working++
				case "stale":
					s.Stale++
				case "gone":
					s.Gone++
				}
			}
			it.Children = s
		}()
	}
	wg.Wait()
}
