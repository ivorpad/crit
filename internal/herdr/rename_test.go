package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestRenameJSON(t *testing.T) {
	longest := "a" + strings.Repeat("b", 31)
	cases := []struct {
		name     string
		body     string
		fail     error
		want     int
		wantName string
		wantArgv []string
		wantErr  string
	}{
		{name: "a name, trimmed", body: `{"path":"local/wA:p1.md","name":"  fix-flaky_test2  "}`, want: http.StatusOK,
			wantName: "fix-flaky_test2", wantArgv: []string{"herdr", "agent", "rename", "wA:p1", "fix-flaky_test2"}},
		{name: "a box pane goes through --machine", body: `{"path":"workbox2/w4:p1.md","name":"relay"}`, want: http.StatusOK,
			wantName: "relay", wantArgv: []string{"herdr", "--machine", "workbox2", "agent", "rename", "w4:p1", "relay"}},
		{name: "the plan file names its pane", body: `{"path":"local/wA:p1.plan.md","name":"plan"}`, want: http.StatusOK,
			wantName: "plan", wantArgv: []string{"herdr", "agent", "rename", "wA:p1", "plan"}},
		{name: "clear", body: `{"path":"local/wA:p1.md","clear":true}`, want: http.StatusOK,
			wantArgv: []string{"herdr", "agent", "rename", "wA:p1", "--clear"}},
		{name: "32 characters", body: `{"path":"local/wA:p1.md","name":"` + longest + `"}`, want: http.StatusOK,
			wantName: longest, wantArgv: []string{"herdr", "agent", "rename", "wA:p1", longest}},
		{name: "33 characters", body: `{"path":"local/wA:p1.md","name":"` + longest + `c"}`, want: http.StatusBadRequest, wantErr: errAgentName.Error()},
		{name: "spaces inside", body: `{"path":"local/wA:p1.md","name":"lab rename check"}`, want: http.StatusBadRequest, wantErr: errAgentName.Error()},
		{name: "an uppercase letter", body: `{"path":"local/wA:p1.md","name":"Vert"}`, want: http.StatusBadRequest, wantErr: errAgentName.Error()},
		{name: "a leading digit", body: `{"path":"local/wA:p1.md","name":"2fix"}`, want: http.StatusBadRequest, wantErr: errAgentName.Error()},
		{name: "a leading underscore", body: `{"path":"local/wA:p1.md","name":"_x"}`, want: http.StatusBadRequest, wantErr: errAgentName.Error()},
		{name: "a leading dash", body: `{"path":"local/wA:p1.md","name":"--clear"}`, want: http.StatusBadRequest, wantErr: errAgentName.Error()},
		{name: "a letter outside a-z", body: `{"path":"local/wA:p1.md","name":"café"}`, want: http.StatusBadRequest, wantErr: errAgentName.Error()},
		{name: "a control character", body: `{"path":"local/wA:p1.md","name":"bell\u0007"}`, want: http.StatusBadRequest, wantErr: errAgentName.Error()},
		{name: "empty", body: `{"path":"local/wA:p1.md","name":""}`, want: http.StatusBadRequest, wantErr: errAgentName.Error()},
		{name: "only spaces", body: `{"path":"local/wA:p1.md","name":"   "}`, want: http.StatusBadRequest, wantErr: errAgentName.Error()},
		{name: "a name and clear", body: `{"path":"local/wA:p1.md","name":"x","clear":true}`, want: http.StatusBadRequest},
		{name: "neither", body: `{"path":"local/wA:p1.md"}`, want: http.StatusBadRequest},
		{name: "no path", body: `{"name":"x"}`, want: http.StatusBadRequest},
		{name: "not JSON", body: `name=x`, want: http.StatusBadRequest},
		{name: "not in the sweep", body: `{"path":"local/nope.md","name":"x"}`, want: http.StatusNotFound},
		{name: "herdr fails", body: `{"path":"workbox2/w4:p1.md","clear":true}`, want: http.StatusBadGateway,
			fail:     errors.New(`herdr: exit status 1: {"error":{"code":"agent_not_found","message":"agent target w4:p1 not found"}}`),
			wantArgv: []string{"herdr", "--machine", "workbox2", "agent", "rename", "w4:p1", "--clear"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls [][]string
			c := NewController(Options{})
			c.col.run = func(_ context.Context, _ []byte, argv []string) ([]byte, error) {
				calls = append(calls, argv)
				return nil, tc.fail
			}
			c.snap = &Snapshot{Items: []Item{
				{Machine: "local", PaneID: "wA:p1", Agent: "claude", Plan: &Plan{Path: "~/.claude/plans/p.md", Text: "# Plan"}},
				{Machine: "workbox2", PaneID: "w4:p1", Agent: "codex"},
			}}
			status, resp := c.RenameJSON(context.Background(), []byte(tc.body))
			if status != tc.want {
				t.Fatalf("status = %d, want %d (%s)", status, tc.want, resp)
			}
			var got []string
			if len(calls) == 1 {
				got = calls[0]
			}
			if len(calls) > 1 || !reflect.DeepEqual(got, tc.wantArgv) {
				t.Errorf("herdr calls = %q, want %q", calls, tc.wantArgv)
			}
			var body struct {
				Name  *string `json:"name"`
				Error string  `json:"error"`
			}
			if err := json.Unmarshal(resp, &body); err != nil {
				t.Fatalf("answer is not JSON: %s", resp)
			}
			if tc.want == http.StatusOK && (body.Name == nil || *body.Name != tc.wantName) {
				t.Errorf("answer = %s, want name %q", resp, tc.wantName)
			}
			if tc.want != http.StatusOK && (body.Error == "" || (tc.wantErr != "" && body.Error != tc.wantErr)) {
				t.Errorf("answer = %s, want error %q", resp, tc.wantErr)
			}
		})
	}
}
