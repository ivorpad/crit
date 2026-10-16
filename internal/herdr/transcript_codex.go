package herdr

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// A Codex pane is joined to its rollout through the files its process holds
// open. Codex keeps <codex home>/sessions/YYYY/MM/DD/rollout-<time>-<thread>.jsonl
// open while the session runs, and one more for every sub-agent it spawned
// (wSP:p2 held four). The pane's own thread is the rollout whose first line,
// session_meta, names no parent thread. A TUI that runs on Codex's shared
// daemon holds none (the daemon holds them all), and is joined through
// Codex's thread index instead (rolloutByTitle). Two Codex panes can share a
// cwd, so the cwd alone never picks a rollout. The page parses the rollout
// itself (herdr-transcript-codex.js); this file only finds it and reads its
// bytes.

// rolloutRE is a rollout file; the group is its thread id.
var rolloutRE = regexp.MustCompile(`/sessions/[0-9]{4}/[0-9]{2}/[0-9]{2}/rollout-[0-9T-]+-([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})\.jsonl$`)

// isCodex reports Codex's own binary, not the node launcher in front of it
// (name and argv0 "node"), which holds no rollout.
func isCodex(p foregroundProcess) bool {
	return p.Name == "codex" || p.Argv0 == "codex" || (len(p.Argv) > 0 && path.Base(p.Argv[0]) == "codex")
}

// openRollouts lists the rollout files the pids hold open, through lsof.
func openRollouts(ctx context.Context, run runner, pids []int) ([]string, error) {
	list := make([]string, len(pids))
	for i, pid := range pids {
		list[i] = strconv.Itoa(pid)
	}
	out, err := run(ctx, nil, []string{"lsof", "-nP", "-Fn", "-p", strings.Join(list, ",")})
	// lsof exits 1 when one of the pids is gone and still lists the others.
	if err != nil && len(out) == 0 {
		return nil, fmt.Errorf("listing the files codex holds open: %w", err)
	}
	var paths []string
	for _, line := range strings.Split(string(out), "\n") {
		p, ok := strings.CutPrefix(line, "n")
		if ok && rolloutRE.MatchString(p) && !slices.Contains(paths, p) {
			paths = append(paths, p)
		}
	}
	return paths, nil
}

// rolloutMeta is the part of a rollout's session_meta the join reads.
type rolloutMeta struct {
	ID           string `json:"id"`
	ParentThread string `json:"parent_thread_id"`
	ThreadSource string `json:"thread_source"`
}

// readRolloutMeta reads the session_meta on the rollout's first line. It
// carries Codex's whole system prompt (23 KB on 0.162), so the line is read
// up to 256 KB.
func readRolloutMeta(p string) (rolloutMeta, bool) {
	f, err := os.Open(p)
	if err != nil {
		return rolloutMeta{}, false
	}
	defer f.Close()
	line, err := bufio.NewReaderSize(f, 256<<10).ReadSlice('\n')
	if err != nil {
		return rolloutMeta{}, false
	}
	var l struct {
		Type    string      `json:"type"`
		Payload rolloutMeta `json:"payload"`
	}
	if json.Unmarshal(line, &l) != nil || l.Type != "session_meta" {
		return rolloutMeta{}, false
	}
	return l.Payload, true
}

// rootRollout picks the pane's own thread out of the open rollouts: no parent
// thread, not a sub-agent, and its thread id in the file name. When two qualify
// the one written last wins.
func rootRollout(paths []string) (file, thread string, ok bool) {
	var newest time.Time
	for _, p := range paths {
		meta, found := readRolloutMeta(p)
		id := rolloutRE.FindStringSubmatch(p)[1]
		if !found || meta.ID != id || meta.ParentThread != "" || meta.ThreadSource == "subagent" {
			continue
		}
		fi, err := os.Stat(p)
		if err != nil || (file != "" && !fi.ModTime().After(newest)) {
			continue
		}
		file, thread, newest = p, id, fi.ModTime()
	}
	return file, thread, file != ""
}

// readLocalCodex reads the rollout of a Codex pane on this Mac: one herdr call,
// one lsof and the file from where the last read stopped.
func readLocalCodex(ctx context.Context, run runner, m machine, it Item, prev *transcriptEntry) (transcriptRead, error) {
	pids, err := panePIDs(ctx, run, m, it.PaneID, isCodex)
	if err != nil {
		return transcriptRead{}, err
	}
	if len(pids) == 0 {
		return transcriptRead{}, &noTranscript{"No codex process in this pane"}
	}
	paths, err := openRollouts(ctx, run, pids)
	if err != nil {
		return transcriptRead{}, err
	}
	p, thread, ok := rootRollout(paths)
	if !ok {
		if p, thread, err = rolloutByTitle(ctx, run, m, it); err != nil {
			return transcriptRead{}, err
		}
	}
	r := transcriptRead{source: "local", kind: refCodexRollout, pid: pids[0], path: p, file: tildePath(p), session: thread}
	offset := int64(0)
	if prev != nil && prev.session == r.session && prev.path == r.path {
		offset = prev.offset
	}
	r.data, r.from, err = readLocalFrom(p, offset)
	return r, err
}

// codexHome is Codex's home: $CODEX_HOME, else ~/.codex.
func codexHome() string {
	if d := os.Getenv("CODEX_HOME"); filepath.IsAbs(d) {
		return filepath.Clean(d)
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".codex")
}

// stateDBRE is Codex's thread index, state_<schema>.sqlite (0.162 and 0.163
// write state_5). Copies such as state_5.before-retag-20260723.sqlite do not
// match.
var stateDBRE = regexp.MustCompile(`^state_([0-9]+)\.sqlite$`)

// codexStateDB is the thread index with the highest schema number, or "".
func codexStateDB() string {
	dir := codexHome()
	entries, _ := os.ReadDir(dir)
	best, bestN := "", -1
	for _, e := range entries {
		if m := stateDBRE.FindStringSubmatch(e.Name()); m != nil {
			if n, _ := strconv.Atoi(m[1]); n > bestN {
				best, bestN = filepath.Join(dir, e.Name()), n
			}
		}
	}
	return best
}

// rolloutByTitle finds the rollout of a Codex TUI that holds none open. From
// 0.159 a TUI can run on Codex's shared daemon (`codex app-server
// --managed-daemon`), which holds every thread's rollout itself, so lsof finds
// nothing on the pane's process. The TUI's terminal title still names its
// task, "<task name> | <project>" (with a status part such as "[ ! ] Action
// Required" while it waits), and Codex's thread index keeps that name per
// thread (threads.name). The pane's thread is the root thread in the pane's
// cwd whose name is one of the title's parts, the one updated last. A title
// that names no task yet finds nothing.
func rolloutByTitle(ctx context.Context, run runner, m machine, it Item) (file, thread string, err error) {
	title, cwd := it.Title, it.CWD
	if fresh, err := agentGet(ctx, run, m, it.PaneID); err == nil {
		title, cwd = deref(fresh.Title), fresh.cwd()
	}
	db := codexStateDB()
	switch {
	case db == "":
		return "", "", &noTranscript{"Codex holds no session file open, and " + tildePath(codexHome()) + " has no thread index (state_*.sqlite)"}
	case !filepath.IsAbs(cwd) || strings.ContainsAny(cwd, "\x00\n"):
		return "", "", &noTranscript{"Codex holds no session file open, and herdr names no directory for this pane"}
	}
	q := fmt.Sprintf(`select json_object('id',id,'rollout',rollout_path,'name',name) from threads`+
		` where cwd='%s' and archived=0 and coalesce(thread_source,'')<>'subagent' and name<>''`+
		` order by updated_at desc limit 50;`, strings.ReplaceAll(cwd, "'", "''"))
	out, err := run(ctx, nil, []string{"sqlite3", "-readonly", "-bail", "-batch", db, q})
	if err != nil {
		return "", "", fmt.Errorf("reading Codex's thread index: %w", err)
	}
	parts := strings.Split(title, "|")
	for _, line := range strings.Split(string(out), "\n") {
		var row struct{ ID, Rollout, Name string }
		if json.Unmarshal([]byte(line), &row) != nil || !namedIn(row.Name, parts) {
			continue
		}
		mm := rolloutRE.FindStringSubmatch(row.Rollout)
		if mm == nil || mm[1] != row.ID {
			continue
		}
		if meta, ok := readRolloutMeta(row.Rollout); ok && meta.ID == row.ID && meta.ParentThread == "" && meta.ThreadSource != "subagent" {
			return row.Rollout, row.ID, nil
		}
	}
	return "", "", &noTranscript{fmt.Sprintf("Codex holds no session file open (it runs on Codex's shared daemon), and no thread in %s is named after its title %q yet", tildePath(cwd), title)}
}

// namedIn reports whether a thread's name is one of the title's parts, whole,
// or cut short with "…".
func namedIn(name string, parts []string) bool {
	for _, p := range parts {
		p = strings.TrimSpace(p)
		cut, short := strings.CutSuffix(p, "…")
		if p == name || (short && len(cut) >= 8 && strings.HasPrefix(name, cut)) {
			return true
		}
	}
	return false
}

// rolloutNoise are rollout lines the page never draws: the model's own copy of
// the conversation (response_item), token counts, and context Codex re-sends
// every turn. They are most of a rollout's bytes.
var rolloutNoise = map[string]bool{
	"response_item": true, "token_usage_record": true, "turn_context": true, "world_state": true,
	"inter_agent_communication_metadata": true, "realtime_item": true, "compacted": true,
	"event_msg/token_count": true,
}

func isRolloutNoise(line []byte) bool {
	var l struct {
		Type    string `json:"type"`
		Payload struct {
			Type string `json:"type"`
		} `json:"payload"`
	}
	if json.Unmarshal(line, &l) != nil {
		return false
	}
	return rolloutNoise[l.Type] || rolloutNoise[l.Type+"/"+l.Payload.Type]
}
