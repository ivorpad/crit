package herdr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const endpointsJSON = `{"version":1,"ssh":[
 {"id":"a","label":"workbox","target":"workdone-ib1","session":"default","enabled":true},
 {"id":"b","label":"workbox2","target":"workdone-ib2","session":"default","enabled":true},
 {"id":"c","label":"workbox5","target":"workdone-ib5","session":"default","enabled":true},
 {"id":"d","label":"off","target":"off-host","session":"default","enabled":false},
 {"id":"e","label":"evil","target":"-oProxyCommand=x","session":"default","enabled":true}
]}`

func writeEndpoints(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "endpoints.json")
	if err := os.WriteFile(p, []byte(endpointsJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadMachines(t *testing.T) {
	ms, err := loadMachines(writeEndpoints(t))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, m := range ms {
		got = append(got, m.Label+"="+m.Target)
	}
	want := "local=,workbox=workdone-ib1,workbox2=workdone-ib2,workbox5=workdone-ib5"
	if strings.Join(got, ",") != want {
		t.Errorf("machines = %v, want %s (disabled and dash-prefixed targets dropped)", got, want)
	}

	ms, err = loadMachines(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil || len(ms) != 1 || ms[0].Label != localLabel {
		t.Errorf("missing catalog: got %v, %v; want only local", ms, err)
	}
}

func TestAssignBoxes(t *testing.T) {
	ms := []machine{{Label: "local"}, {Label: "workbox", Target: "workdone-ib1"}, {Label: "workbox5", Target: "workdone-ib5"}, {Label: "syno", Target: "syno"}}
	boxes := map[string]boxState{"ib5": {State: "running"}, "box-059ed8": {State: "running"}}
	assignBoxes(ms, boxes, map[string]string{"workbox": "box-059ed8"})
	got := map[string]string{}
	for _, m := range ms {
		got[m.Label] = m.Box
	}
	want := map[string]string{"local": "", "workbox": "box-059ed8", "workbox5": "ib5", "syno": ""}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("box for %s = %q, want %q", k, got[k], v)
		}
	}
}

func TestInputLine(t *testing.T) {
	rule := strings.Repeat("─", 40)
	tests := []struct {
		name, screen, want string
		ok                 bool
	}{
		{"claude box", "answer\n" + rule + "\n❯\u00a0Ranking is fine, go with (b)\n" + rule + "\n  ⏵⏵ bypass", "Ranking is fine, go with (b)", true},
		{"empty box", "answer\n" + rule + "\n❯ \n" + rule, "", false},
		{"prompt not under a rule", "> quoted text in an answer\nmore", "", false},
		{"no screen", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := inputLine(tt.screen)
			if got != tt.want || ok != tt.ok {
				t.Errorf("inputLine = %q, %v; want %q, %v", got, ok, tt.want, tt.ok)
			}
		})
	}
}

// fakeGateway answers instabox, herdr and gateway calls by machine; it records every argv.
type fakeGateway struct {
	mu       sync.Mutex
	calls    [][]string
	boxes    string                         // instabox ls --json output
	herdr    map[string]map[string]string   // machine label -> "agent"/"pane" -> list result JSON
	perHost  map[string]func([]byte) []byte // keyed by SSH target, "" for local
	failHost map[string]error
	resumed  []string
}

func (f *fakeGateway) run(_ context.Context, stdin []byte, argv []string) ([]byte, error) {
	f.mu.Lock()
	f.calls = append(f.calls, argv)
	f.mu.Unlock()
	switch argv[0] {
	case "instabox":
		if argv[1] == "resume" {
			f.mu.Lock()
			f.resumed = append(f.resumed, argv[2])
			f.mu.Unlock()
			return nil, nil
		}
		return []byte(f.boxes), nil
	case "herdr":
		label, rest := localLabel, argv[1:]
		if rest[0] == "--machine" {
			label, rest = rest[1], rest[2:]
		}
		key := map[string]string{"agent": "agents", "pane": "panes"}[rest[0]]
		list := f.herdr[label][rest[0]]
		if list == "" {
			list = "[]"
		}
		return []byte(`{"id":"cli","result":{"` + key + `":` + list + `}}`), nil
	}
	host := ""
	if argv[0] == "ssh" {
		host = argv[len(argv)-2]
	}
	if err := f.failHost[host]; err != nil {
		return nil, err
	}
	h := f.perHost[host]
	if h == nil {
		return nil, errors.New("no handler for " + host)
	}
	return h(stdin), nil
}

// answer builds a gateway handler: one response line per request line, by op. A
// key "op:<target or pane_id or path>" wins over the bare op.
func answer(byOp map[string]string) func([]byte) []byte {
	return func(stdin []byte) []byte {
		var out bytes.Buffer
		for _, line := range bytes.Split(bytes.TrimSpace(stdin), []byte("\n")) {
			var req gatewayRequest
			_ = json.Unmarshal(line, &req)
			key := req.Op
			for _, k := range []string{"target", "pane_id", "path"} {
				if v, ok := req.Params[k].(string); ok {
					key = req.Op + ":" + v
				}
			}
			if req.Op == "read_agent" && req.Params["source"] == "reply" {
				key += ":reply"
			}
			res, ok := byOp[key]
			if !ok {
				res, ok = byOp[req.Op]
			}
			if !ok {
				out.WriteString(`{"id":"` + req.ID + `","ok":false,"error":{"code":"unknown_op","message":"no"}}` + "\n")
				continue
			}
			if strings.HasPrefix(res, "ERR ") {
				code := strings.TrimPrefix(res, "ERR ")
				out.WriteString(`{"id":"` + req.ID + `","ok":false,"error":{"code":"` + code + `","message":"refused"}}` + "\n")
				continue
			}
			var compact bytes.Buffer
			if err := json.Compact(&compact, []byte(res)); err != nil {
				panic(err)
			}
			out.WriteString(`{"id":"` + req.ID + `","ok":true,"result":` + compact.String() + "}\n")
		}
		return out.Bytes()
	}
}

var localSnapshot = `{"agents":[
 {"name":null,"pane_id":"wA:p1","agent":"claude","status":"blocked","cwd":"/src/a","title":"Fix it","attention":"dialog",
  "choices":{"dialog_id":"abc","kind":"permission","text":"Bash command\ncurl x\nDo you want to proceed?","options":[{"n":1,"label":"Yes","current":true},{"n":2,"label":"No"}],"multi":false,"free_text":false,"go_ahead":null}},
 {"name":"vert","pane_id":"wA:p2","agent":"claude","status":"done","cwd":"/src/a","title":"Plan","attention":"question",
  "last_reply":{"text":"` + strings.Repeat("x", overviewClip-1) + "…" + `","at":"2026-10-15T10:00:00Z","in_progress":null}},
 {"name":null,"pane_id":"wA:p3","agent":"codex","status":"working","cwd":"/src/b","title":null,"attention":null},
 {"name":null,"pane_id":"wA:p4","agent":"claude","status":"blocked","cwd":"/src/a","attention":"dialog",
  "choices":{"dialog_id":"def","kind":"question","text":"Ready to code?\nWould you like to proceed?\nctrl+g to edit in VS Code · ~/.claude/plans/happy-plan.md","options":[{"n":1,"label":"Yes, and use auto mode"}],"multi":false,"free_text":false,"go_ahead":null}},
 {"name":null,"pane_id":"wA:p5","agent":"claude","status":"idle","cwd":"/src/a","attention":null,
  "last_reply":{"text":"Done.","at":"2026-10-15T10:00:00Z","in_progress":null,"ref":{"kind":"claude_jsonl","path":"/x/s.jsonl","session":"s"}}}
],"background":[{"name":"bg","pane_id":"wA:p9","agent":"claude","status":"background","cwd":"/src/c"}]}`

const seqAgents = `[
 {"pane_id":"wA:p1","agent":"claude","agent_status":"blocked","cwd":"/src/a","completion_seq":11,"state_change_seq":12},
 {"pane_id":"wA:p2","agent":"claude","agent_status":"done","cwd":"/src/a","completion_seq":21,"state_change_seq":22,"agent_session":{"kind":"id","value":"0b1e2c3d-0000-4000-8000-000000000001"}},
 {"pane_id":"wA:p3","agent":"codex","agent_status":"working","cwd":"/src/b","completion_seq":31,"state_change_seq":32},
 {"pane_id":"wA:p4","agent":"claude","agent_status":"blocked","cwd":"/src/a","completion_seq":41,"state_change_seq":42},
 {"pane_id":"wA:p5","agent":"claude","agent_status":"idle","cwd":"/src/a","completion_seq":51,"state_change_seq":52},
 {"pane_id":"wB:p1","agent":"claude","agent_status":"idle","cwd":"/Users/me","terminal_title_stripped":"Home","completion_seq":61,"state_change_seq":62}
]`

const seqPanes = `[
 {"pane_id":"wA:p1","agent":"claude"},{"pane_id":"wB:p1","agent":"claude"},
 {"pane_id":"wC:p1","agent":null,"cwd":"/src/a"},
 {"pane_id":"wC:p2","agent":null,"cwd":"/src/a"},
 {"pane_id":"wC:p3","agent":null,"cwd":"/tmp"}
]`

func TestCollect(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // the plan fallback and claudeRef read the home directory
	rule := strings.Repeat("─", 20)
	remoteSnap := `{"agents":[{"name":null,"pane_id":"w3:p4","agent":"claude","status":"done","cwd":"/data/workspace/relay","attention":"question",
	  "screen_tail":"Should I?\n` + rule + `\n❯ go ahead\n` + rule + `\n  status line"}]}`
	ansiScreen, _ := json.Marshal("answer\n" + rule + "\n\x1b[0m❯ \x1b[2mcommit it\x1b[0m\n" + rule)
	f := &fakeGateway{
		boxes: `[{"name":"box-059ed8","state":"running"},{"name":"ib2","state":"suspended"},{"name":"ib5","state":"running"}]`,
		herdr: map[string]map[string]string{
			"local":    {"agent": seqAgents, "pane": seqPanes},
			"workbox5": {"agent": `[{"pane_id":"w3:p4","agent":"claude","agent_status":"done","completion_seq":39,"state_change_seq":39}]`},
		},
		perHost: map[string]func([]byte) []byte{
			"": answer(map[string]string{
				"console_snapshot":                        localSnapshot,
				"read_agent:wA:p2:reply":                  `{"agent":{},"reply":{"text":"the full reply, which asks: which one?","at":"2026-10-15T10:00:00Z","truncated":false,"in_progress":null}}`,
				"read_agent:wA:p5":                        `{"agent":{},"text":` + string(ansiScreen) + `}`,
				"read_agent":                              `{"agent":{},"text":"no box here"}`,
				"read_file:~/.claude/plans/happy-plan.md": "ERR path_not_allowed",
				"read_pane:wC:p1":                         `{"pane":{},"text":"Resume this session with:\nclaude --resume 0b1e2c3d-0000-4000-8000-0000000000aa\n$ "}`,
				"read_pane:wC:p2":                         `{"pane":{},"text":"$ ls\n$ "}`,
				"read_pane:wC:p3":                         "ERR pane_not_found",
			}),
			"workdone-ib5": answer(map[string]string{"console_snapshot": remoteSnap, "read_agent": `{"agent":{},"text":"Should I?\n` + rule + `\n❯ go ahead\n` + rule + `"}`}),
		},
		failHost: map[string]error{"workdone-ib1": errors.New("ssh: connect timed out")},
	}
	snap, err := collect(context.Background(), collectOptions{
		endpoints: writeEndpoints(t), overrides: map[string]string{"workbox": "box-059ed8"},
		callTimeout: 5 * time.Second, run: f.run, now: func() time.Time { return time.Date(2026, 10, 15, 16, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}

	byLabel := map[string]MachineStatus{}
	for _, m := range snap.Machines {
		byLabel[m.Label] = m
	}
	wantStates := map[string]string{"local": "reachable", "workbox": "reachable", "workbox2": "paused", "workbox5": "reachable"}
	for k, v := range wantStates {
		if byLabel[k].State != v {
			t.Errorf("machine %s state = %q, want %q", k, byLabel[k].State, v)
		}
	}
	if byLabel["workbox"].GatewayError == "" {
		t.Errorf("workbox: herdr answered but the gateway did not; want gateway_error, got %+v", byLabel["workbox"])
	}
	if l := byLabel["local"]; l.ShellPanes != 1 || l.UninspectedPanes != 1 {
		t.Errorf("local shell/uninspected = %d/%d, want 1/1", l.ShellPanes, l.UninspectedPanes)
	}
	for _, c := range f.calls {
		if (c[0] == "ssh" && c[len(c)-2] == "workdone-ib2") || (c[0] == "herdr" && contains(c, "workbox2")) {
			t.Errorf("paused box was contacted: %v", c)
		}
	}
	if len(f.resumed) != 0 {
		t.Errorf("boxes resumed without --wake: %v", f.resumed)
	}

	items := map[string]Item{}
	for _, it := range snap.Items {
		items[it.ID] = it
	}
	d := items["local/wA:p1"]
	if d.Dialog == nil || d.Dialog.ID != "abc" || len(d.Dialog.Options) != 2 || !d.NeedsYou || d.InputLine != nil {
		t.Errorf("dialog item = %+v", d)
	}
	if d.CompletionSeq == nil || *d.CompletionSeq != 11 || d.StateChangeSeq == nil || *d.StateChangeSeq != 12 {
		t.Errorf("seqs must come from herdr: %v %v", d.CompletionSeq, d.StateChangeSeq)
	}
	q := items["local/wA:p2"]
	if q.Last == nil || q.Last.Source != sourceTranscript || !strings.Contains(q.Last.Text, "which one?") || q.Last.Truncated {
		t.Errorf("question item should carry the full transcript reply, got %+v", q.Last)
	}
	if q.TranscriptRef == nil || q.TranscriptRef.Kind != refClaudeJSONL || q.TranscriptRef.Session != "0b1e2c3d-0000-4000-8000-000000000001" {
		t.Errorf("claude transcript_ref from herdr's agent_session, got %+v", q.TranscriptRef)
	}
	if w := items["local/wA:p3"]; w.NeedsYou || w.Status != "working" || w.TranscriptRef != nil {
		t.Errorf("working codex item = %+v", w)
	}
	p := items["local/wA:p4"]
	if p.Plan == nil || p.Plan.Path != "~/.claude/plans/happy-plan.md" || !strings.Contains(p.Plan.Error, "path_not_allowed") {
		t.Errorf("plan item = %+v", p.Plan)
	}
	g := items["local/wA:p5"]
	if g.InputLine == nil || g.InputLine.Text != "commit it" || g.InputLine.Ghost == nil || !*g.InputLine.Ghost {
		t.Errorf("dim input line must be ghost: %+v", g.InputLine)
	}
	if g.TranscriptRef == nil || g.TranscriptRef.Path != "/x/s.jsonl" {
		t.Errorf("gateway's ref wins: %+v", g.TranscriptRef)
	}
	if bg := items["local/wA:p9"]; bg.Status != statusBackground {
		t.Errorf("background item = %+v", bg)
	}
	u := items["local/wB:p1"]
	if u.Status != statusUnreadable || u.HerdrStatus != "idle" || u.CWD != "/Users/me" || u.CompletionSeq == nil || *u.CompletionSeq != 61 || u.Last != nil {
		t.Errorf("herdr-only item = %+v", u)
	}
	e := items["local/wC:p1"]
	if e.Status != statusExited || e.ResumeCommand != "claude --resume 0b1e2c3d-0000-4000-8000-0000000000aa" || e.Agent != "claude" || e.TranscriptRef == nil {
		t.Errorf("exited item = %+v", e)
	}
	if _, ok := items["local/wC:p2"]; ok {
		t.Errorf("a plain shell pane must not be an item")
	}
	r := items["workbox5/w3:p4"]
	if r.Box != "ib5" || r.Last == nil || r.Last.Source != sourceScreen || r.Last.Text != "Should I?" {
		t.Errorf("remote screen item last = %+v", r.Last)
	}
	if r.InputLine == nil || r.InputLine.Text != "go ahead" || r.InputLine.Ghost != nil || len(r.Notes) != 2 {
		t.Errorf("remote input line without ANSI must stay unverified and say why: %+v notes %q", r.InputLine, r.Notes)
	}
	if r.CompletionSeq == nil || *r.CompletionSeq != 39 {
		t.Errorf("remote seq = %v, want 39", r.CompletionSeq)
	}
	if len(items) != 9 {
		t.Errorf("got %d items, want 9", len(items))
	}
}

func TestInputLineANSI(t *testing.T) {
	rule := strings.Repeat("─", 20)
	tests := []struct {
		name, line, want string
		ghost            *bool
		ok               bool
	}{
		{"all dim", "\x1b[0m❯ \x1b[2mcommit it\x1b[0m", "commit it", ptr(true), true},
		{"dim via 2;37", "❯ \x1b[2;37mtry curl\x1b[22m", "try curl", ptr(true), true},
		{"typed", "\x1b[0m❯ \x1b[39mfix the test\x1b[0m", "fix the test", ptr(false), true},
		{"mixed", "❯ fix \x1b[2mthe test\x1b[0m", "fix the test", nil, true},
		{"empty", "❯ \x1b[7m \x1b[0m", "", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ghost, ok := inputLineANSI("x\n\x1b[38;5;8m" + rule + "\x1b[0m\n" + tt.line + "\n" + rule)
			if got != tt.want || ok != tt.ok || (ghost == nil) != (tt.ghost == nil) || (ghost != nil && *ghost != *tt.ghost) {
				t.Errorf("got %q ghost=%v ok=%v; want %q ghost=%v ok=%v", got, ghost, ok, tt.want, tt.ghost, tt.ok)
			}
		})
	}
}

func ptr(b bool) *bool { return &b }

func TestResumeCommand(t *testing.T) {
	tests := []struct{ screen, cmd, kind string }{
		{"Resume this session with:\nclaude --resume 0b1e2c3d-0000-4000-8000-0000000000aa", "claude --resume 0b1e2c3d-0000-4000-8000-0000000000aa", "claude"},
		{"To reconnect, run: codex resume 019a2b3c-4d5e", "codex resume 019a2b3c-4d5e", "codex"},
		{"Continue  opencode -s ses_7abC9", "opencode -s ses_7abC9", "opencode"},
		{"$ ls", "", ""},
	}
	for _, tt := range tests {
		cmd, kind, ok := resumeCommand(tt.screen)
		if cmd != tt.cmd || kind != tt.kind || ok != (tt.cmd != "") {
			t.Errorf("resumeCommand(%q) = %q %q %v", tt.screen, cmd, kind, ok)
		}
	}
}

func TestCollectWake(t *testing.T) {
	f := &fakeGateway{
		boxes: `[{"name":"ib2","state":"suspended"},{"name":"ib5","state":"running"}]`,
		perHost: map[string]func([]byte) []byte{
			"":             answer(map[string]string{"console_snapshot": `{"agents":[]}`}),
			"workdone-ib1": answer(map[string]string{"console_snapshot": `{"agents":[]}`}),
			"workdone-ib2": answer(map[string]string{"bridge_status": `{}`, "console_snapshot": `{"agents":[]}`}),
			"workdone-ib5": answer(map[string]string{"console_snapshot": `{"agents":[]}`}),
		},
	}
	snap, err := collect(context.Background(), collectOptions{endpoints: writeEndpoints(t), wake: true, callTimeout: 5 * time.Second, run: f.run, now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(f.resumed, ",") != "ib2" {
		t.Errorf("resumed %v, want only ib2", f.resumed)
	}
	for _, m := range snap.Machines {
		if m.Label == "workbox2" && (!m.Woken || m.State != "reachable") {
			t.Errorf("workbox2 = %+v, want woken and reachable", m)
		}
	}
}

func TestCallGatewayRefusesWritingOps(t *testing.T) {
	for _, op := range []string{"prompt_agent", "answer_agent", "send_agent_keys", "watch_agent", "prunable_agents", "exec"} {
		called := false
		run := func(context.Context, []byte, []string) ([]byte, error) { called = true; return nil, nil }
		_, _, err := callGateway(context.Background(), run, machine{Label: "local"}, []gatewayRequest{{ID: "1", Op: op}})
		if err == nil || called {
			t.Errorf("op %s: err=%v called=%v; want refusal before anything runs", op, err, called)
		}
	}
}

func TestCallGatewayPerRequestErrors(t *testing.T) {
	run := func(_ context.Context, stdin []byte, _ []string) ([]byte, error) {
		return answer(map[string]string{"bridge_status": `{"ok":1}`})(stdin), nil
	}
	res, errs, err := callGateway(context.Background(), run, machine{Label: "local"}, []gatewayRequest{
		{ID: "a", Op: "bridge_status", Params: map[string]any{}},
		{ID: "b", Op: "read_agent", Params: map[string]any{}},
	})
	if err != nil || errs[0] != nil || string(res[0]) != `{"ok":1}` {
		t.Fatalf("first request: res=%s errs=%v err=%v", res, errs, err)
	}
	var ge *gatewayError
	if !errors.As(errs[1], &ge) || ge.Code != "unknown_op" {
		t.Errorf("second request error = %v, want gateway error unknown_op", errs[1])
	}
}

func TestMachineCommand(t *testing.T) {
	remote := machine{Label: "workbox5", Target: "workdone-ib5"}.command()
	if remote[0] != "ssh" || !contains(remote, "BatchMode=yes") || remote[len(remote)-2] != "workdone-ib5" || remote[len(remote)-1] != gatewayLauncher {
		t.Errorf("remote argv = %v", remote)
	}
	local := machine{Label: "local"}.command()
	if len(local) != 1 || !strings.HasSuffix(local[0], "/.local/libexec/herdr-chatgpt/herdr-gateway-launcher.sh") || strings.Contains(local[0], "~") {
		t.Errorf("local argv = %v", local)
	}
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func TestPrintSnapshot(t *testing.T) {
	var buf bytes.Buffer
	printSnapshot(&buf, &Snapshot{
		CollectedAt: "2026-10-15T16:00:00Z",
		Machines: []MachineStatus{
			{Label: "local", State: "reachable", Agents: 1, MS: 600},
			{Label: "workbox2", Box: "ib2", BoxState: "suspended", State: "paused"},
		},
		Items: []Item{{ID: "local/wA:p1", Machine: "local", PaneID: "wA:p1", Agent: "claude", Status: "done", Attention: "question", NeedsYou: true,
			Last: &LastText{Text: "Which one?", Source: "transcript"}, InputLine: &InputLine{Text: "go ahead"}}},
	})
	out := buf.String()
	for _, want := range []string{"1 of 1 panes need you", "workbox2 (ib2, suspended): paused, not contacted", "* wA:p1", "Which one?", "may be a ghost suggestion"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}
