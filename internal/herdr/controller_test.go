package herdr

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func seq(n int64) *int64 { return &n }

func TestRenderItem(t *testing.T) {
	ghost := true
	tests := []struct {
		name    string
		item    Item
		want    []string
		notWant []string
	}{
		{
			name: "dialog with plan error",
			item: Item{Machine: "local", PaneID: "wA:p1", Agent: "claude", Status: "blocked", Title: "Fix it", Attention: attentionDialog,
				StateChangeSeq: seq(7), Dialog: &Dialog{Kind: "permission", Text: "Do you want ```x``` to proceed?", Options: []Choice{{N: 1, Label: "Yes"}, {N: 3, Label: "Type something.", FreeText: true}}},
				Plan: &Plan{Path: "~/.claude/plans/p.md", Error: "path_not_allowed: outside"}},
			want: []string{"# Fix it", "completion_seq null · state_change_seq 7", "## Needs you: a menu is open", "````text\nDo you want ```x``` to proceed?\n````", "3. Type something. _(opens a text field)_", "Not read: path_not_allowed"},
		},
		{
			name: "screen answer with ghost input",
			item: Item{Machine: "workbox3", Box: "ib3", PaneID: "w3:p4", Agent: "claude", Status: "done", Attention: attentionQuestion,
				Last: &LastText{Text: "Should I <b>go</b>?", Source: sourceScreen}, InputLine: &InputLine{Text: "go ahead", Ghost: &ghost}, Notes: []string{"no transcript"}},
			want:    []string{"# w3:p4", "workbox3 (ib3)", "```text\nShould I <b>go</b>?\n```", "never sent", "> go ahead", "- no transcript"},
			notWant: []string{"Typed into the pane"},
		},
		{
			name: "exited",
			item: Item{Machine: "local", PaneID: "wC:p1", Agent: "claude", Status: statusExited, ResumeCommand: "claude --resume abc"},
			want: []string{"## Exited", "`claude --resume abc`"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := renderItem(tt.item)
			for _, w := range tt.want {
				if !strings.Contains(got, w) {
					t.Errorf("missing %q in:\n%s", w, got)
				}
			}
			for _, w := range tt.notWant {
				if strings.Contains(got, w) {
					t.Errorf("unexpected %q in:\n%s", w, got)
				}
			}
		})
	}
}

func TestControllerViewed(t *testing.T) {
	readPath := filepath.Join(t.TempDir(), "herdr-read.json")
	c := NewController(Options{ReadStatePath: readPath})
	item := Item{Machine: "local", PaneID: "wA:p1", Agent: "claude", Status: "done", Attention: attentionQuestion, NeedsYou: true, StateChangeSeq: seq(5), CompletionSeq: seq(5)}
	other := Item{Machine: "local", PaneID: "wA:p2", Agent: "claude", Status: "idle", StateChangeSeq: seq(9)}
	c.snap = &Snapshot{Items: []Item{item, other}}

	if _, err := c.SnapshotJSON(); err != nil {
		t.Fatal(err)
	}
	if err := c.SetViewed("local/nope.md", true); err == nil {
		t.Errorf("unknown pane must be refused")
	}
	if err := c.SetViewed("local/wA:p1.md", true); err != nil {
		t.Fatal(err)
	}
	view := func() (need int, viewed map[string]bool) {
		data, err := c.SnapshotJSON()
		if err != nil {
			t.Fatal(err)
		}
		var s struct {
			NeedYou int `json:"need_you"`
			Items   []struct {
				Path   string `json:"path"`
				Viewed bool   `json:"viewed"`
			} `json:"items"`
		}
		if err := json.Unmarshal(data, &s); err != nil {
			t.Fatal(err)
		}
		viewed = map[string]bool{}
		for _, it := range s.Items {
			viewed[it.Path] = it.Viewed
		}
		return s.NeedYou, viewed
	}
	if need, v := view(); need != 0 || !v["local/wA:p1.md"] || v["local/wA:p2.md"] {
		t.Errorf("after marking read: need=%d viewed=%v", need, v)
	}

	// The mark survives a restart.
	c2 := NewController(Options{ReadStatePath: readPath})
	c2.snap = c.snap
	if data, _ := os.ReadFile(readPath); !strings.Contains(string(data), "local/wA:p1.md") {
		t.Errorf("read state not persisted: %s", data)
	}

	// A new turn makes it unread again.
	item.StateChangeSeq = seq(6)
	c.snap = &Snapshot{Items: []Item{item, other}}
	if need, v := view(); need != 1 || v["local/wA:p1.md"] {
		t.Errorf("after a new turn: need=%d viewed=%v", need, v)
	}

	if err := c.SetViewed("local/wA:p1.md", false); err != nil {
		t.Fatal(err)
	}
	if _, v := view(); v["local/wA:p1.md"] {
		t.Errorf("unmark failed")
	}
}

func TestOrderItems(t *testing.T) {
	s := &Snapshot{
		Machines: []MachineStatus{{Label: "local"}, {Label: "ib3"}},
		Items: []Item{
			{Machine: "ib3", PaneID: "a", NeedsYou: true},
			{Machine: "local", PaneID: "b"},
			{Machine: "local", PaneID: "c", NeedsYou: true},
		},
	}
	var got []string
	for _, it := range orderItems(s) {
		got = append(got, it.PaneID)
	}
	if strings.Join(got, ",") != "c,b,a" {
		t.Errorf("order = %v, want c,b,a", got)
	}
}

func TestControllerBaselineAndPlan(t *testing.T) {
	dir := t.TempDir()
	c := NewController(Options{ReadStatePath: filepath.Join(dir, "herdr-read.json")})
	it := Item{Machine: "local", PaneID: "wA:p1", Agent: "claude", Status: "blocked", Attention: attentionDialog, StateChangeSeq: seq(3),
		Plan: &Plan{Path: "~/.claude/plans/p.md", Text: "# Plan", Source: "local_disk"}}
	c.snap = &Snapshot{Items: []Item{it}}

	if f := c.files(c.snap.Items); f[0].Previous != nil {
		t.Errorf("no read mark yet: no baseline")
	}
	if err := c.SetViewed("local/wA:p1.md", true); err != nil {
		t.Fatal(err)
	}
	f := c.files(c.snap.Items)
	if f[0].Previous == nil || *f[0].Previous != renderItem(it) {
		t.Errorf("baseline must be the pane as read")
	}
	if data, err := os.ReadFile(filepath.Join(dir, "herdr-baseline.json")); err != nil || !strings.Contains(string(data), "local/wA:p1.md") {
		t.Errorf("baseline not persisted: %s %v", data, err)
	}
	c2 := NewController(Options{ReadStatePath: filepath.Join(dir, "herdr-read.json")})
	if c2.baseline["local/wA:p1.md"] == "" {
		t.Errorf("baseline not reloaded")
	}

	data, err := c.PlanJSON("local/wA:p1.md")
	if err != nil || !strings.Contains(string(data), `"source":"local_disk"`) || !strings.Contains(string(data), `"plan_path":"~/.claude/plans/p.md"`) {
		t.Errorf("PlanJSON = %s, %v", data, err)
	}
	if _, err := c.PlanJSON("local/nope.md"); err == nil {
		t.Errorf("PlanJSON on a pane without a plan must fail")
	}
}

func TestReadLocalPlan(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".claude", "plans"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", "plans", "happy-plan.md"), []byte("# The plan"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := &Plan{Path: "~/.claude/plans/happy-plan.md", Error: "path_not_allowed: outside"}
	readLocalPlan(p)
	if p.Text != "# The plan" || p.Source != "local_disk" || p.Error != "" {
		t.Errorf("plan = %+v", p)
	}
	for _, bad := range []string{"~/.claude/plans/../../.ssh/id.md", "/etc/passwd", "~/.claude/plans/missing.md"} {
		q := &Plan{Path: bad}
		readLocalPlan(q)
		if q.Text != "" {
			t.Errorf("%s must not be read: %+v", bad, q)
		}
	}
}

func TestPlanFile(t *testing.T) {
	c := NewController(Options{})
	withPlan := Item{Machine: "workbox3", PaneID: "w3:p4", Agent: "claude", Status: "blocked", Attention: attentionDialog, StateChangeSeq: seq(4),
		Plan: &Plan{Path: "~/.claude/plans/p.md", Text: "# Plan\n\n1. Do it\n"}}
	unread := Item{Machine: "workbox3", PaneID: "w3:p5", Agent: "claude", Status: "blocked", StateChangeSeq: seq(2),
		Plan: &Plan{Path: "~/.claude/plans/q.md", Error: "path_not_allowed"}}
	c.snap = &Snapshot{Items: []Item{withPlan, unread}}

	var paths []string
	for _, f := range c.files(c.snap.Items) {
		paths = append(paths, f.Path)
	}
	if strings.Join(paths, ",") != "workbox3/w3:p4.md,workbox3/w3:p4.plan.md,workbox3/w3:p5.md" {
		t.Fatalf("files = %v", paths)
	}
	if got := c.files(c.snap.Items)[1].Content; got != withPlan.Plan.Text {
		t.Errorf("plan file content = %q", got)
	}
	pane := renderItem(withPlan)
	if !strings.Contains(pane, "The plan is its own file: `workbox3/w3:p4.plan.md`") || strings.Contains(pane, "1. Do it") {
		t.Errorf("pane file must point at the plan, not repeat it:\n%s", pane)
	}
	if !strings.Contains(renderItem(unread), "Not read: path_not_allowed") {
		t.Errorf("an unread plan stays in the pane file")
	}

	if err := c.SetViewed("workbox3/w3:p4.plan.md", true); err != nil {
		t.Fatal(err)
	}
	data, _ := c.SnapshotJSON()
	var s struct {
		Items []struct {
			Path       string `json:"path"`
			Viewed     bool   `json:"viewed"`
			PlanFile   string `json:"plan_file"`
			PlanViewed *bool  `json:"plan_viewed"`
		} `json:"items"`
	}
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatal(err)
	}
	p := s.Items[0]
	if p.PlanFile != "workbox3/w3:p4.plan.md" || p.PlanViewed == nil || !*p.PlanViewed || p.Viewed {
		t.Errorf("plan read mark is its own: %+v", p)
	}
	if q := s.Items[1]; q.PlanFile != "" || q.PlanViewed != nil {
		t.Errorf("no plan file without plan text: %+v", q)
	}
	if b := c.files(c.snap.Items)[1].Previous; b == nil || *b != withPlan.Plan.Text {
		t.Errorf("plan baseline = %v", b)
	}
}
