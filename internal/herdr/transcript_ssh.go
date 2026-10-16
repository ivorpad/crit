package herdr

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// The remote half of the transcript join. Every command is a fixed script with
// only checked values in it: pids (integers), a config dir name (configDirRE),
// a project slug (projectDirRE), a session id (uuidRE) and byte offsets.

var (
	projectDirRE = regexp.MustCompile(`^[A-Za-z0-9-]+$`)
	// remoteFileRE is a transcript path relative to ~: the only shape a remote
	// path takes in a command, or is accepted back from one.
	remoteFileRE = regexp.MustCompile(`^(\.claude[A-Za-z0-9._-]*)/projects/[A-Za-z0-9-]+/([0-9a-f-]{36})\.jsonl$`)
)

// sshArgv runs a script on the machine's SSH target through sh, whatever the
// login shell is. No script contains a single quote.
func sshArgv(m machine, script string) ([]string, error) {
	if m.Target == "" || strings.HasPrefix(m.Target, "-") {
		return nil, fmt.Errorf("no usable ssh target for %s: %q", m.Label, m.Target)
	}
	if strings.ContainsRune(script, '\'') {
		return nil, errors.New("refusing a remote script with a single quote")
	}
	return []string{"ssh", "-T", "-o", "BatchMode=yes", "-o", "ConnectTimeout=8", m.Target, "sh -c '" + script + "'"}, nil
}

// locateScript prints, for each pid in turn, every ~/.claude*/sessions/<pid>.json,
// newest first: a "== <pid> <config dir>" line, then the file.
func locateScript(pids []int) string {
	var b strings.Builder
	b.WriteString("cd ~ || exit 1; for p in")
	for _, pid := range pids {
		b.WriteString(" " + strconv.Itoa(pid))
	}
	b.WriteString(`; do for f in $(ls -t .claude*/sessions/$p.json 2>/dev/null); do echo "== $p ${f%%/*}"; cat "$f"; echo; done; done`)
	return b.String()
}

// parseLocate picks, in pids order, the first pid with a sessions file that
// checks out from locateScript's output.
func parseLocate(out []byte, pids []int) (joined, bool) {
	found := map[int]joined{}
	for _, block := range strings.Split("\n"+string(out), "\n== ")[1:] {
		head, body, _ := strings.Cut(block, "\n")
		pidText, dir, _ := strings.Cut(head, " ")
		pid, err := strconv.Atoi(pidText)
		sf, ok := parseSessionFile([]byte(body))
		if _, seen := found[pid]; err != nil || seen || !ok || !configDirRE.MatchString(dir) {
			continue
		}
		found[pid] = joined{pid: pid, dir: dir, session: sf.SessionID, cwd: sf.CWD}
	}
	for _, pid := range pids {
		if j, ok := found[pid]; ok {
			return j, true
		}
	}
	return joined{}, false
}

// fetchSpec is the transcript a remote read wants and where to start.
type fetchSpec struct {
	path    string // the transcript under ~; "" when not known
	dir     string // config dir name to look in by session id; "" for every ~/.claude*
	session string
	offset  int64
	pid     int // > 0: print <dir>/sessions/<pid>.json first, so a warm read sees a /clear
}

// fetchScript finds the transcript (path, else the newest
// <dir>/projects/*/<session>.jsonl) and prints "== file <path> <size>" and
// "== from <n>", then its bytes from byte n gzipped: n is the offset, or 0 when
// the file is now shorter. It prints "== missing" when there is no such file.
func fetchScript(spec fetchSpec) (string, error) {
	if (spec.path != "" && !remoteFileRE.MatchString(spec.path)) || (spec.dir != "" && !configDirRE.MatchString(spec.dir)) ||
		!uuidRE.MatchString(spec.session) || spec.offset < 0 || spec.pid < 0 || (spec.pid > 0 && spec.dir == "") {
		return "", fmt.Errorf("unsafe transcript read: %+v", spec)
	}
	dirs := spec.dir
	if dirs == "" {
		dirs = ".claude*"
	}
	var b strings.Builder
	b.WriteString("cd ~ || exit 1; ")
	if spec.pid > 0 {
		fmt.Fprintf(&b, `echo "== session"; cat %s/sessions/%d.json 2>/dev/null; echo; `, spec.dir, spec.pid)
	}
	fmt.Fprintf(&b, `f=%s; [ -f "$f" ] || f=$(ls -t %s/projects/*/%s.jsonl 2>/dev/null | head -n 1); `, spec.path, dirs, spec.session)
	b.WriteString(`[ -n "$f" ] || { echo "== missing"; exit 0; }; s=$(($(wc -c < "$f"))); echo "== file $f $s"; `)
	if spec.offset == 0 {
		b.WriteString(`echo "== from 0"; gzip -c < "$f"`)
	} else {
		// tail -c +N starts at byte N counting from 1.
		fmt.Fprintf(&b, `if [ "$s" -lt %d ]; then echo "== from 0"; gzip -c < "$f"; else echo "== from %d"; tail -c +%d "$f" | gzip -c; fi`,
			spec.offset, spec.offset, spec.offset+1)
	}
	return b.String(), nil
}

// fetched is fetchScript's answer.
type fetched struct {
	session *sessionFile // the pid's sessions file, when asked for and readable
	missing bool
	path    string
	from    int64
	data    []byte
}

// cutMarker finds the first line that starts with marker and returns the rest of
// that line and everything after it. Lines before it (a login banner) are skipped.
func cutMarker(out []byte, marker string) (line string, rest []byte, ok bool) {
	for len(out) > 0 {
		end := bytes.IndexByte(out, '\n')
		if end < 0 {
			return "", nil, false
		}
		if bytes.HasPrefix(out, []byte(marker)) {
			return string(out[len(marker):end]), out[end+1:], true
		}
		out = out[end+1:]
	}
	return "", nil, false
}

func parseFetch(out []byte, spec fetchSpec) (fetched, error) {
	var f fetched
	if spec.pid > 0 {
		_, rest, ok := cutMarker(out, "== session")
		end := bytes.Index(rest, []byte("\n== "))
		if !ok || end < 0 {
			return fetched{}, badFetch(string(out))
		}
		if sf, ok := parseSessionFile(rest[:end]); ok {
			f.session = &sf
		}
		out = rest[end+1:]
	}
	head, rest, ok := cutMarker(out, "== ")
	if ok && head == "missing" {
		f.missing = true
		return f, nil
	}
	var err error
	if f.path, f.from, rest, err = fetchHeader(head, rest, spec); err != nil {
		return fetched{}, err
	}
	zr, err := gzip.NewReader(bytes.NewReader(rest))
	if err != nil {
		return fetched{}, fmt.Errorf("reading the transcript: %w", err)
	}
	if f.data, err = readCapped(zr); err != nil {
		return fetched{}, err
	}
	return f, nil
}

// fetchHeader reads "file <path> <size>" (head) and the "== from <n>" line
// after it. The path must be the session's transcript, n the offset asked for
// or 0.
func fetchHeader(head string, out []byte, spec fetchSpec) (path string, from int64, rest []byte, err error) {
	head, isHead := strings.CutPrefix(head, "file ")
	i := strings.LastIndexByte(head, ' ')
	if !isHead || i < 0 {
		return "", 0, nil, badFetch(head)
	}
	if m := remoteFileRE.FindStringSubmatch(head[:i]); m == nil || m[2] != spec.session {
		return "", 0, nil, badFetch(head)
	}
	line, rest, ok := cutMarker(out, "== from ")
	n, err := strconv.ParseInt(line, 10, 64)
	if !ok || err != nil || (n != 0 && n != spec.offset) {
		return "", 0, nil, badFetch(line)
	}
	return head[:i], n, rest, nil
}

func badFetch(what string) error {
	return fmt.Errorf("unexpected answer from the transcript read: %.120q", what)
}

// sshFetch runs fetchScript on the machine.
func sshFetch(ctx context.Context, run runner, m machine, spec fetchSpec) (fetched, error) {
	script, err := fetchScript(spec)
	if err != nil {
		return fetched{}, err
	}
	argv, err := sshArgv(m, script)
	if err != nil {
		return fetched{}, err
	}
	out, err := run(ctx, nil, argv)
	if err != nil {
		return fetched{}, err
	}
	return parseFetch(out, spec)
}

// joinRemote finds the session of a pane on another machine: the first claude
// pid with a sessions file in any ~/.claude*, else the fallback.
func joinRemote(ctx context.Context, run runner, m machine, it Item, pids []int) (joined, error) {
	if len(pids) == 0 {
		return fallbackJoin(m, it, pids), nil
	}
	argv, err := sshArgv(m, locateScript(pids))
	if err != nil {
		return joined{}, err
	}
	out, err := run(ctx, nil, argv)
	if err != nil {
		return joined{}, err
	}
	if j, ok := parseLocate(out, pids); ok {
		return j, nil
	}
	return fallbackJoin(m, it, pids), nil
}

func remoteRead(pid int, session string, f fetched) transcriptRead {
	return transcriptRead{source: "ssh", pid: pid, dir: remoteFileRE.FindStringSubmatch(f.path)[1], path: f.path,
		file: "~/" + f.path, session: session, from: f.from, data: f.data}
}

// readRemote reads the transcript of a pane on another machine. A cold read is
// three calls (process-info, locate, fetch); a warm one is one, see warmRead.
func readRemote(ctx context.Context, run runner, m machine, it Item, prev *transcriptEntry) (transcriptRead, error) {
	pids, j, warm, err := warmRead(ctx, run, m, it, prev)
	if err != nil {
		return transcriptRead{}, err
	}
	if warm != nil {
		return *warm, nil
	}
	if j.session == "" {
		if j, err = joinRemote(ctx, run, m, it, pids); err != nil {
			return transcriptRead{}, err
		}
		if j.session == "" {
			return transcriptRead{}, &noTranscript{j.reason}
		}
	}
	spec := fetchSpec{dir: j.dir, session: j.session}
	if slug := claudeSlug(j.cwd); j.dir != "" && projectDirRE.MatchString(slug) {
		spec.path = j.dir + "/projects/" + slug + "/" + j.session + ".jsonl"
	}
	if prev != nil && prev.session == j.session {
		spec.path, spec.offset = prev.path, prev.offset
	}
	f, err := sshFetch(ctx, run, m, spec)
	if err != nil {
		return transcriptRead{}, err
	}
	if f.missing {
		return transcriptRead{}, missingTranscript(m, j, prev)
	}
	return remoteRead(j.pid, j.session, f), nil
}

// warmRead lists the pane's claude pids and, when prev came from the pid join,
// at the same time checks that pid's sessions file and fetches the bytes after
// prev in one SSH call. warm is that read when the pid and its session are
// unchanged; j is the new session when the same process moved on (a /clear).
func warmRead(ctx context.Context, run runner, m machine, it Item, prev *transcriptEntry) (pids []int, j joined, warm *transcriptRead, err error) {
	if prev == nil || prev.pid == 0 {
		pids, err = panePIDs(ctx, run, m, it.PaneID, isClaude)
		return pids, joined{}, nil, err
	}
	type result struct {
		f   fetched
		err error
	}
	ch := make(chan result, 1)
	go func() {
		f, err := sshFetch(ctx, run, m, fetchSpec{path: prev.path, dir: prev.dir, session: prev.session, offset: prev.offset, pid: prev.pid})
		ch <- result{f, err}
	}()
	pids, err = panePIDs(ctx, run, m, it.PaneID, isClaude)
	w := <-ch
	if err != nil || w.err != nil || !slices.Contains(pids, prev.pid) || w.f.session == nil {
		return pids, joined{}, nil, err
	}
	if w.f.session.SessionID != prev.session || w.f.missing {
		return pids, joined{pid: prev.pid, dir: prev.dir, session: w.f.session.SessionID, cwd: w.f.session.CWD}, nil, nil
	}
	r := remoteRead(prev.pid, prev.session, w.f)
	return pids, joined{}, &r, nil
}
