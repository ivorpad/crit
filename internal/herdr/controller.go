package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tomasz-tomczyk/crit/internal/session"
)

// UpdatedEvent is the SSE event the review page reloads herdr panes on. It is not
// file-changed: that one ends a review round and closes open comment forms.
const UpdatedEvent = "herdr-updated"

// Options configure a Controller.
type Options struct {
	Interval    time.Duration     // pause between sweeps; a sweep itself takes ~6 s
	CallTimeout time.Duration     // per machine
	Overrides   map[string]string // herdr label -> instabox box (herdr_instabox_boxes)
	// ReadStatePath is where the owner's "read" marks are kept, one turn key per
	// pane path. Empty keeps them in memory only. Each pane's content at the time
	// it was marked read goes next to it, in herdr-baseline.json.
	ReadStatePath string
}

// Controller keeps the herdr session's files in step with the machines, delivers
// one reply at a time to a pane (send.go), and reads the panes' Claude
// transcripts (transcript.go).
type Controller struct {
	opts Options
	col  collectOptions

	mu   sync.Mutex
	snap *Snapshot
	read map[string]string // pane path -> turn key the owner marked read
	// baseline is each pane's rendered content when it was last marked read: the
	// Document/Diff toggle shows what changed since.
	baseline map[string]string
	sess     *session.Session
	last     string // fingerprint of the last published sweep
	// sent is the last delivery per pane path (send.go); in memory only.
	sent   map[string]Delivery
	keyGap time.Duration // pause between send-keys calls; tests set 0
	kick   chan struct{} // a send asks Run for a sweep now

	// transcripts is each read pane's Claude transcript, by item id
	// (transcript.go). In memory only.
	transcripts map[string]*transcriptState
	reuse       time.Duration // see transcriptReuse; tests set 0
	// boot and epochs name every read from byte 0, so a version the page holds
	// never matches a text read again from the start, even after a restart.
	boot   string
	epochs atomic.Int64
}

// NewController loads the read marks; the first Sweep fills it.
func NewController(o Options) *Controller {
	if o.Interval <= 0 {
		o.Interval = 15 * time.Second
	}
	if o.CallTimeout <= 0 {
		o.CallTimeout = 30 * time.Second
	}
	c := &Controller{
		opts: o,
		col: collectOptions{
			endpoints: endpointsPath(), overrides: o.Overrides, callTimeout: o.CallTimeout,
			run: execRunner, now: time.Now,
		},
		read:     map[string]string{},
		baseline: map[string]string{},
		sent:     map[string]Delivery{},
		keyGap:   keyGap,
		kick:     make(chan struct{}, 1),

		transcripts: map[string]*transcriptState{},
		reuse:       transcriptReuse,
		boot:        strconv.FormatInt(time.Now().UnixNano(), 36),
	}
	if o.ReadStatePath != "" {
		if data, err := os.ReadFile(o.ReadStatePath); err == nil {
			_ = json.Unmarshal(data, &c.read)
		}
		if data, err := os.ReadFile(c.baselinePath()); err == nil {
			_ = json.Unmarshal(data, &c.baseline)
		}
	}
	return c
}

func (c *Controller) baselinePath() string {
	return filepath.Join(filepath.Dir(c.opts.ReadStatePath), "herdr-baseline.json")
}

// files renders a sweep into review files, each compared with its read baseline.
// The caller holds c.mu.
func (c *Controller) files(items []Item) []session.SyntheticFile {
	files := make([]session.SyntheticFile, 0, len(items))
	add := func(path, content string) {
		f := session.SyntheticFile{Path: path, Content: content}
		if b, ok := c.baseline[path]; ok {
			f.Previous = &b
		}
		files = append(files, f)
	}
	for _, it := range items {
		add(itemPath(it), renderItem(it))
		if hasPlanFile(it) {
			add(planFilePath(it), it.Plan.Text)
		}
	}
	return files
}

// Sweep collects once and returns the review files for it, in display order.
func (c *Controller) Sweep(ctx context.Context) ([]session.SyntheticFile, error) {
	snap, err := collect(ctx, c.col)
	if err != nil {
		return nil, err
	}
	snap.Items = orderItems(snap)
	c.mu.Lock()
	defer c.mu.Unlock()
	old := c.snap
	c.snap = snap
	c.keepTranscripts(old, snap)
	return c.files(snap.Items), nil
}

// orderItems groups panes by machine in machine order, panes that need the owner
// first within each machine.
func orderItems(s *Snapshot) []Item {
	rank := map[string]int{}
	for i, m := range s.Machines {
		rank[m.Label] = i
	}
	out := append([]Item(nil), s.Items...)
	sort.SliceStable(out, func(i, j int) bool {
		if rank[out[i].Machine] != rank[out[j].Machine] {
			return rank[out[i].Machine] < rank[out[j].Machine]
		}
		return out[i].NeedsYou && !out[j].NeedsYou
	})
	return out
}

// Run sweeps until ctx ends, one sweep at a time, and publishes each sweep that
// changed something to sess: its files, then UpdatedEvent.
func (c *Controller) Run(ctx context.Context, sess *session.Session) {
	c.mu.Lock()
	c.sess = sess
	c.mu.Unlock()
	c.publish(sess)
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(c.opts.Interval):
		case <-c.kick:
		}
		if _, err := c.Sweep(ctx); err != nil {
			if !errors.Is(err, context.Canceled) {
				log.Printf("herdr sweep: %v", err)
			}
			continue
		}
		c.publish(sess)
	}
}

func (c *Controller) publish(sess *session.Session) {
	c.mu.Lock()
	snap := c.snap
	if snap == nil {
		c.mu.Unlock()
		return
	}
	files := c.files(snap.Items)
	c.mu.Unlock()
	changed := sess.ReplaceSyntheticFiles(files)
	fp := fingerprint(snap)
	c.mu.Lock()
	moved := fp != c.last
	c.last = fp
	c.mu.Unlock()
	if changed || moved {
		sess.Notify(session.SSEEvent{Type: UpdatedEvent})
	}
}

// fingerprint is a sweep without its timings, so a sweep that only took a
// different time does not reload the page.
func fingerprint(s *Snapshot) string {
	ms := make([]MachineStatus, len(s.Machines))
	for i, m := range s.Machines {
		m.MS = 0
		ms[i] = m
	}
	data, _ := json.Marshal(struct {
		M []MachineStatus
		I []Item
	}{ms, s.Items})
	return string(data)
}

// turnKey identifies the turn an owner marks read: a new state change, dialog or
// resume line makes the pane unread again. state_change_seq leads because herdr
// leaves completion_seq null for hook-driven agents (opencode).
func turnKey(it Item) string {
	dialog := ""
	if it.Dialog != nil {
		dialog = it.Dialog.ID
	}
	return fmt.Sprintf("%s|%s|%s|%s|%s", seqText(it.StateChangeSeq), seqText(it.CompletionSeq), dialog, it.Status, it.ResumeCommand)
}

// viewItem is an item as /api/herdr sends it.
type viewItem struct {
	Item
	Path       string `json:"path"`
	Viewed     bool   `json:"viewed"`
	PlanFile   string `json:"plan_file,omitempty"`
	PlanViewed *bool  `json:"plan_viewed,omitempty"`
	// Delivery is the last send to this pane and whether the agent took it.
	Delivery *Delivery `json:"delivery,omitempty"`
}

// SnapshotJSON is the latest sweep for the review page.
func (c *Controller) SnapshotJSON() ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.snap == nil {
		return nil, errors.New("no sweep yet")
	}
	items := make([]viewItem, len(c.snap.Items))
	need := 0
	for i, it := range c.snap.Items {
		p := itemPath(it)
		v := viewItem{Item: it, Path: p, Viewed: c.read[p] != "" && c.read[p] == turnKey(it), Delivery: c.deliveryFor(it)}
		if hasPlanFile(it) {
			pp := planFilePath(it)
			read := c.read[pp] != "" && c.read[pp] == turnKey(it)
			v.PlanFile, v.PlanViewed = pp, &read
		}
		if it.NeedsYou && !v.Viewed {
			need++
		}
		items[i] = v
	}
	return json.Marshal(struct {
		CollectedAt string          `json:"collected_at"`
		Interval    float64         `json:"interval_seconds"`
		Machines    []MachineStatus `json:"machines"`
		Items       []viewItem      `json:"items"`
		NeedYou     int             `json:"need_you"`
		BoxesError  string          `json:"instabox_error,omitempty"`
	}{c.snap.CollectedAt, c.opts.Interval.Seconds(), c.snap.Machines, items, need, c.snap.BoxesError})
}

// SetViewed marks the pane's current turn read, or clears the mark.
func (c *Controller) SetViewed(path string, viewed bool) error {
	c.mu.Lock()
	var found *Item
	content := ""
	if c.snap != nil {
		for i := range c.snap.Items {
			it := &c.snap.Items[i]
			if itemPath(*it) == path {
				found, content = it, renderItem(*it)
				break
			}
			if hasPlanFile(*it) && planFilePath(*it) == path {
				found, content = it, it.Plan.Text
				break
			}
		}
	}
	if found == nil {
		c.mu.Unlock()
		return fmt.Errorf("no pane %s in the latest sweep", path)
	}
	if viewed {
		c.read[path] = turnKey(*found)
		c.baseline[path] = content
	} else {
		delete(c.read, path)
	}
	readData, _ := json.Marshal(c.read)
	baseData, _ := json.Marshal(c.baseline)
	sess := c.sess
	var files []session.SyntheticFile
	if sess != nil {
		files = c.files(c.snap.Items)
	}
	c.mu.Unlock()
	if c.opts.ReadStatePath != "" {
		if err := session.AtomicWriteFile(c.opts.ReadStatePath, readData, 0o600); err != nil {
			return err
		}
		if err := session.AtomicWriteFile(c.baselinePath(), baseData, 0o600); err != nil {
			return err
		}
	}
	if sess != nil {
		sess.ReplaceSyntheticFiles(files)
		sess.Notify(session.SSEEvent{Type: UpdatedEvent})
	}
	return nil
}

// PlanJSON is the plan of the pane at path in the latest sweep.
func (c *Controller) PlanJSON(path string) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.snap != nil {
		for _, it := range c.snap.Items {
			if itemPath(it) != path || it.Plan == nil {
				continue
			}
			return json.Marshal(struct {
				Path     string `json:"path"`
				PlanPath string `json:"plan_path"`
				Text     string `json:"text"`
				Error    string `json:"error,omitempty"`
				Source   string `json:"source,omitempty"`
			}{path, it.Plan.Path, it.Plan.Text, it.Plan.Error, it.Plan.Source})
		}
	}
	return nil, fmt.Errorf("no plan for %s in the latest sweep", path)
}

// paneIDRE is a herdr pane id (w3:p4, wNR:pC). It reaches argv, so nothing that
// could read as a flag gets through.
var paneIDRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9:_.-]{0,63}$`)

// ErrNoPane means the path is not a pane (or plan) in the latest sweep.
var ErrNoPane = errors.New("no such pane in the latest sweep")

// Focus brings the pane at path (a pane file or its plan file) to the front of
// the owner's herdr TUI on this Mac. It changes which pane the TUI shows and
// types nothing, so it needs none of the checks send.go makes.
func (c *Controller) Focus(ctx context.Context, path string) (machineLabel, paneID string, err error) {
	c.mu.Lock()
	found, ok := c.itemAt(path)
	run := c.col.run
	c.mu.Unlock()
	if !ok {
		return "", "", ErrNoPane
	}
	if !paneIDRE.MatchString(found.PaneID) {
		return "", "", fmt.Errorf("unsafe pane id %q", found.PaneID)
	}
	m := machine{Label: found.Machine}
	if found.Machine != localLabel {
		m.Target = found.Machine // any non-empty target makes herdrArgv use --machine <label>
	}
	argv, err := herdrArgv(m, "agent", "focus", found.PaneID)
	if err != nil {
		return "", "", err
	}
	callCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if _, err := run(callCtx, nil, argv); err != nil {
		return "", "", err
	}
	return found.Machine, found.PaneID, nil
}
