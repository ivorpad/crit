package herdr

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTranscriptErrors(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	resumed := "44444444-4444-4444-8444-444444444444"
	mustWrite(t, filepath.Join(home, ".claude", "projects", "-src-a", resumed+".jsonl"), lineUser)
	mustWrite(t, filepath.Join(home, ".claude", "sessions", "2222.json"), `{"sessionId":"`+sessionB+`","cwd":"/src/a"}`)
	mustWrite(t, filepath.Join(home, ".claude", "sessions", "3333.json"), `{"sessionId":"../../../etc/passwd","cwd":"/src/a"}`)
	box := t.TempDir()
	mustWrite(t, filepath.Join(box, ".claude", "sessions", "900.json"), `{"sessionId":"`+sessionB+`","cwd":"/home/u/app"}`)

	claudePID := func(pid string) string {
		return `[{"argv":["claude"],"argv0":"claude","name":"2.1.300","pid":` + pid + `}]`
	}
	f := &fakeBoxes{
		procs: map[string]string{
			"local/wA:p3":    `[{"argv":["zsh"],"argv0":"zsh","name":"zsh","pid":10}]`,
			"local/wA:p4":    claudePID("1234"),
			"local/wA:p5":    `[{"argv":["zsh"],"argv0":"zsh","name":"zsh","pid":10}]`,
			"local/wA:p8":    claudePID("2222"),
			"local/wA:p9":    `[{"argv":["claude"],"name":"claude","pid":"1234; touch /tmp/x"}]`,
			"local/wB:p1":    claudePID("3333"),
			"workbox3/w3:p1": `[{"argv":["claude"],"name":"claude","pid":900}]`,
			"workbox3/w3:p2": `[{"argv":["claude"],"name":"claude","pid":900}]`,
		},
		homes: map[string]string{"workdone-ib3": box},
		fail: map[string]error{
			"local/wA:p7":  errors.New("herdr: context deadline exceeded"),
			"workdone-ib9": errors.New("ssh: connect to host workdone-ib9: Operation timed out"),
		},
	}
	c := newTranscriptController(f,
		Item{Machine: localLabel, PaneID: "wA:p2", Agent: "gemini"},
		Item{Machine: localLabel, PaneID: "wA:p3", Agent: "claude"},
		Item{Machine: localLabel, PaneID: "wA:p4", Agent: "claude"},
		Item{Machine: localLabel, PaneID: "wA:p5", Agent: "claude", Status: statusExited, CWD: "/src/a",
			TranscriptRef: &TranscriptRef{Kind: refClaudeJSONL, Session: resumed}},
		Item{Machine: localLabel, PaneID: "wA:p6", Agent: "claude"},
		Item{Machine: localLabel, PaneID: "wA:p7", Agent: "claude"},
		Item{Machine: localLabel, PaneID: "wA:p8", Agent: "claude"},
		Item{Machine: localLabel, PaneID: "wA:p9", Agent: "claude"},
		Item{Machine: localLabel, PaneID: "wB:p1", Agent: "claude"},
		Item{Machine: "workbox3", PaneID: "w3:p1", Agent: "claude"},
		Item{Machine: "workbox9", PaneID: "w9:p1", Agent: "claude"},
		Item{Machine: "syno", PaneID: "s1:p1", Agent: "claude"},
	)
	c.snap.Machines = append(c.snap.Machines, MachineStatus{Label: "workbox9", Target: "workdone-ib9"})
	f.setProcs("workbox9/w9:p1", `[{"argv":["claude"],"name":"claude","pid":5}]`)

	tests := []struct {
		name, path string
		want       int
		error      string
		reason     string
	}{
		{"not in the sweep", "local/nope.md", http.StatusNotFound, "unknown pane", ""},
		{"an agent with no reader", "local/wA:p2.md", http.StatusNotFound, "no transcript", "No gemini reader yet"},
		{"no claude process", "local/wA:p3.md", http.StatusNotFound, "no transcript", "No claude process in this pane"},
		{"no session file for the pid", "local/wA:p4.md", http.StatusNotFound, "no transcript", "No session file for pid 1234 on local"},
		{"a pid that is not an integer is never used", "local/wA:p9.md", http.StatusNotFound, "no transcript", "No claude process in this pane"},
		{"a session id that is not a UUID is never used", "local/wB:p1.md", http.StatusNotFound, "no transcript", "No session file for pid 3333 on local"},
		{"a session with no transcript yet", "local/wA:p8.md", http.StatusNotFound, "no transcript", "No transcript yet for session " + sessionB + " on local"},
		{"herdr no longer knows the pane", "local/wA:p6.md", http.StatusNotFound, "no transcript", "herdr no longer knows pane wA:p6 on local"},
		{"herdr fails", "local/wA:p7.md", http.StatusBadGateway, "herdr: context deadline exceeded", ""},
		{"a box session with no transcript yet", "workbox3/w3:p1.md", http.StatusNotFound, "no transcript", "No transcript yet for session " + sessionB + " on workbox3"},
		{"ssh fails", "workbox9/w9:p1.md", http.StatusBadGateway, "ssh: connect to host workdone-ib9: Operation timed out", ""},
		{"a machine the sweep has no target for", "syno/s1:p1.md", http.StatusBadGateway, "no ssh target for syno in the latest sweep", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, a := getTranscript(t, c, tt.path, "")
			if status != tt.want || a.Error != tt.error || a.Reason != tt.reason {
				t.Errorf("got %d %q %q, want %d %q %q", status, a.Error, a.Reason, tt.want, tt.error, tt.reason)
			}
		})
	}

	t.Run("an exited pane falls back to its resume line's session", func(t *testing.T) {
		status, a := getTranscript(t, c, "local/wA:p5.md", "")
		if status != http.StatusOK || a.SessionID != resumed || a.JSONL != lineUser {
			t.Errorf("got %d %+v", status, a)
		}
	})
	t.Run("a transcript that was read and is gone is a failed read", func(t *testing.T) {
		if err := os.Remove(filepath.Join(home, ".claude", "projects", "-src-a", resumed+".jsonl")); err != nil {
			t.Fatal(err)
		}
		status, a := getTranscript(t, c, "local/wA:p5.md", "")
		if status != http.StatusBadGateway || !strings.Contains(a.Error, "is gone") {
			t.Errorf("got %d %+v", status, a)
		}
	})
	for _, c := range f.calls {
		if strings.Contains(strings.Join(c, " "), "touch") || strings.Contains(strings.Join(c, " "), "passwd") {
			t.Errorf("an unchecked value reached a command: %q", c)
		}
	}
}

func TestTranscriptReadCap(t *testing.T) {
	old := maxReadBytes
	maxReadBytes = 1 << 20
	t.Cleanup(func() { maxReadBytes = old })
	home, box := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	// One byte repeated gzips to almost nothing: on a box this is the shape of a
	// gzip bomb, small on the wire and large once unpacked.
	atCap := strings.Repeat("x", int(maxReadBytes)-1) + "\n"
	overCap := strings.Repeat("x", int(maxReadBytes)) + "\n"
	for _, root := range []struct{ dir, cwd, slug string }{{home, "/src/a", "-src-a"}, {box, "/home/u/app", "-home-u-app"}} {
		for pid, s := range map[string]string{"1": sessionA, "2": sessionB} {
			mustWrite(t, filepath.Join(root.dir, ".claude", "sessions", pid+".json"), `{"sessionId":"`+s+`","cwd":"`+root.cwd+`"}`)
		}
		mustWrite(t, filepath.Join(root.dir, ".claude", "projects", root.slug, sessionA+".jsonl"), atCap)
		mustWrite(t, filepath.Join(root.dir, ".claude", "projects", root.slug, sessionB+".jsonl"), overCap)
	}
	pid := func(n string) string { return `[{"argv":["claude"],"name":"claude","pid":` + n + `}]` }
	f := &fakeBoxes{
		procs: map[string]string{"local/wA:p1": pid("1"), "local/wA:p2": pid("2"), "workbox3/w3:p1": pid("1"), "workbox3/w3:p2": pid("2")},
		homes: map[string]string{"workdone-ib3": box},
	}
	c := newTranscriptController(f,
		Item{Machine: localLabel, PaneID: "wA:p1", Agent: "claude"},
		Item{Machine: localLabel, PaneID: "wA:p2", Agent: "claude"},
		Item{Machine: "workbox3", PaneID: "w3:p1", Agent: "claude"},
		Item{Machine: "workbox3", PaneID: "w3:p2", Agent: "claude"},
	)
	tests := []struct {
		name, path string
		want       int
		error      string
	}{
		{"local, exactly the cap", "local/wA:p1.md", http.StatusOK, ""},
		{"local, one byte over", "local/wA:p2.md", http.StatusBadGateway, "stopped reading the transcript at 1 MB, far past any real one"},
		{"box, exactly the cap once unpacked", "workbox3/w3:p1.md", http.StatusOK, ""},
		{"box, one byte over once unpacked", "workbox3/w3:p2.md", http.StatusBadGateway, "stopped reading the transcript at 1 MB, far past any real one"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, a := getTranscript(t, c, tt.path, "")
			if status != tt.want || a.Error != tt.error || (status == http.StatusOK && a.Bytes != maxReadBytes) {
				t.Errorf("got %d %q (%d bytes), want %d %q", status, a.Error, a.Bytes, tt.want, tt.error)
			}
		})
	}
}
