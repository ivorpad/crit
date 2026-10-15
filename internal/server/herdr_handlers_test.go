package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeHerdr struct {
	snap   string
	err    error
	viewed map[string]bool
}

func (f *fakeHerdr) SnapshotJSON() ([]byte, error) { return []byte(f.snap), f.err }
func (f *fakeHerdr) PlanJSON(path string) ([]byte, error) {
	if path != "local/wA:p1.md" {
		return nil, errors.New("no plan for " + path)
	}
	return []byte(`{"path":"local/wA:p1.md","plan_path":"~/.claude/plans/p.md","text":"# Plan","source":"local_disk"}`), nil
}

func (f *fakeHerdr) Focus(_ context.Context, path string) (string, string, bool, error) {
	switch path {
	case "local/wA:p1.md":
		return "local", "wA:p1", false, nil
	case "workbox2/w4:p1.md":
		return "", "", false, errors.New("herdr: exit status 1: machine workbox2 is not reachable")
	}
	return "", "", true, errors.New("no such pane")
}

func (f *fakeHerdr) SendJSON(_ context.Context, _ []byte) (int, []byte) {
	return http.StatusNotImplemented, []byte(`{"error":"not in the fake"}`)
}

func (f *fakeHerdr) KeysJSON(_ string, _ int) (int, []byte) {
	return http.StatusNotImplemented, []byte(`{"error":"not in the fake"}`)
}

func (f *fakeHerdr) SetViewed(path string, viewed bool) error {
	if path == "gone.md" {
		return errors.New("no pane gone.md")
	}
	f.viewed[path] = viewed
	return nil
}

func TestHerdrRoutes(t *testing.T) {
	srv, _ := newTestServer(t)
	do := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		return w
	}

	if w := do(http.MethodGet, "/api/herdr", ""); w.Code != http.StatusNotFound {
		t.Errorf("no provider: GET status = %d, want 404", w.Code)
	}

	f := &fakeHerdr{snap: `{"items":[]}`, viewed: map[string]bool{}}
	srv.SetHerdrProvider(f)

	tests := []struct {
		name, method, path, body string
		want                     int
		contains                 string
	}{
		{"snapshot", http.MethodGet, "/api/herdr", "", http.StatusOK, `{"items":[]}`},
		{"snapshot wrong method", http.MethodPost, "/api/herdr", "", http.StatusMethodNotAllowed, ""},
		{"mark viewed", http.MethodPost, "/api/herdr/viewed", `{"path":"local/wA:p1.md","viewed":true}`, http.StatusOK, `"viewed":true`},
		{"viewed bad body", http.MethodPost, "/api/herdr/viewed", `{"viewed":true}`, http.StatusBadRequest, ""},
		{"viewed unknown pane", http.MethodPost, "/api/herdr/viewed", `{"path":"gone.md","viewed":true}`, http.StatusNotFound, ""},
		{"viewed wrong method", http.MethodGet, "/api/herdr/viewed", "", http.StatusMethodNotAllowed, ""},
		{"plan", http.MethodGet, "/api/herdr/plan?path=local%2FwA%3Ap1.md", "", http.StatusOK, `"source":"local_disk"`},
		{"plan unknown pane", http.MethodGet, "/api/herdr/plan?path=local%2FwA%3Ap2.md", "", http.StatusNotFound, ""},
		{"plan wrong method", http.MethodPost, "/api/herdr/plan?path=x", "", http.StatusMethodNotAllowed, ""},
		{"focus", http.MethodPost, "/api/herdr/focus", `{"path":"local/wA:p1.md"}`, http.StatusOK, `"pane_id":"wA:p1"`},
		{"focus unknown pane", http.MethodPost, "/api/herdr/focus", `{"path":"gone.md"}`, http.StatusNotFound, ""},
		{"focus herdr fails", http.MethodPost, "/api/herdr/focus", `{"path":"workbox2/w4:p1.md"}`, http.StatusBadGateway, "not reachable"},
		{"focus bad body", http.MethodPost, "/api/herdr/focus", `{}`, http.StatusBadRequest, ""},
		{"focus wrong method", http.MethodGet, "/api/herdr/focus", "", http.StatusMethodNotAllowed, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := do(tt.method, tt.path, tt.body)
			if w.Code != tt.want || !strings.Contains(w.Body.String(), tt.contains) {
				t.Errorf("%s %s = %d %q, want %d containing %q", tt.method, tt.path, w.Code, w.Body.String(), tt.want, tt.contains)
			}
		})
	}
	if !f.viewed["local/wA:p1.md"] {
		t.Errorf("SetViewed not called: %v", f.viewed)
	}

	f.err = errors.New("first sweep not done")
	if w := do(http.MethodGet, "/api/herdr", ""); w.Code != http.StatusServiceUnavailable {
		t.Errorf("no snapshot yet: status = %d, want 503", w.Code)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/herdr/viewed", strings.NewReader(`{"path":"local/wA:p1.md","viewed":false}`))
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden || !f.viewed["local/wA:p1.md"] {
		t.Errorf("cross-site POST must be rejected: status %d", w.Code)
	}
}

func TestHerdrSessionRefusesRounds(t *testing.T) {
	srv, sess := newTestServer(t)
	for _, path := range []string{"/api/finish", "/api/round-complete"} {
		sess.ReviewType = "herdr"
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, nil))
		if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "deliver") {
			t.Errorf("herdr %s = %d %q, want 409", path, w.Code, w.Body.String())
		}
		sess.ReviewType = ""
		w = httptest.NewRecorder()
		srv.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, nil))
		if w.Code == http.StatusConflict {
			t.Errorf("code review %s must not be refused", path)
		}
	}
}
