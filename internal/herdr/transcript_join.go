package herdr

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// A pane is joined to its Claude Code transcript the same way on every machine,
// with nothing installed: herdr lists the pane's foreground processes, Claude
// Code keeps <config dir>/sessions/<pid>.json with the process's sessionId and
// cwd, and the transcript is <config dir>/projects/<slug of cwd>/<sessionId>.jsonl.
// On this Mac that is file reads; on other machines, SSH calls (transcript_ssh.go).

// configDirRE is a Claude Code config directory name under ~.
var configDirRE = regexp.MustCompile(`^\.claude[A-Za-z0-9._-]*$`)

// maxReadBytes caps one read of a transcript (after gunzip for a remote one), so
// a planted or broken file cannot fill the daemon's memory. The biggest real
// transcript is about 60 MB. Tests lower it.
var maxReadBytes int64 = 256 << 20

// readCapped reads r to its end, and stops with an error past maxReadBytes.
func readCapped(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxReadBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading the transcript: %w", err)
	}
	if int64(len(data)) > maxReadBytes {
		return nil, fmt.Errorf("stopped reading the transcript at %d MB, far past any real one", maxReadBytes>>20)
	}
	return data, nil
}

// noTranscript is a pane with no Claude transcript to read. The reason is shown
// on the page as written.
type noTranscript struct{ reason string }

func (e *noTranscript) Error() string { return e.reason }

// foregroundProcess is one process of `herdr pane process-info`. The boxes'
// herdr sends no argv0. The pid stays raw so that only a plain integer is used.
type foregroundProcess struct {
	PID   json.RawMessage `json:"pid"`
	Name  string          `json:"name"`
	Argv  []string        `json:"argv"`
	Argv0 string          `json:"argv0"`
}

// panePIDs lists the pids of the pane's foreground processes that pass is
// (isClaude, isCodex), through `herdr [--machine X] pane process-info --pane <id>`.
func panePIDs(ctx context.Context, run runner, m machine, paneID string, is func(foregroundProcess) bool) ([]int, error) {
	argv, err := herdrArgv(m, "pane", "process-info", "--pane", paneID)
	if err != nil {
		return nil, err
	}
	out, err := run(ctx, nil, argv)
	if err != nil {
		// herdr prints its error JSON on stderr, which the runner puts in err.
		if strings.Contains(err.Error(), "pane_not_found") {
			return nil, &noTranscript{fmt.Sprintf("herdr no longer knows pane %s on %s", paneID, m.Label)}
		}
		return nil, err
	}
	return parsePanePIDs(out, is)
}

// parsePanePIDs picks the processes is matches out of process-info. An
// interactive one comes before a `claude -p` that a tool started in the same
// process group.
func parsePanePIDs(out []byte, is func(foregroundProcess) bool) ([]int, error) {
	var body struct {
		Result struct {
			ProcessInfo struct {
				Foreground []foregroundProcess `json:"foreground_processes"`
			} `json:"process_info"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &body); err != nil {
		return nil, fmt.Errorf("parsing herdr pane process-info: %w", err)
	}
	var interactive, printing []int
	for _, p := range body.Result.ProcessInfo.Foreground {
		pid, err := strconv.Atoi(string(p.PID))
		if err != nil || pid <= 0 || !is(p) {
			continue
		}
		if slices.Contains(p.Argv, "-p") || slices.Contains(p.Argv, "--print") {
			printing = append(printing, pid)
		} else {
			interactive = append(interactive, pid)
		}
	}
	return append(interactive, printing...), nil
}

// isClaude reports a Claude Code process. herdr on this Mac names it by its
// version (2.1.300) and sends argv0; the boxes' herdr names it claude.
func isClaude(p foregroundProcess) bool {
	return p.Name == "claude" || p.Argv0 == "claude" || (len(p.Argv) > 0 && path.Base(p.Argv[0]) == "claude")
}

// claudeConfigDirs are this Mac's Claude Code config directories: every ~/.claude*
// directory (~/.claude.json is a file and is skipped) and $CLAUDE_CONFIG_DIR.
func claudeConfigDirs() []string {
	var dirs []string
	add := func(d string) {
		if fi, err := os.Stat(d); err == nil && fi.IsDir() && !slices.Contains(dirs, d) {
			dirs = append(dirs, d)
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		entries, _ := os.ReadDir(home)
		for _, e := range entries {
			if configDirRE.MatchString(e.Name()) {
				add(filepath.Join(home, e.Name()))
			}
		}
	}
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); filepath.IsAbs(d) {
		add(filepath.Clean(d))
	}
	return dirs
}

// sessionFile is the part of Claude Code's sessions/<pid>.json the join reads.
type sessionFile struct {
	SessionID string `json:"sessionId"`
	CWD       string `json:"cwd"`
}

func parseSessionFile(data []byte) (sessionFile, bool) {
	var sf sessionFile
	if json.Unmarshal(data, &sf) != nil || !uuidRE.MatchString(sf.SessionID) {
		return sessionFile{}, false
	}
	return sf, true
}

// joined is the session the join found for a pane, or why there is none.
type joined struct {
	pid     int    // 0 when the session came from herdr's agent_session or a resume line
	dir     string // config dir: a path on this Mac, a name under ~ elsewhere; "" when not known
	session string
	cwd     string
	reason  string // set when session is "": why, in plain words
}

// fallbackJoin is the session the sweep named for a pane whose pid join found
// nothing: herdr's agent_session, or an exited pane's resume line.
func fallbackJoin(m machine, it Item, pids []int) joined {
	j := joined{cwd: it.CWD, reason: "No claude process in this pane"}
	if len(pids) > 0 {
		j.reason = fmt.Sprintf("No session file for pid %d on %s", pids[0], m.Label)
	}
	if r := it.TranscriptRef; r != nil && r.Kind == refClaudeJSONL && uuidRE.MatchString(r.Session) {
		j.session = r.Session
	}
	return j
}

// missingTranscript is a session whose transcript file is not there: gone when
// it was read before, else not written yet (a claude with no prompt) or never
// found.
func missingTranscript(m machine, j joined, prev *transcriptEntry) error {
	if prev != nil && prev.session == j.session {
		return fmt.Errorf("the transcript %s is gone", prev.file)
	}
	if j.reason != "" {
		return &noTranscript{j.reason}
	}
	return &noTranscript{fmt.Sprintf("No transcript yet for session %s on %s", j.session, m.Label)}
}

// transcriptRead is what one read found: the transcript and its bytes from byte
// from. from is prev's offset when the read continues prev's file, else 0.
type transcriptRead struct {
	source  string // "local" or "ssh"
	kind    string // refClaudeJSONL, or refCodexRollout; "" is Claude
	pid     int
	dir     string // ssh: the config dir name under ~
	path    string // absolute on this Mac, relative to ~ elsewhere
	file    string // as shown on the page
	session string
	from    int64
	data    []byte
}

// locateLocal reads sessions/<pid>.json for each pid in turn in every config
// dir. The newest file wins when two dirs have one: a killed claude leaves its
// file behind, and a later process can get the same pid.
func locateLocal(dirs []string, pids []int) (joined, bool) {
	for _, pid := range pids {
		var best joined
		var bestTime time.Time
		for _, d := range dirs {
			p := filepath.Join(d, "sessions", strconv.Itoa(pid)+".json")
			fi, err := os.Stat(p)
			if err != nil || (best.session != "" && !fi.ModTime().After(bestTime)) {
				continue
			}
			data, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			if sf, ok := parseSessionFile(data); ok {
				best, bestTime = joined{pid: pid, dir: d, session: sf.SessionID, cwd: sf.CWD}, fi.ModTime()
			}
		}
		if best.session != "" {
			return best, true
		}
	}
	return joined{}, false
}

// findLocalTranscript is the session's transcript in the first of dirs that has
// it: under the cwd's project, else under any project (a session resumed from
// another directory). "" when there is none.
func findLocalTranscript(dirs []string, cwd, session string) string {
	name := session + ".jsonl"
	if cwd != "" {
		for _, d := range dirs {
			if p := filepath.Join(d, "projects", claudeSlug(cwd), name); isFile(p) {
				return p
			}
		}
	}
	for _, d := range dirs {
		entries, _ := os.ReadDir(filepath.Join(d, "projects"))
		for _, e := range entries {
			if p := filepath.Join(d, "projects", e.Name(), name); isFile(p) {
				return p
			}
		}
	}
	return ""
}

func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}

// tildePath shows a path under the home directory as ~/...
func tildePath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home+string(filepath.Separator)) {
		return "~" + p[len(home):]
	}
	return p
}

// readLocal reads the transcript of a pane on this Mac. The join runs on every
// read: one herdr call and a few small file reads.
func readLocal(ctx context.Context, run runner, m machine, it Item, prev *transcriptEntry) (transcriptRead, error) {
	pids, err := panePIDs(ctx, run, m, it.PaneID, isClaude)
	if err != nil {
		return transcriptRead{}, err
	}
	dirs := claudeConfigDirs()
	j, ok := locateLocal(dirs, pids)
	if !ok {
		j = fallbackJoin(m, it, pids)
	}
	if j.session == "" {
		return transcriptRead{}, &noTranscript{j.reason}
	}
	if j.dir != "" {
		dirs = []string{j.dir}
	}
	p := findLocalTranscript(dirs, j.cwd, j.session)
	if p == "" {
		return transcriptRead{}, missingTranscript(m, j, prev)
	}
	r := transcriptRead{source: "local", pid: j.pid, path: p, file: tildePath(p), session: j.session}
	offset := int64(0)
	if prev != nil && prev.session == r.session && prev.path == r.path {
		offset = prev.offset
	}
	r.data, r.from, err = readLocalFrom(p, offset)
	return r, err
}

// readLocalFrom reads a file from offset to its end, or from 0 when the file is
// now shorter than offset.
func readLocalFrom(p string, offset int64) (data []byte, from int64, err error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, 0, err
	}
	if fi.Size() < offset {
		offset = 0
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, 0, err
	}
	data, err = readCapped(f)
	return data, offset, err
}
