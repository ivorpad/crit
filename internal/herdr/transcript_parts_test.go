package herdr

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

// TestMain keeps every test in this package away from the real home directory
// and its Claude config dirs: tests that need files there make their own.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "herdr-test-home")
	if err != nil {
		panic(err)
	}
	_ = os.Setenv("HOME", home)
	_ = os.Unsetenv("CLAUDE_CONFIG_DIR")
	code := m.Run()
	_ = os.RemoveAll(home)
	os.Exit(code)
}

const (
	sessionA = "c8533f45-a0d4-4f60-851f-a184947673e0"
	sessionB = "0b1e2c3d-0000-4000-8000-0000000000b2"
	sessionC = "33333333-3333-4333-8333-333333333333"
)

func processInfo(procs string) []byte {
	return []byte(`{"id":"cli:pane:process_info","result":{"process_info":{"foreground_process_group_id":1,"foreground_processes":` + procs + `}}}`)
}

func TestParseClaudePIDs(t *testing.T) {
	tests := []struct {
		name  string
		procs string
		want  []int
	}{
		{"this Mac: name is the version, argv0 says claude",
			`[{"argv":["caffeinate","-i"],"argv0":"caffeinate","name":"caffeinate","pid":10},{"argv":["claude","--resume","x"],"argv0":"claude","cmdline":"claude --resume x","cwd":"/src/a","name":"2.1.300","pid":4242}]`,
			[]int{4242}},
		{"a box: no argv0, named claude", `[{"argv":["claude"],"name":"claude","pid":777}]`, []int{777}},
		{"a box: no argv0, argv[0] is a path to claude", `[{"argv":["/home/u/.local/bin/claude","--continue"],"name":"2.1.300","pid":778}]`, []int{778}},
		{"an interactive claude before a claude -p a tool started", `[{"argv":["claude","-p","sum it up"],"name":"claude","pid":5},{"argv":["claude"],"name":"claude","pid":9}]`, []int{9, 5}},
		{"a pid that is not a plain integer is skipped", `[{"argv":["claude"],"name":"claude","pid":"12; touch /tmp/x"},{"argv":["claude"],"name":"claude","pid":12.5},{"argv":["claude"],"name":"claude","pid":-3}]`, nil},
		{"no claude in the pane", `[{"argv":["zsh"],"argv0":"zsh","name":"zsh","pid":10},{"argv":["claudette"],"name":"claudette","pid":11}]`, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parsePanePIDs(processInfo(tt.procs), isClaude)
			if err != nil || !reflect.DeepEqual(got, tt.want) {
				t.Errorf("pids = %v, %v; want %v", got, err, tt.want)
			}
		})
	}
	if _, err := parsePanePIDs([]byte("not json"), isClaude); err == nil {
		t.Errorf("garbage must be an error")
	}
}

func TestFilterJSONL(t *testing.T) {
	user := `{"type":"user","message":{"content":"how do api-request lines look?"}}` + "\n"
	answer := `{"type":"assistant","message":{"content":[{"type":"text","text":"like this"}]}}` + "\n"
	debug := []string{
		`{"type":"api-request","body":"` + strings.Repeat("x", 300) + `"}` + "\n",
		`{"type":"api-request-blob","blob":"abc"}` + "\n",
		`{"type":"api-request-shape","shape":{"type":"user"}}` + "\n",
	}
	tests := []struct {
		name                 string
		data                 string
		want                 string
		used, dropped        int64
		clipped              int
		wantValidJSON, isCut bool
	}{
		{name: "request debug lines are dropped, a line that only mentions them is kept",
			data: user + debug[0] + debug[1] + answer + debug[2],
			want: user + answer, used: int64(len(user + debug[0] + debug[1] + answer + debug[2])),
			dropped: int64(len(debug[0] + debug[1] + debug[2]))},
		{name: "a last line with no newline waits",
			data: user + `{"type":"assistant","mess`,
			want: user, used: int64(len(user))},
		{name: "blank lines go", data: "\n" + user + "  \n", want: user, used: int64(len(user) + 4)},
		{name: "nothing complete yet", data: `{"type":"us`, want: "", used: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, used, dropped, clipped := filterJSONL([]byte(tt.data), isRequestDebug)
			if string(out) != tt.want || used != tt.used || dropped != tt.dropped || clipped != tt.clipped {
				t.Errorf("got %q used %d dropped %d clipped %d; want %q %d %d %d", out, used, dropped, clipped, tt.want, tt.used, tt.dropped, tt.clipped)
			}
		})
	}
}

func TestClipStrings(t *testing.T) {
	// Text: a run of only letters would count as base64 (TestClipStringsImageData).
	big := strings.Repeat("word ", clipMax/5+20)
	// "é" is two bytes: after one "a", clipMax falls inside one. "\n" is two
	// bytes as written: after one "a", clipMax falls inside an escape.
	accents := "a" + strings.Repeat("é", clipMax/2+10)
	escapes := "a" + strings.Repeat(`\n`, clipMax/2+10)
	tests := []struct {
		name    string
		line    string
		clipped int
		keep    []string // parts that must stay byte for byte
		cutAt   int      // bytes kept of the first long string
	}{
		{"one long string", `{"type":"user","tool":{"z":1,"content":"` + big + `"},"a":2}`, 1, []string{`{"type":"user","tool":{"z":1,"content":"`, `"},"a":2}`}, clipMax},
		{"two long strings", `{"a":"` + big + `","b":"` + big + `"}`, 2, []string{`{"a":"`, `","b":"`}, clipMax},
		{"a cut inside a UTF-8 sequence backs off", `{"t":"` + accents + `"}`, 1, []string{`{"t":"a`}, clipMax - 1},
		{"a cut inside an escape backs off", `{"t":"` + escapes + `"}`, 1, []string{`{"t":"a`}, clipMax - 1},
		{"short strings in a long line stay", `{"a":"` + strings.Repeat("b", clipMax-10) + `","c":"` + strings.Repeat("d", 100) + `"}`, 0, nil, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, n := clipStrings([]byte(tt.line))
			if n != tt.clipped {
				t.Fatalf("clipped = %d, want %d", n, tt.clipped)
			}
			if !json.Valid(got) || !utf8.Valid(got) {
				t.Fatalf("not valid JSON / UTF-8 after the cut: %.200s", got)
			}
			if n == 0 {
				if string(got) != tt.line {
					t.Errorf("a line with nothing to cut must stay as it is")
				}
				return
			}
			for _, k := range tt.keep {
				if !strings.Contains(string(got), k) {
					t.Errorf("lost %q", k)
				}
			}
			var v map[string]any
			_ = json.Unmarshal(got, &v)
			first := firstString(v)
			if strings.ContainsRune(first, utf8.RuneError) {
				t.Errorf("the cut split a character")
			}
			note := " [… " // the rest of the note carries the count
			i := strings.Index(string(got), note)
			start := strings.Index(tt.line, tt.keep[0]) + len(tt.keep[0])
			if tt.cutAt == clipMax-1 {
				start-- // the kept "a" is part of the string
			}
			if i-start != tt.cutAt {
				t.Errorf("kept %d bytes of the string, want %d", i-start, tt.cutAt)
			}
		})
	}
}

// TestClipStringsImageData: a picture's base64 is cut to the note alone (the
// page fetches it from /api/herdr/image); text beside it keeps 64 KB.
func TestClipStringsImageData(t *testing.T) {
	data := strings.Repeat("iVBORw0KGgoAAAANSUhEUgAA+/8=", clipMax/28+40)
	text := strings.Repeat("ok \\n", clipMax/5+20)
	line := `{"type":"user","message":{"content":[{"tool_use_id":"t1","type":"tool_result","content":[{"type":"image","source":{"type":"base64","data":"` + data +
		`","media_type":"image/png"}}]}]},"uuid":"u1","toolUseResult":{"type":"image","file":{"base64":"` + data + `","type":"image/png"},"text":"` + text + `"}}`
	got, n := clipStrings([]byte(line))
	if n != 3 || !json.Valid(got) {
		t.Fatalf("clipped = %d, valid JSON = %v; want 3 and true", n, json.Valid(got))
	}
	var v struct {
		Message struct {
			Content []struct {
				Content []struct {
					Source struct {
						Data      string `json:"data"`
						MediaType string `json:"media_type"`
					} `json:"source"`
				} `json:"content"`
			} `json:"content"`
		} `json:"message"`
		UUID          string `json:"uuid"`
		ToolUseResult struct {
			File struct{ Base64 string } `json:"file"`
			Text string                  `json:"text"`
		} `json:"toolUseResult"`
	}
	if err := json.Unmarshal(got, &v); err != nil {
		t.Fatal(err)
	}
	note := " [… " + strconv.Itoa(len(data)) + " bytes cut]"
	src := v.Message.Content[0].Content[0].Source
	if src.Data != note || v.ToolUseResult.File.Base64 != note {
		t.Errorf("image data = %q and %q, want the note alone, %q", src.Data, v.ToolUseResult.File.Base64, note)
	}
	if src.MediaType != "image/png" || v.UUID != "u1" {
		t.Errorf("the rest of the line must stay: media %q uuid %q", src.MediaType, v.UUID)
	}
	start := strings.Index(string(got), `"text":"`) + len(`"text":"`)
	if kept := strings.Index(string(got[start:]), " [… "); kept < clipMax-1 || kept > clipMax {
		t.Errorf("text kept %d bytes as written, want %d (one less to keep an escape whole)", kept, clipMax)
	}
	if short := `{"data":"` + strings.Repeat("QUJD", 100) + `"}`; string(must(clipStrings([]byte(short)))) != short {
		t.Errorf("short base64 must stay as it is")
	}
}

func must(b []byte, _ int) []byte { return b }

// firstString is the first long string value in a decoded line.
func firstString(v any) string {
	switch x := v.(type) {
	case string:
		if len(x) > 1000 {
			return x
		}
	case map[string]any:
		for _, e := range x {
			if s := firstString(e); s != "" {
				return s
			}
		}
	}
	return ""
}

func TestRemoteValuesAreChecked(t *testing.T) {
	ok := fetchSpec{path: ".claude/projects/-home-u-app/" + sessionA + ".jsonl", dir: ".claude", session: sessionA, offset: 10, pid: 777}
	if _, err := fetchScript(ok); err != nil {
		t.Fatalf("a checked spec must build: %v", err)
	}
	bad := []struct {
		name string
		edit func(*fetchSpec)
	}{
		{"dir name with a quote", func(s *fetchSpec) { s.dir = ".claude'x" }},
		{"dir name with a space", func(s *fetchSpec) { s.dir = ".claude x" }},
		{"dir name that is not a claude dir", func(s *fetchSpec) { s.dir = ".ssh" }},
		{"slug with a quote", func(s *fetchSpec) { s.path = ".claude/projects/-home-u'x/" + sessionA + ".jsonl" }},
		{"slug with a space", func(s *fetchSpec) { s.path = ".claude/projects/-home-u x/" + sessionA + ".jsonl" }},
		{"slug that climbs", func(s *fetchSpec) { s.path = ".claude/projects/../../.ssh/" + sessionA + ".jsonl" }},
		{"session that is not a UUID", func(s *fetchSpec) { s.session = "../../etc/passwd" }},
		{"session with a command", func(s *fetchSpec) { s.session = "$(touch x)" }},
		{"negative offset", func(s *fetchSpec) { s.offset = -1 }},
		{"pid without a dir", func(s *fetchSpec) { s.dir = "" }},
	}
	for _, tt := range bad {
		t.Run(tt.name, func(t *testing.T) {
			s := ok
			tt.edit(&s)
			if script, err := fetchScript(s); err == nil {
				t.Errorf("built %q", script)
			}
		})
	}
	if _, err := sshArgv(machine{Label: "evil", Target: "-oProxyCommand=x"}, "true"); err == nil {
		t.Errorf("a target that reads as an ssh option must be refused")
	}
	if _, err := sshArgv(machine{Label: "w", Target: "workdone-ib3"}, "echo 'x'"); err == nil {
		t.Errorf("a script with a single quote must be refused")
	}
	argv, err := sshArgv(machine{Label: "workbox3", Target: "workdone-ib3"}, locateScript([]int{777, 12}))
	want := []string{"ssh", "-T", "-o", "BatchMode=yes", "-o", "ConnectTimeout=8", "workdone-ib3"}
	if err != nil || !reflect.DeepEqual(argv[:7], want) || !strings.HasPrefix(argv[7], "sh -c 'cd ~ || exit 1; for p in 777 12; do") {
		t.Errorf("argv = %q, %v", argv, err)
	}
}

func TestParseLocate(t *testing.T) {
	good := `{"pid":777,"sessionId":"` + sessionA + `","cwd":"/home/u/app"}`
	tests := []struct {
		name    string
		out     string
		wantDir string
		wantOK  bool
	}{
		{"one file", "== 777 .claude\n" + good + "\n", ".claude", true},
		{"a login banner first", "Welcome to ib3\n== 777 .claude-work\n" + good + "\n", ".claude-work", true},
		{"the newest (first) file of a pid wins", "== 777 .claude-work\n" + good + "\n== 777 .claude\n" + good + "\n", ".claude-work", true},
		{"dir name with a quote", "== 777 .claude'x\n" + good + "\n", "", false},
		{"dir name with a space", "== 777 .claude x\n" + good + "\n", "", false},
		{"pid that is not an integer", "== 77x .claude\n" + good + "\n", "", false},
		{"session that is not a UUID", "== 777 .claude\n" + `{"sessionId":"../../etc/passwd","cwd":"/x"}` + "\n", "", false},
		{"pretty-printed file", "== 777 .claude\n{\n  \"sessionId\": \"" + sessionA + "\",\n  \"cwd\": \"/home/u/app\"\n}\n", ".claude", true},
		{"nothing found", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			j, ok := parseLocate([]byte(tt.out), []int{777})
			if ok != tt.wantOK || j.dir != tt.wantDir || (ok && (j.session != sessionA || j.cwd != "/home/u/app" || j.pid != 777)) {
				t.Errorf("got %+v, %v; want dir %q, %v", j, ok, tt.wantDir, tt.wantOK)
			}
		})
	}
}

func TestClaudeRefChecksEveryConfigDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	custom := filepath.Join(t.TempDir(), "custom-claude")
	t.Setenv("CLAUDE_CONFIG_DIR", custom)
	mustWrite(t, filepath.Join(home, ".claude.json"), "{}")
	mustWrite(t, filepath.Join(home, ".claude", "projects", "-src-a", sessionB+".jsonl"), "{}\n")
	mustWrite(t, filepath.Join(home, ".claude-work", "projects", "-src-a", sessionA+".jsonl"), "{}\n")
	mustWrite(t, filepath.Join(custom, "projects", "-src-b", sessionA+".jsonl"), "{}\n")

	tests := []struct {
		name, cwd, session string
		m                  machine
		want               string
	}{
		{"~/.claude", "/src/a", sessionB, machine{Label: localLabel}, filepath.Join(home, ".claude", "projects", "-src-a", sessionB+".jsonl")},
		{"~/.claude-work", "/src/a", sessionA, machine{Label: localLabel}, filepath.Join(home, ".claude-work", "projects", "-src-a", sessionA+".jsonl")},
		{"$CLAUDE_CONFIG_DIR", "/src/b", sessionA, machine{Label: localLabel}, filepath.Join(custom, "projects", "-src-b", sessionA+".jsonl")},
		{"no such transcript", "/src/c", sessionA, machine{Label: localLabel}, ""},
		{"another machine is not looked at", "/src/a", sessionA, machine{Label: "workbox3", Target: "workdone-ib3"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ref := claudeRef(tt.m, tt.cwd, tt.session)
			if ref.Path != tt.want || ref.Session != tt.session || ref.Kind != refClaudeJSONL {
				t.Errorf("ref = %+v, want path %q", ref, tt.want)
			}
		})
	}
}

func mustWrite(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
