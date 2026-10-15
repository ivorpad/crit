package herdr

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// MachineStatus is how one machine answered this sweep.
type MachineStatus struct {
	Label  string `json:"label"`
	Target string `json:"ssh_target,omitempty"`
	Box    string `json:"instabox,omitempty"`
	// BoxState is instabox's state for the box (running, suspended, stopped), empty
	// when the machine is not a box or instabox could not be asked.
	BoxState string `json:"instabox_state,omitempty"`
	// State: "reachable", "paused" (not contacted), "unreachable", or "error".
	State string `json:"state"`
	Error string `json:"error,omitempty"`
	// Partial failures on a reachable machine: one of the two sources failed.
	GatewayError   string `json:"gateway_error,omitempty"`
	DiscoveryError string `json:"discovery_error,omitempty"`
	Woken          bool   `json:"woken,omitempty"`
	MS             int64  `json:"ms"`
	Agents         int    `json:"agents"`
	// Panes with no agent: ShellPanes showed no resume line; UninspectedPanes could
	// not be read (outside the gateway's allowedRoots), so an exited agent there is missed.
	ShellPanes       int `json:"shell_panes"`
	UninspectedPanes int `json:"uninspected_panes"`
}

// Snapshot is one sweep over every machine.
type Snapshot struct {
	CollectedAt string          `json:"collected_at"`
	Machines    []MachineStatus `json:"machines"`
	Items       []Item          `json:"items"`
	// BoxesError is set when instabox could not list boxes: paused boxes are then
	// contacted like any machine and may take up to the call timeout to fail.
	BoxesError string `json:"instabox_error,omitempty"`
}

type collectOptions struct {
	endpoints   string
	overrides   map[string]string // herdr label -> instabox name
	wake        bool
	callTimeout time.Duration
	run         runner
	now         func() time.Time
}

// collect polls every machine in parallel. A machine that fails never fails the
// sweep: it shows up in Machines with its error.
func collect(ctx context.Context, o collectOptions) (*Snapshot, error) {
	machines, err := loadMachines(o.endpoints)
	if err != nil {
		return nil, err
	}
	snap := &Snapshot{CollectedAt: o.now().UTC().Format(time.RFC3339)}
	boxCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	boxes, boxErr := listBoxes(boxCtx, o.run)
	cancel()
	if boxErr != nil {
		snap.BoxesError = boxErr.Error()
	}
	assignBoxes(machines, boxes, o.overrides)

	statuses := make([]MachineStatus, len(machines))
	items := make([][]Item, len(machines))
	var wg sync.WaitGroup
	for i, m := range machines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			statuses[i], items[i] = collectMachine(ctx, o, m, boxes)
		}()
	}
	wg.Wait()
	snap.Machines = statuses
	for _, its := range items {
		snap.Items = append(snap.Items, its...)
	}
	if snap.Items == nil {
		snap.Items = []Item{}
	}
	return snap, nil
}

func collectMachine(ctx context.Context, o collectOptions, m machine, boxes map[string]boxState) (MachineStatus, []Item) {
	start := time.Now()
	st := MachineStatus{Label: m.Label, Target: m.Target, Box: m.Box}
	done := func(state string, err error) MachineStatus {
		st.State = state
		if err != nil {
			st.Error = err.Error()
		}
		st.MS = time.Since(start).Milliseconds()
		return st
	}
	if m.Box != "" {
		b := boxes[m.Box]
		st.BoxState = b.State
		if b.paused() {
			if !o.wake {
				return done("paused", nil), nil
			}
			reachable, _, err := wakeBox(ctx, o.run, m)
			if err != nil {
				return done("error", fmt.Errorf("waking %s: %w", m.Box, err)), nil
			}
			st.Woken = true
			if !reachable {
				return done("unreachable", fmt.Errorf("resumed %s, but its gateway did not answer within %s", m.Box, wakeReachTimeout)), nil
			}
		}
	}

	callCtx, cancel := context.WithTimeout(ctx, o.callTimeout)
	defer cancel()

	// herdr (discovery, seqs) and the gateway (parsing, attention, transcripts) at once.
	var (
		hAgents, hPanes []herdrAgent
		discErr         error
		wg              sync.WaitGroup
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		hAgents, hPanes, discErr = discover(callCtx, o.run, m)
	}()
	cs, gwErr := snapshotOf(callCtx, o.run, m)
	wg.Wait()
	if discErr != nil && gwErr != nil {
		return done("unreachable", fmt.Errorf("herdr: %w; gateway: %w", discErr, gwErr)), nil
	}
	if discErr != nil {
		st.DiscoveryError = discErr.Error()
	}
	if gwErr != nil {
		st.GatewayError = gwErr.Error()
	}

	items, candidates := merge(m, cs, hAgents, hPanes, gwErr)
	if gwErr == nil {
		readMore(callCtx, o.run, m, &items, candidates, &st)
	} else {
		st.UninspectedPanes = len(candidates)
	}
	st.Agents = len(items)
	return done("reachable", nil), items
}

func snapshotOf(ctx context.Context, run runner, m machine) (consoleSnapshot, error) {
	var cs consoleSnapshot
	res, errs, err := callGateway(ctx, run, m, []gatewayRequest{
		{ID: "snap", Op: "console_snapshot", Params: map[string]any{"audit_n": 1}},
	})
	if err != nil {
		return cs, err
	}
	if errs[0] != nil {
		return cs, errs[0]
	}
	if err := json.Unmarshal(res[0], &cs); err != nil {
		return cs, fmt.Errorf("parsing console_snapshot: %w", err)
	}
	return cs, nil
}

// merge joins the gateway's agents with herdr's by pane_id. Agents only herdr knows
// are unreadable_by_gateway; panes with no agent are returned as candidates for an
// exited agent, to be read for a resume line.
func merge(m machine, cs consoleSnapshot, hAgents, hPanes []herdrAgent, gwErr error) (items []Item, candidates []herdrAgent) {
	byPane := make(map[string]*herdrAgent, len(hAgents))
	for i := range hAgents {
		byPane[hAgents[i].PaneID] = &hAgents[i]
	}
	seen := map[string]bool{}
	for _, a := range cs.Agents {
		items = append(items, itemFromAgent(m, a, byPane[a.PaneID]))
		seen[a.PaneID] = true
	}
	for _, b := range cs.Background {
		items = append(items, itemFromBackground(m, b))
		seen[b.PaneID] = true
	}
	for _, h := range hAgents {
		if seen[h.PaneID] {
			continue
		}
		it := itemFromHerdr(m, h)
		if gwErr != nil {
			it.Notes = []string{"the gateway did not answer this sweep: herdr's fields only"}
		}
		items = append(items, it)
		seen[h.PaneID] = true
	}
	for _, p := range hPanes {
		if !seen[p.PaneID] && p.Agent == nil {
			candidates = append(candidates, p)
		}
	}
	return items, candidates
}

// readBatch collects gateway reads for one machine and what to do with each answer.
type readBatch struct {
	reqs  []gatewayRequest
	apply []func(json.RawMessage, error)
}

func (b *readBatch) add(op string, params map[string]any, apply func(json.RawMessage, error)) {
	b.reqs = append(b.reqs, gatewayRequest{ID: fmt.Sprintf("r%d", len(b.reqs)), Op: op, Params: params})
	b.apply = append(b.apply, apply)
}

// run sends every read on one gateway process and hands each answer back.
func (b *readBatch) run(ctx context.Context, run runner, m machine) {
	if len(b.reqs) == 0 {
		return
	}
	res, errs, err := callGateway(ctx, run, m, b.reqs)
	for k, apply := range b.apply {
		if err != nil {
			apply(nil, err)
			continue
		}
		apply(res[k], errs[k])
	}
}

// readMore runs the follow-up reads in one gateway process: the full reply for a
// clipped question, the ANSI input line of each settled Claude agent, the plan
// file of a plan dialog, and the screen of each agent-less pane (for a resume line).
func readMore(ctx context.Context, run runner, m machine, items *[]Item, candidates []herdrAgent, st *MachineStatus) {
	var b readBatch
	its := *items
	for i := range its {
		queueItemReads(&b, m, &its[i])
	}
	var exited []Item
	for _, p := range candidates {
		b.add("read_pane", map[string]any{"pane_id": p.PaneID, "source": "visible", "lines": 60}, func(raw json.RawMessage, err error) {
			var r struct {
				Text string `json:"text"`
			}
			if err != nil || json.Unmarshal(raw, &r) != nil {
				st.UninspectedPanes++
				return
			}
			if cmd, kind, ok := resumeCommand(r.Text); ok {
				exited = append(exited, itemFromExited(m, p, cmd, kind))
				return
			}
			st.ShellPanes++
		})
	}
	b.run(ctx, run, m)
	its = append(its, exited...)
	*items = its
}

func queueItemReads(b *readBatch, m machine, it *Item) {
	if it.Attention == attentionQuestion && it.Last != nil && it.Last.Source == sourceTranscript && it.Last.Truncated {
		b.add("read_agent", map[string]any{"target": it.PaneID, "source": "reply"}, func(raw json.RawMessage, err error) {
			applyFullReply(it, raw, err)
		})
	}
	if it.Agent == "claude" && (it.Status == "idle" || it.Status == "done") && it.Dialog == nil && it.DialogScreen == "" {
		b.add("read_agent", map[string]any{"target": it.PaneID, "source": "visible", "lines": 40, "strip_ansi": false}, func(raw json.RawMessage, err error) {
			applyInputLine(it, raw, err)
		})
	}
	if it.Plan != nil {
		b.add("read_file", map[string]any{"path": it.Plan.Path}, func(raw json.RawMessage, err error) {
			applyPlan(it.Plan, raw, err)
			if it.Plan.Text == "" && m.Target == "" {
				readLocalPlan(it.Plan)
			}
		})
	}
}

func applyFullReply(it *Item, raw json.RawMessage, err error) {
	var r struct {
		Reply *wireReply `json:"reply"`
	}
	if err != nil || json.Unmarshal(raw, &r) != nil || r.Reply == nil {
		return
	}
	it.Last = lastFromReply(r.Reply, 0)
	if r.Reply.Ref != nil {
		it.TranscriptRef = r.Reply.Ref
	}
}

func applyPlan(p *Plan, raw json.RawMessage, err error) {
	if err != nil {
		p.Error = err.Error()
		return
	}
	var f struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &f) == nil {
		p.Text, p.Source = f.Text, "gateway"
	}
}

// readLocalPlan reads a plan on this Mac when the gateway would not: its
// allowedRoots never include ~/.claude/plans. planPathRE allows one plain file
// name under that directory and nothing else.
func readLocalPlan(p *Plan) {
	if !planPathRE.MatchString(p.Path) || planPathRE.FindString(p.Path) != p.Path {
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	data, err := os.ReadFile(filepath.Join(home, strings.TrimPrefix(p.Path, "~/")))
	if err != nil {
		p.Error = strings.TrimSpace(p.Error + "; local read: " + err.Error())
		return
	}
	p.Text, p.Source, p.Error = string(data), "local_disk", ""
}

// applyInputLine sets the input line from an ANSI read. A gateway that does not
// support strip_ansi:false sends plain text: the line is kept, ghost stays null,
// and the item says so.
func applyInputLine(it *Item, raw json.RawMessage, err error) {
	if err != nil {
		return
	}
	var r struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &r) != nil {
		return
	}
	if !hasANSI(r.Text) {
		if line, ok := inputLine(r.Text); ok {
			it.InputLine = &InputLine{Text: line}
			it.Notes = append(it.Notes, "input line unverified: this machine's gateway does not return ANSI yet, so ghost text cannot be told from typed text")
		}
		return
	}
	text, ghost, ok := inputLineANSI(r.Text)
	if !ok {
		it.InputLine = nil
		return
	}
	it.InputLine = &InputLine{Text: text, Ghost: ghost}
}
