package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeBoxes answers `herdr [--machine X] pane process-info` from procs and runs
// every ssh script for real, with sh, in a temp directory standing in for that
// box's home. It never connects anywhere.
type fakeBoxes struct {
	mu    sync.Mutex
	procs map[string]string // "<machine>/<pane>" -> foreground_processes JSON
	homes map[string]string // ssh target -> its fake home
	fail  map[string]error  // "<machine>/<pane>" or ssh target -> error
	calls [][]string
}

func (f *fakeBoxes) run(ctx context.Context, _ []byte, argv []string) ([]byte, error) {
	f.mu.Lock()
	f.calls = append(f.calls, argv)
	f.mu.Unlock()
	switch argv[0] {
	case "herdr":
		label, rest := localLabel, argv[1:]
		if rest[0] == "--machine" {
			label, rest = rest[1], rest[2:]
		}
		if len(rest) != 4 || rest[0] != "pane" || rest[1] != "process-info" || rest[2] != "--pane" {
			return nil, fmt.Errorf("unexpected herdr call %q", argv)
		}
		key := label + "/" + rest[3]
		f.mu.Lock()
		procs, ok := f.procs[key]
		err := f.fail[key]
		f.mu.Unlock()
		if err != nil {
			return nil, err
		}
		if !ok {
			// What execRunner makes of herdr's answer for a pane it does not know.
			return nil, errors.New(`herdr: exit status 1: {"error":{"code":"pane_not_found","message":"pane not found"},"id":"cli:pane:process_info"}`)
		}
		return processInfo(procs), nil
	case "ssh":
		target := argv[len(argv)-2]
		f.mu.Lock()
		home, err := f.homes[target], f.fail[target]
		f.mu.Unlock()
		if err != nil {
			return nil, err
		}
		// The remote login shell gets the command as one string, as ssh sends it.
		cmd := exec.CommandContext(ctx, "sh", "-c", argv[len(argv)-1])
		cmd.Env = []string{"HOME=" + home, "PATH=" + os.Getenv("PATH")}
		return cmd.Output()
	}
	return nil, fmt.Errorf("unexpected call %q", argv)
}

func (f *fakeBoxes) setProcs(key, procs string) {
	f.mu.Lock()
	f.procs[key] = procs
	f.mu.Unlock()
}

// callsSince counts the herdr and ssh calls made after the first n.
func (f *fakeBoxes) callsSince(n int) (herdrCalls, sshCalls int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls[n:] {
		if c[0] == "ssh" {
			sshCalls++
		} else {
			herdrCalls++
		}
	}
	return herdrCalls, sshCalls
}

func (f *fakeBoxes) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

type transcriptAnswer struct {
	Path         string `json:"path"`
	Machine      string `json:"machine"`
	PaneID       string `json:"pane_id"`
	Kind         string `json:"kind"`
	Source       string `json:"source"`
	SessionID    string `json:"session_id"`
	File         string `json:"file"`
	Bytes        int64  `json:"bytes"`
	DroppedBytes int64  `json:"dropped_bytes"`
	Clipped      int    `json:"clipped"`
	Version      string `json:"version"`
	From         int    `json:"from"`
	JSONL        string `json:"jsonl"`
	FetchedAt    string `json:"fetched_at"`
	Error        string `json:"error"`
	Reason       string `json:"reason"`
}

func getTranscript(t *testing.T, c *Controller, path, since string) (int, transcriptAnswer) {
	t.Helper()
	status, body := c.TranscriptJSON(context.Background(), path, since)
	var a transcriptAnswer
	if err := json.Unmarshal(body, &a); err != nil {
		t.Fatalf("answer is not JSON: %s", body)
	}
	return status, a
}

func newTranscriptController(f *fakeBoxes, items ...Item) *Controller {
	c := NewController(Options{})
	c.col.run = f.run
	c.reuse = 0
	for i := range items {
		items[i].ID = items[i].Machine + "/" + items[i].PaneID
	}
	c.snap = &Snapshot{
		Machines: []MachineStatus{{Label: localLabel}, {Label: "workbox3", Target: "workdone-ib3", Box: "ib3"}},
		Items:    items,
	}
	return c
}

// transcriptStep is one request in a sequence against the same pane: change
// the files, ask with since (the last answer's version unless set), then hold
// the text the way the page does and compare it.
type transcriptStep struct {
	name    string
	change  func()
	since   string // "": the last version; "none": no since; "version N": step N's, its text held again
	full    bool   // the answer must start over (from 0)
	want    string // the text the page holds afterwards
	session string
	herdr   int // calls this step must make
	ssh     int
	check   func(t *testing.T, a transcriptAnswer)
}

func runSteps(t *testing.T, c *Controller, f *fakeBoxes, path string, steps []transcriptStep) {
	t.Helper()
	held, last := "", ""
	var versions, texts []string
	for _, st := range steps {
		if st.change != nil {
			st.change()
		}
		since := last
		switch {
		case st.since == "none":
			since = ""
		case strings.HasPrefix(st.since, "version "):
			// The page still holds the text of that answer.
			var i int
			_, _ = fmt.Sscanf(st.since, "version %d", &i)
			since, held = versions[i], texts[i]
		case st.since != "":
			since = st.since
		}
		n := f.count()
		status, a := getTranscript(t, c, path, since)
		if status != http.StatusOK {
			t.Fatalf("%s: status %d: %+v", st.name, status, a)
		}
		switch {
		case st.full && a.From != 0:
			t.Errorf("%s: from = %d, want 0 (everything)", st.name, a.From)
		case !st.full && a.From != len(held):
			t.Errorf("%s: from = %d, want %d (an append)", st.name, a.From, len(held))
		}
		if a.From <= len(held) {
			held = held[:a.From] + a.JSONL
		}
		if held != st.want {
			t.Errorf("%s: page holds\n%q\nwant\n%q", st.name, held, st.want)
		}
		if a.SessionID != st.session || !strings.HasPrefix(a.Version, st.session+":") {
			t.Errorf("%s: session %q version %q, want session %q", st.name, a.SessionID, a.Version, st.session)
		}
		if h, s := f.callsSince(n); h != st.herdr || s != st.ssh {
			t.Errorf("%s: %d herdr and %d ssh calls, want %d and %d", st.name, h, s, st.herdr, st.ssh)
		}
		if st.check != nil {
			st.check(t, a)
		}
		last = a.Version
		versions, texts = append(versions, a.Version), append(texts, held)
	}
}

const (
	lineUser   = `{"type":"user","uuid":"u1","message":{"role":"user","content":"look at <b>this</b> & that"}}` + "\n"
	lineAnswer = `{"type":"assistant","uuid":"a1","message":{"role":"assistant","content":[{"type":"text","text":"done"}]}}` + "\n"
	lineNext   = `{"type":"user","uuid":"u2","message":{"role":"user","content":"next"}}` + "\n"
	lineLast   = `{"type":"assistant","uuid":"a2","message":{"role":"assistant","content":[{"type":"text","text":"ok"}]}}` + "\n"
	lineOther  = `{"type":"user","uuid":"n1","message":{"role":"user","content":"after /clear"}}` + "\n"
	lineShort  = `{"type":"user","uuid":"s1"}` + "\n"
)

var lineDebug = `{"type":"api-request","uuid":"r1","body":"` + strings.Repeat("x", 500) + `"}` + "\n"

func appendTo(t *testing.T, p, s string) {
	t.Helper()
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(s); err != nil {
		t.Fatal(err)
	}
}

func TestTranscriptLocal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	mustWrite(t, filepath.Join(home, ".claude.json"), "{}")
	if err := os.MkdirAll(filepath.Join(home, ".claude", "sessions"), 0o700); err != nil {
		t.Fatal(err)
	}
	sessions := filepath.Join(home, ".claude-work", "sessions", "4242.json")
	mustWrite(t, sessions, `{"pid":4242,"sessionId":"`+sessionA+`","cwd":"/Users/me/src/app","startedAt":1}`)
	first := filepath.Join(home, ".claude-work", "projects", "-Users-me-src-app", sessionA+".jsonl")
	second := filepath.Join(home, ".claude-work", "projects", "-Users-me-src-app", sessionB+".jsonl")
	half := lineNext[:20]
	mustWrite(t, first, lineUser+lineDebug+lineAnswer+half)

	f := &fakeBoxes{procs: map[string]string{
		"local/wA:p1": `[{"argv":["caffeinate"],"argv0":"caffeinate","name":"caffeinate","pid":10},{"argv":["claude"],"argv0":"claude","name":"2.1.300","pid":4242}]`,
	}}
	c := newTranscriptController(f, Item{Machine: localLabel, PaneID: "wA:p1", Agent: "claude", Status: "done"})

	runSteps(t, c, f, "local/wA:p1.md", []transcriptStep{
		{name: "first read: no request debug, the half-written line waits", since: "none", full: true,
			want: lineUser + lineAnswer, session: sessionA, herdr: 1,
			check: func(t *testing.T, a transcriptAnswer) {
				want := transcriptAnswer{Path: "local/wA:p1.md", Machine: localLabel, PaneID: "wA:p1", Kind: "claude_jsonl", Source: "local",
					File: "~/.claude-work/projects/-Users-me-src-app/" + sessionA + ".jsonl", Bytes: int64(len(lineUser + lineDebug + lineAnswer)),
					DroppedBytes: int64(len(lineDebug))}
				got := a
				got.SessionID, got.Version, got.From, got.JSONL, got.FetchedAt = "", "", 0, "", ""
				if got != want {
					t.Errorf("answer = %+v\nwant %+v", got, want)
				}
				if _, err := time.Parse(time.RFC3339, a.FetchedAt); err != nil {
					t.Errorf("fetched_at = %q", a.FetchedAt)
				}
			}},
		{name: "the line completes and one more follows: an append", change: func() { appendTo(t, first, lineNext[20:]+lineLast) },
			want: lineUser + lineAnswer + lineNext + lineLast, session: sessionA, herdr: 1},
		{name: "nothing new: an empty append",
			want: lineUser + lineAnswer + lineNext + lineLast, session: sessionA, herdr: 1},
		{name: "an older version of the same read: an append from there", since: "version 0",
			want: lineUser + lineAnswer + lineNext + lineLast, session: sessionA, herdr: 1},
		{name: "a since that names no version: everything", since: sessionA + ":nope:3", full: true,
			want: lineUser + lineAnswer + lineNext + lineLast, session: sessionA, herdr: 1},
		{name: "a /clear writes a new session: everything", change: func() {
			mustWrite(t, second, lineOther)
			mustWrite(t, sessions, `{"pid":4242,"sessionId":"`+sessionB+`","cwd":"/Users/me/src/app"}`)
		}, full: true, want: lineOther, session: sessionB, herdr: 1},
		{name: "the file shrank: everything", change: func() { mustWrite(t, second, lineShort) },
			full: true, want: lineShort, session: sessionB, herdr: 1},
		{name: "a version from before the shrink starts over", since: "version 5", full: true,
			want: lineShort, session: sessionB, herdr: 1},
	})
}

func TestTranscriptSSH(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	box := t.TempDir()
	first := filepath.Join(box, ".claude", "projects", "-home-u-app", sessionA+".jsonl")
	second := filepath.Join(box, ".claude", "projects", "-home-u-app", sessionB+".jsonl")
	third := filepath.Join(box, ".claude-work", "projects", "-home-u-other", sessionC+".jsonl")
	mustWrite(t, filepath.Join(box, ".claude", "sessions", "777.json"), `{"pid":777,"sessionId":"`+sessionA+`","cwd":"/home/u/app"}`)
	half := lineNext[:30]
	mustWrite(t, first, lineUser+lineDebug+lineAnswer+half)

	f := &fakeBoxes{
		procs: map[string]string{"workbox3/w3:p4": `[{"argv":["claude"],"name":"claude","pid":777}]`},
		homes: map[string]string{"workdone-ib3": box},
	}
	c := newTranscriptController(f, Item{Machine: "workbox3", Box: "ib3", PaneID: "w3:p4", Agent: "claude", Status: "done"})

	runSteps(t, c, f, "workbox3/w3:p4.md", []transcriptStep{
		{name: "cold read: process-info, locate, fetch", since: "none", full: true,
			want: lineUser + lineAnswer, session: sessionA, herdr: 1, ssh: 2,
			check: func(t *testing.T, a transcriptAnswer) {
				if a.Source != "ssh" || a.File != "~/.claude/projects/-home-u-app/"+sessionA+".jsonl" ||
					a.Bytes != int64(len(lineUser+lineDebug+lineAnswer)) || a.DroppedBytes != int64(len(lineDebug)) || a.Machine != "workbox3" {
					t.Errorf("answer = %+v", a)
				}
			}},
		{name: "warm read: the new bytes in one ssh call", change: func() { appendTo(t, first, lineNext[30:]+lineLast) },
			want: lineUser + lineAnswer + lineNext + lineLast, session: sessionA, herdr: 1, ssh: 1},
		{name: "warm read with nothing new", want: lineUser + lineAnswer + lineNext + lineLast, session: sessionA, herdr: 1, ssh: 1},
		{name: "a /clear in the same process: the sessions file names a new session", change: func() {
			mustWrite(t, second, lineOther)
			mustWrite(t, filepath.Join(box, ".claude", "sessions", "777.json"), `{"pid":777,"sessionId":"`+sessionB+`","cwd":"/home/u/app"}`)
		}, full: true, want: lineOther, session: sessionB, herdr: 1, ssh: 2},
		{name: "the file shrank", change: func() { mustWrite(t, second, lineShort) },
			full: true, want: lineShort, session: sessionB, herdr: 1, ssh: 1},
		{name: "claude restarted with a new pid and another config dir", change: func() {
			_ = os.Remove(filepath.Join(box, ".claude", "sessions", "777.json"))
			mustWrite(t, filepath.Join(box, ".claude-work", "sessions", "778.json"), `{"pid":778,"sessionId":"`+sessionC+`","cwd":"/home/u/other"}`)
			mustWrite(t, third, lineNext)
			f.setProcs("workbox3/w3:p4", `[{"argv":["/home/u/.local/bin/claude"],"name":"2.1.300","pid":778}]`)
		}, full: true, want: lineNext, session: sessionC, herdr: 1, ssh: 3,
			check: func(t *testing.T, a transcriptAnswer) {
				if a.File != "~/.claude-work/projects/-home-u-other/"+sessionC+".jsonl" {
					t.Errorf("file = %q", a.File)
				}
			}},
	})
}
