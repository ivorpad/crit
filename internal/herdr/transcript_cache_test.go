package herdr

import (
	"context"
	"net/http"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/tomasz-tomczyk/crit/internal/session"
)

func TestTranscriptReadsAreShared(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	box := t.TempDir()
	mustWrite(t, filepath.Join(box, ".claude", "sessions", "777.json"), `{"sessionId":"`+sessionA+`","cwd":"/home/u/app"}`)
	mustWrite(t, filepath.Join(box, ".claude", "projects", "-home-u-app", sessionA+".jsonl"), lineUser)
	f := &fakeBoxes{
		procs: map[string]string{"workbox3/w3:p4": `[{"argv":["claude"],"name":"claude","pid":777}]`},
		homes: map[string]string{"workdone-ib3": box},
	}
	c := newTranscriptController(f, Item{Machine: "workbox3", PaneID: "w3:p4", Agent: "claude"})
	var wg sync.WaitGroup
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if status, _ := c.TranscriptJSON(context.Background(), "workbox3/w3:p4.md", ""); status != http.StatusOK {
				t.Errorf("status %d", status)
			}
		}()
	}
	wg.Wait()
	// Five requests at once share reads: at most a cold read and a warm one.
	if h, _ := f.callsSince(0); h > 2 {
		t.Errorf("%d process-info calls for 5 requests at once, want at most 2", h)
	}

	c.reuse = time.Hour
	n := f.count()
	if status, _ := c.TranscriptJSON(context.Background(), "workbox3/w3:p4.md", ""); status != http.StatusOK || f.count() != n {
		t.Errorf("a fresh read must answer without reading again: %d calls", f.count()-n)
	}
}

func TestKeepTranscripts(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	box := t.TempDir()
	transcript := filepath.Join(box, ".claude", "projects", "-home-u-app", sessionA+".jsonl")
	mustWrite(t, filepath.Join(box, ".claude", "sessions", "777.json"), `{"sessionId":"`+sessionA+`","cwd":"/home/u/app"}`)
	mustWrite(t, transcript, lineUser)
	f := &fakeBoxes{
		procs: map[string]string{"workbox3/w3:p4": `[{"argv":["claude"],"name":"claude","pid":777}]`, "workbox3/w3:p5": `[]`},
		homes: map[string]string{"workdone-ib3": box},
	}
	box3 := Item{Machine: "workbox3", PaneID: "w3:p4", Agent: "claude", CompletionSeq: seq(5)}
	quiet := Item{Machine: "workbox3", PaneID: "w3:p6", Agent: "claude", CompletionSeq: seq(2)}
	leaving := Item{Machine: "workbox3", PaneID: "w3:p5", Agent: "claude", CompletionSeq: seq(1)}
	never := Item{Machine: "workbox3", PaneID: "w3:p7", Agent: "claude", CompletionSeq: seq(1)}
	mac := Item{Machine: localLabel, PaneID: "wA:p1", Agent: "claude", CompletionSeq: seq(3)}
	c := newTranscriptController(f, box3, quiet, leaving, never, mac)
	if status, a := getTranscript(t, c, "workbox3/w3:p4.md", ""); status != http.StatusOK || a.JSONL != lineUser {
		t.Fatalf("first read: %d %+v", status, a)
	}
	sess, err := session.NewSyntheticSession("herdr", []string{"herdr"}, nil, t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	events := sess.Subscribe()
	c.mu.Lock()
	c.sess = sess
	for _, id := range []string{"workbox3/w3:p5", "workbox3/w3:p6", "local/wA:p1"} {
		c.transcripts[id] = &transcriptState{entry: &transcriptEntry{session: sessionB, fetchedAt: time.Now()}}
	}
	c.mu.Unlock()

	appendTo(t, transcript, lineAnswer)
	n := f.count()
	moved := func(it Item, s int64) Item { it.ID, it.CompletionSeq = it.Machine+"/"+it.PaneID, seq(s); return it }
	next := &Snapshot{Machines: c.snap.Machines, Items: []Item{moved(box3, 6), moved(quiet, 2), moved(never, 9), moved(mac, 4)}}
	c.mu.Lock()
	old := c.snap
	c.snap = next
	c.keepTranscripts(old, next)
	c.mu.Unlock()

	select {
	case ev := <-events:
		if ev.Type != UpdatedEvent {
			t.Errorf("event %q, want %q", ev.Type, UpdatedEvent)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no herdr-updated after the background read")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.transcripts["workbox3/w3:p4"].entry; e == nil || string(e.text) != lineUser+lineAnswer {
		t.Errorf("the moved box pane was not read again: %+v", e)
	}
	if _, ok := c.transcripts["workbox3/w3:p5"]; ok {
		t.Errorf("a pane that left the sweep keeps its transcript")
	}
	if _, ok := c.transcripts["workbox3/w3:p6"]; !ok {
		t.Errorf("a pane still in the sweep lost its transcript")
	}
	if _, ok := c.transcripts["workbox3/w3:p7"]; ok {
		t.Errorf("a pane never read must not be read in the background")
	}
	// One warm read (process-info and one ssh call) for w3:p4; nothing for the
	// pane whose seq did not move or the local one.
	if h, s := f.callsSince(n); h != 1 || s != 1 {
		t.Errorf("%d herdr and %d ssh calls, want 1 and 1", h, s)
	}
}
