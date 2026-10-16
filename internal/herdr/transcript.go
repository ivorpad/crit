package herdr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tomasz-tomczyk/crit/internal/session"
)

const (
	// transcriptTimeout bounds one read. A cold read from a box takes 5 to 7 s.
	transcriptTimeout = 20 * time.Second
	// transcriptReuse is how long a finished read answers new requests without
	// reading again: the page asks right after a background refresh's herdr-updated.
	transcriptReuse = 3 * time.Second
)

// transcriptEntry is the daemon's copy of one pane's transcript: the file it came
// from, how far it was read, and the filtered text. Entries are not changed once
// made; a read makes a new one.
type transcriptEntry struct {
	source    string // "local" or "ssh"
	pid       int    // the claude process the join found; 0 for the fallback
	dir       string // ssh: the config dir name under ~
	path      string
	file      string
	session   string
	offset    int64  // file bytes read, up to the last newline
	text      []byte // the filtered JSONL
	dropped   int64
	clipped   int
	epoch     string // new for every read from byte 0
	fetchedAt time.Time
}

// transcriptState is a pane's last read and the read in flight for it.
type transcriptState struct {
	entry  *transcriptEntry
	flight *transcriptFlight
}

// transcriptFlight is one read. Every request for the pane while it runs waits
// for it instead of starting its own.
type transcriptFlight struct {
	done  chan struct{}
	entry *transcriptEntry
	err   error
}

// version names the text the page holds: the session, the read from byte 0 the
// text continues, and its length.
func (e *transcriptEntry) version() string {
	return e.session + ":" + e.epoch + ":" + strconv.Itoa(len(e.text))
}

// after is where the text past since starts: since's length when since is a
// version of this text, else 0 (everything).
func (e *transcriptEntry) after(since string) int {
	rest, ok := strings.CutPrefix(since, e.session+":"+e.epoch+":")
	n, err := strconv.Atoi(rest)
	if !ok || err != nil || n < 0 || n > len(e.text) || (n > 0 && e.text[n-1] != '\n') {
		return 0
	}
	return n
}

// transcriptView is the answer of GET /api/herdr/transcript.
type transcriptView struct {
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
}

// TranscriptJSON is GET /api/herdr/transcript: the transcript of the pane at
// path (Claude, Codex, pi, opencode, grok or cursor-agent), filtered, as HTTP
// status and body. With since (a version the page holds) only the lines after
// it are sent.
func (c *Controller) TranscriptJSON(ctx context.Context, path, since string) (int, []byte) {
	c.mu.Lock()
	it, ok := c.itemAt(path)
	c.mu.Unlock()
	if !ok {
		return marshalStatus(refuse(http.StatusNotFound, "unknown pane"))
	}
	e, err := c.transcript(ctx, it)
	if err != nil {
		return marshalStatus(transcriptRefusal(err))
	}
	from := e.after(since)
	kind := refClaudeJSONL
	if lr, ok := localReaders[it.Agent]; ok {
		kind = lr.kind
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(transcriptView{
		Path: path, Machine: it.Machine, PaneID: it.PaneID, Kind: kind, Source: e.source,
		SessionID: e.session, File: e.file, Bytes: e.offset, DroppedBytes: e.dropped, Clipped: e.clipped,
		Version: e.version(), From: from, JSONL: string(e.text[from:]), FetchedAt: e.fetchedAt.UTC().Format(time.RFC3339),
	})
	return http.StatusOK, buf.Bytes()
}

// transcript is the pane's transcript: the last read when it finished less than
// c.reuse ago, else a new read, shared by every request for the pane that comes
// while it runs.
func (c *Controller) transcript(ctx context.Context, it Item) (*transcriptEntry, error) {
	c.mu.Lock()
	st := c.transcripts[it.ID]
	if st == nil {
		st = &transcriptState{}
		c.transcripts[it.ID] = st
	}
	if e := st.entry; st.flight == nil && e != nil && c.col.now().Sub(e.fetchedAt) < c.reuse {
		c.mu.Unlock()
		return e, nil
	}
	fl := c.startRead(st, it)
	c.mu.Unlock()
	select {
	case <-fl.done:
		return fl.entry, fl.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// startRead starts a read of the pane's transcript unless one is running, and
// returns the one that runs. No request owns it: a page that goes away does not
// cancel a read others wait for. The caller holds c.mu.
func (c *Controller) startRead(st *transcriptState, it Item) *transcriptFlight {
	if st.flight != nil {
		return st.flight
	}
	fl := &transcriptFlight{done: make(chan struct{})}
	st.flight = fl
	prev, m, run := st.entry, c.machineOf(it), c.col.run
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), transcriptTimeout)
		defer cancel()
		e, err := c.readTranscript(ctx, run, m, it, prev)
		c.mu.Lock()
		st.flight = nil
		// A pane that left the sweep meanwhile is not cached again.
		if err == nil && c.transcripts[it.ID] == st {
			st.entry = e
		}
		c.mu.Unlock()
		fl.entry, fl.err = e, err
		close(fl.done)
	}()
	return fl
}

// machineOf is the pane's machine with its SSH target from the sweep. The
// caller holds c.mu.
func (c *Controller) machineOf(it Item) machine {
	m := machine{Label: it.Machine, Box: it.Box}
	if c.snap != nil {
		for _, ms := range c.snap.Machines {
			if ms.Label == it.Machine {
				m.Target = ms.Target
			}
		}
	}
	return m
}

// localReaders read the agents other than Claude, on this Mac only so far:
// the kind the page parses and the reader.
var localReaders = map[string]struct {
	kind string
	read func(context.Context, runner, machine, Item, *transcriptEntry) (transcriptRead, error)
}{
	"codex":    {refCodexRollout, readLocalCodex},
	"pi":       {refPiJSONL, readLocalPi},
	"opencode": {refOpencodeSQLite, readLocalOpencode},
	"grok":     {refGrokJSONL, readLocalGrok},
	"cursor":   {refCursorJSONL, readLocalCursor},
}

// readTranscript reads the pane's transcript (Claude, Codex, pi, opencode,
// grok or cursor-agent), continuing prev when it is the same file.
func (c *Controller) readTranscript(ctx context.Context, run runner, m machine, it Item, prev *transcriptEntry) (*transcriptEntry, error) {
	switch {
	case it.Agent == "":
		return nil, &noTranscript{"No agent in this pane"}
	case it.Agent != "claude" && localReaders[it.Agent].read == nil:
		return nil, &noTranscript{fmt.Sprintf("No %s reader yet", it.Agent)}
	case it.Agent != "claude" && m.Label != localLabel:
		return nil, &noTranscript{fmt.Sprintf("No %[1]s reader for %[2]s yet, only for %[1]s panes on this Mac", it.Agent, m.Label)}
	case !paneIDRE.MatchString(it.PaneID):
		return nil, fmt.Errorf("unsafe pane id %q", it.PaneID)
	case m.Label != localLabel && m.Target == "":
		// Without a target herdrArgv would ask this Mac's herdr about the pane.
		return nil, fmt.Errorf("no ssh target for %s in the latest sweep", m.Label)
	}
	read := readRemote
	switch {
	case it.Agent != "claude":
		read = localReaders[it.Agent].read
	case m.Label == localLabel:
		read = readLocal
	}
	r, err := read(ctx, run, m, it, prev)
	if err != nil {
		return nil, err
	}
	return c.advance(prev, r), nil
}

// advance makes the entry after read r: prev with r's new lines appended when r
// continues it (readers start past byte 0 only at prev's offset in prev's file),
// else a new text, under a new epoch so no version from before matches it.
func (c *Controller) advance(prev *transcriptEntry, r transcriptRead) *transcriptEntry {
	drop := isRequestDebug
	switch r.kind {
	case refCodexRollout:
		drop = isRolloutNoise
	case refPiJSONL:
		drop = isPiNoise
	}
	text, used, dropped, clipped := filterJSONL(r.data, drop)
	e := &transcriptEntry{
		source: r.source, pid: r.pid, dir: r.dir, path: r.path, file: r.file, session: r.session,
		offset: r.from + used, text: text, dropped: dropped, clipped: clipped, fetchedAt: c.col.now(),
	}
	if r.from == 0 || prev == nil {
		e.epoch = c.boot + "." + strconv.FormatInt(c.epochs.Add(1), 10)
		return e
	}
	e.epoch, e.dropped, e.clipped = prev.epoch, prev.dropped+dropped, prev.clipped+clipped
	e.text = prev.text
	if len(text) > 0 {
		e.text = slices.Concat(prev.text, text)
	}
	return e
}

// keepTranscripts follows a new sweep. Transcripts of panes that left it are
// dropped. A box pane read before whose completion_seq moved is read again in
// the background, so the page has the new turn when it asks; herdr-updated
// follows. The caller holds c.mu.
func (c *Controller) keepTranscripts(old, now *Snapshot) {
	before := map[string]*int64{}
	if old != nil {
		for _, it := range old.Items {
			before[it.ID] = it.CompletionSeq
		}
	}
	live := map[string]bool{}
	for _, it := range now.Items {
		live[it.ID] = true
		st := c.transcripts[it.ID]
		was, seen := before[it.ID]
		if st == nil || st.entry == nil || it.Machine == localLabel || !seen || seqText(was) == seqText(it.CompletionSeq) {
			continue
		}
		go c.notifyAfter(c.startRead(st, it))
	}
	for id := range c.transcripts {
		if !live[id] {
			delete(c.transcripts, id)
		}
	}
}

// notifyAfter sends herdr-updated once a background read has finished, the way
// SetViewed does.
func (c *Controller) notifyAfter(fl *transcriptFlight) {
	<-fl.done
	c.mu.Lock()
	sess := c.sess
	c.mu.Unlock()
	if fl.err == nil && sess != nil {
		sess.Notify(session.SSEEvent{Type: UpdatedEvent})
	}
}
