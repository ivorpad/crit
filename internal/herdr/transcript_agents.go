package herdr

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// pi and opencode panes are joined through herdr's own agent_session, which
// herdr's integrations report for both (fallbackRef): pi's session file and
// opencode's session id. The page parses what is read (herdr-transcript-pi.js,
// herdr-transcript-opencode.js).

var (
	opencodeIDRE = regexp.MustCompile(`^ses_[A-Za-z0-9]{8,64}$`)
	// piSessionRE is the session id at the end of a pi session file's name.
	piSessionRE = regexp.MustCompile(`_([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})\.jsonl$`)
)

// isPiSession reports a pi session file on this Mac: a .jsonl under
// ~/.pi/agent/sessions, in the clean absolute form herdr reports it.
func isPiSession(p string) bool {
	home, err := os.UserHomeDir()
	return err == nil && filepath.IsAbs(p) && filepath.Clean(p) == p && strings.HasSuffix(p, ".jsonl") &&
		strings.HasPrefix(p, filepath.Join(home, ".pi", "agent", "sessions")+string(filepath.Separator))
}

// readLocalPi reads the session file herdr named for a pi pane on this Mac,
// from where the last read stopped.
func readLocalPi(_ context.Context, _ runner, _ machine, it Item, prev *transcriptEntry) (transcriptRead, error) {
	ref := it.TranscriptRef
	if ref == nil || ref.Kind != refPiJSONL || !isPiSession(ref.Path) {
		return transcriptRead{}, &noTranscript{"herdr reports no session file for this pi pane"}
	}
	session := strings.TrimSuffix(filepath.Base(ref.Path), ".jsonl")
	if m := piSessionRE.FindStringSubmatch(ref.Path); m != nil {
		session = m[1]
	}
	r := transcriptRead{source: "local", kind: refPiJSONL, path: ref.Path, file: tildePath(ref.Path), session: session}
	offset := int64(0)
	if prev != nil && prev.session == r.session && prev.path == r.path {
		offset = prev.offset
	}
	var err error
	r.data, r.from, err = readLocalFrom(ref.Path, offset)
	if errors.Is(err, fs.ErrNotExist) {
		return transcriptRead{}, &noTranscript{"pi has not written " + r.file + " yet"}
	}
	return r, err
}

// isPiNoise is a pi "custom" line: extension state the page never draws. A
// sub-agent registry snapshot is one, rewritten whole after every change (84
// of 88 MB in one session).
func isPiNoise(line []byte) bool { return bytes.HasPrefix(line, []byte(`{"type":"custom",`)) }

// opencodeDB is opencode's database: $XDG_DATA_HOME/opencode/opencode.db, else
// ~/.local/share/opencode/opencode.db.
func opencodeDB() string {
	if d := os.Getenv("XDG_DATA_HOME"); filepath.IsAbs(d) {
		return filepath.Join(d, "opencode", "opencode.db")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "opencode", "opencode.db")
}

// opencodeQuery prints one JSON object per line: the session ({"k":"s"}: title,
// directory, version), then every message ({"k":"m"}) and part ({"k":"p"}) of
// it in the order they were created, with each row's data as opencode wrote
// it. New rows come last, so the lines before them keep their numbers. Only an
// id that passed opencodeIDRE goes in.
func opencodeQuery(session string) string {
	return fmt.Sprintf(`select j from (`+
		`select -1 as t, 0 as k, id, json_object('k','s','id',id,'title',title,'dir',directory,'version',version) as j from session where id='%[1]s'`+
		` union all select time_created, 1, id, json_object('k','m','id',id,'t',time_created,'data',json(data)) from message where session_id='%[1]s'`+
		` union all select time_created, 2, id, json_object('k','p','id',id,'mid',message_id,'t',time_created,'data',json(data)) from part where session_id='%[1]s'`+
		`) order by t, k, id;`, session)
}

// readLocalOpencode reads an opencode pane's session out of opencode's database
// with the sqlite3 CLI, read-only. A part changes in place while a turn streams,
// so every read is the whole session, from byte 0.
func readLocalOpencode(ctx context.Context, run runner, _ machine, it Item, _ *transcriptEntry) (transcriptRead, error) {
	ref := it.TranscriptRef
	if ref == nil || ref.Kind != refOpencodeSQLite || !opencodeIDRE.MatchString(ref.Session) {
		return transcriptRead{}, &noTranscript{"herdr reports no session for this opencode pane"}
	}
	db := opencodeDB()
	if !isFile(db) {
		return transcriptRead{}, &noTranscript{"No opencode database at " + tildePath(db)}
	}
	out, err := run(ctx, nil, []string{"sqlite3", "-readonly", "-bail", "-batch", db, opencodeQuery(ref.Session)})
	if err != nil {
		return transcriptRead{}, fmt.Errorf("reading opencode's database: %w", err)
	}
	if int64(len(out)) > maxReadBytes {
		return transcriptRead{}, fmt.Errorf("stopped reading the transcript at %d MB, far past any real one", maxReadBytes>>20)
	}
	if len(bytes.TrimSpace(out)) == 0 {
		return transcriptRead{}, &noTranscript{"No messages yet in opencode session " + ref.Session}
	}
	return transcriptRead{source: "local", kind: refOpencodeSQLite, path: db, file: tildePath(db) + " · " + ref.Session, session: ref.Session, data: out}, nil
}

// grokHome is grok's home: $GROK_HOME, else ~/.grok.
func grokHome() string {
	if d := os.Getenv("GROK_HOME"); filepath.IsAbs(d) {
		return filepath.Clean(d)
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".grok")
}

// readNewestMatch reads, whole, the newest file matching a glob built from a
// checked session id. Grok and cursor-agent may rewrite their files (a grok
// rewind does), and the biggest seen are 1.2 MB and 0.8 MB, so no read
// continues an earlier one.
func readNewestMatch(kind, agent, session, pattern string) (transcriptRead, error) {
	matches, _ := filepath.Glob(pattern)
	var p string
	var newest time.Time
	for _, m := range matches {
		if fi, err := os.Stat(m); err == nil && fi.Mode().IsRegular() && (p == "" || fi.ModTime().After(newest)) {
			p, newest = m, fi.ModTime()
		}
	}
	if p == "" {
		return transcriptRead{}, &noTranscript{fmt.Sprintf("No %s transcript yet for session %s", agent, session)}
	}
	data, from, err := readLocalFrom(p, 0)
	return transcriptRead{source: "local", kind: kind, path: p, file: tildePath(p), session: session, from: from, data: data}, err
}

// readLocalGrok reads a grok pane's chat history,
// <grok home>/sessions/<cwd, URL-encoded>/<session>/chat_history.jsonl, by the
// session id herdr reports.
func readLocalGrok(_ context.Context, _ runner, _ machine, it Item, _ *transcriptEntry) (transcriptRead, error) {
	ref := it.TranscriptRef
	if ref == nil || ref.Kind != refGrokJSONL || !uuidRE.MatchString(ref.Session) {
		return transcriptRead{}, &noTranscript{"herdr reports no session for this grok pane"}
	}
	return readNewestMatch(refGrokJSONL, "grok", ref.Session, filepath.Join(grokHome(), "sessions", "*", ref.Session, "chat_history.jsonl"))
}

// readLocalCursor reads a cursor-agent pane's transcript,
// ~/.cursor/projects/<cwd slug>/agent-transcripts/<chat>/<chat>.jsonl, by the
// chat id herdr reports. cursor-agent keeps no tool output in it.
func readLocalCursor(_ context.Context, _ runner, _ machine, it Item, _ *transcriptEntry) (transcriptRead, error) {
	ref := it.TranscriptRef
	if ref == nil || ref.Kind != refCursorJSONL || !uuidRE.MatchString(ref.Session) {
		return transcriptRead{}, &noTranscript{"herdr reports no session for this cursor pane"}
	}
	home, _ := os.UserHomeDir()
	return readNewestMatch(refCursorJSONL, "cursor", ref.Session, filepath.Join(home, ".cursor", "projects", "*", "agent-transcripts", ref.Session, ref.Session+".jsonl"))
}
