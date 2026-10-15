package herdr

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// fakeHerdrCLI answers `herdr agent get` with the given status and seq and
// records every other call.
type fakeHerdrCLI struct {
	status string
	seq    int64
	calls  [][]string
}

func (f *fakeHerdrCLI) run(_ context.Context, _ []byte, argv []string) ([]byte, error) {
	if len(argv) >= 3 && argv[len(argv)-3] == "agent" && argv[len(argv)-2] == "get" {
		return []byte(fmt.Sprintf(`{"result":{"agent":{"pane_id":%q,"agent_status":%q,"state_change_seq":%d}}}`,
			argv[len(argv)-1], f.status, f.seq)), nil
	}
	f.calls = append(f.calls, argv)
	return []byte(`{}`), nil
}

func TestSendJSON(t *testing.T) {
	cases := []struct {
		name      string
		status    string
		seq       int64
		body      string
		want      int
		wantErr   string
		wantCalls [][]string
	}{
		{name: "text to an idle pane", status: "idle", seq: 5,
			body: `{"path":"local/wA:p1.md","expect_state_change_seq":5,"text":"- a list reply"}`, want: http.StatusOK,
			wantCalls: [][]string{{"herdr", "agent", "prompt", "wA:p1", "- a list reply"}}},
		{name: "text to a remote pane goes through --machine", status: "working", seq: 5,
			body: `{"path":"workbox2/w4:p1.md","expect_state_change_seq":5,"text":"hi"}`, want: http.StatusOK,
			wantCalls: [][]string{{"herdr", "--machine", "workbox2", "agent", "prompt", "w4:p1", "hi"}}},
		{name: "the pane moved since the sweep", status: "idle", seq: 6,
			body: `{"path":"local/wA:p1.md","expect_state_change_seq":5,"text":"hi"}`, want: http.StatusConflict, wantErr: "stale"},
		{name: "a dialog is up", status: "blocked", seq: 5,
			body: `{"path":"local/wA:p1.md","expect_state_change_seq":5,"text":"hi"}`, want: http.StatusConflict, wantErr: "blocked"},
		{name: "keys while working", status: "working", seq: 5,
			body: `{"path":"local/wA:p1.md","expect_state_change_seq":5,"keys":["enter"]}`, want: http.StatusConflict, wantErr: "working"},
		{name: "unknown pane", status: "idle", seq: 5,
			body: `{"path":"local/nope.md","expect_state_change_seq":5,"text":"hi"}`, want: http.StatusNotFound},
		{name: "no seq", status: "idle", seq: 5,
			body: `{"path":"local/wA:p1.md","text":"hi"}`, want: http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeHerdrCLI{status: tc.status, seq: tc.seq}
			c := NewController(Options{ReadStatePath: filepath.Join(t.TempDir(), "herdr-read.json")})
			c.col.run = f.run
			c.keyGap = 0
			c.snap = &Snapshot{Items: []Item{
				{Machine: "local", PaneID: "wA:p1", Agent: "claude", Status: "idle", StateChangeSeq: seq(5)},
				{Machine: "workbox2", PaneID: "w4:p1", Agent: "claude", Status: "working", StateChangeSeq: seq(5)},
			}}
			status, resp := c.SendJSON(context.Background(), []byte(tc.body))
			if status != tc.want {
				t.Fatalf("status = %d, want %d (%s)", status, tc.want, resp)
			}
			if tc.wantErr != "" {
				var body struct{ Error string }
				if err := json.Unmarshal(resp, &body); err != nil || body.Error != tc.wantErr {
					t.Errorf("body = %s, want error %q", resp, tc.wantErr)
				}
			}
			if !reflect.DeepEqual(f.calls, tc.wantCalls) {
				t.Errorf("herdr calls = %q, want %q", f.calls, tc.wantCalls)
			}
			if tc.want == http.StatusOK {
				data, err := c.SnapshotJSON()
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(data), `"seq_before":5`) {
					t.Errorf("snapshot does not carry the delivery: %s", data)
				}
			}
		})
	}
}

func TestResolveInput(t *testing.T) {
	it := Item{Agent: "claude", Dialog: &Dialog{Kind: "question", Options: []Choice{{N: 1}, {N: 2}}}}
	opt := func(n int) *int { return &n }
	cases := []struct {
		name     string
		req      sendRequest
		wantKind string
		wantKeys []string
		wantErr  bool
	}{
		{name: "text", req: sendRequest{Text: "hello there"}, wantKind: "text"},
		{name: "a list reply is text", req: sendRequest{Text: "- one\n- two"}, wantKind: "text"},
		{name: "a bare flag word is refused", req: sendRequest{Text: "--wait"}, wantErr: true},
		{name: "keys", req: sendRequest{Keys: []string{"2", "enter"}}, wantKind: "keys", wantKeys: []string{"2", "enter"}},
		{name: "ctrl+c is not an allowed key", req: sendRequest{Keys: []string{"ctrl+c"}}, wantErr: true},
		{name: "option resolves through the key table", req: sendRequest{Option: opt(2)}, wantKind: "keys", wantKeys: []string{"2", "enter"}},
		{name: "nothing", req: sendRequest{}, wantErr: true},
		{name: "two inputs", req: sendRequest{Text: "hi", Keys: []string{"enter"}}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kind, keys, err := resolveInput(it, tc.req)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if kind != tc.wantKind || !reflect.DeepEqual(keys, tc.wantKeys) {
				t.Errorf("got %q %q, want %q %q", kind, keys, tc.wantKind, tc.wantKeys)
			}
		})
	}
}

func TestOptionKeys(t *testing.T) {
	three := []Choice{{N: 1, Current: true}, {N: 2}, {N: 3}}
	cases := []struct {
		name    string
		agent   string
		dialog  *Dialog
		n       int
		want    []string
		wantErr bool
	}{
		{name: "claude question", agent: "claude", dialog: &Dialog{Kind: "question", Options: three}, n: 2, want: []string{"2", "enter"}},
		{name: "claude trust walks the cursor", agent: "claude", dialog: &Dialog{Kind: "trust", Options: three}, n: 2, want: []string{"down", "enter"}},
		{name: "claude multi-select", agent: "claude", dialog: &Dialog{Kind: "question", Multi: true, Options: three}, n: 3, want: []string{"3", "down", "down", "down", "enter", "enter"}},
		{name: "codex question opens the form first", agent: "codex", dialog: &Dialog{Kind: "question", Options: three}, n: 1, want: []string{"shift+left", "1", "enter"}},
		{name: "codex approval letters", agent: "codex", dialog: &Dialog{Kind: "permission", Options: three}, n: 2, want: []string{"p"}},
		{name: "opencode permission buttons", agent: "opencode", dialog: &Dialog{Kind: "permission", Options: three}, n: 3, want: []string{"right", "right", "enter"}},
		{name: "free text is not supported", agent: "claude", dialog: &Dialog{Kind: "question", Options: []Choice{{N: 1, FreeText: true}}}, n: 1, wantErr: true},
		{name: "pi answers in prose", agent: "pi", dialog: &Dialog{Kind: "question", Options: three}, n: 1, wantErr: true},
		{name: "no such option", agent: "claude", dialog: &Dialog{Kind: "question", Options: three}, n: 4, wantErr: true},
		{name: "no dialog", agent: "claude", n: 1, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := OptionKeys(Item{Agent: tc.agent, Dialog: tc.dialog}, tc.n)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("keys = %q, want %q", got, tc.want)
			}
		})
	}
}
