package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

type fakeHerdr struct {
	snap    string
	err     error
	viewed  map[string]bool
	renamed string // the last rename body
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

func (f *fakeHerdr) TranscriptJSON(_ context.Context, path, since string) (int, []byte) {
	switch path {
	case "local/wA:p1.md":
		return http.StatusOK, []byte(`{"path":"local/wA:p1.md","since":"` + since + `"}`)
	case "local/wA:p2.md":
		return http.StatusNotFound, []byte(`{"error":"no transcript","reason":"No codex reader yet"}`)
	case "workbox2/w4:p1.md":
		return http.StatusBadGateway, []byte(`{"error":"ssh: connect timed out"}`)
	}
	return http.StatusNotFound, []byte(`{"error":"unknown pane"}`)
}

func (f *fakeHerdr) RenameJSON(_ context.Context, body []byte) (int, []byte) {
	f.renamed = string(body)
	switch {
	case strings.Contains(string(body), "gone.md"):
		return http.StatusNotFound, []byte(`{"error":"no such pane in the latest sweep"}`)
	case strings.Contains(string(body), "workbox2"):
		return http.StatusBadGateway, []byte(`{"error":"herdr: exit status 1"}`)
	case strings.Contains(string(body), `"name":""`):
		return http.StatusBadRequest, []byte(`{"error":"agent name must start with a lowercase letter and contain only lowercase letters, digits, '-' or '_' (1-32 characters)"}`)
	}
	return http.StatusOK, []byte(`{"path":"local/wA:p1.md","name":"vert"}`)
}

func (f *fakeHerdr) ExitJSON(_ context.Context, _ []byte) (int, []byte) {
	return http.StatusNotImplemented, []byte(`{"error":"not in the fake"}`)
}

func (f *fakeHerdr) CloseJSON(_ context.Context, _ []byte) (int, []byte) {
	return http.StatusNotImplemented, []byte(`{"error":"not in the fake"}`)
}

// fakePNG is the start of a PNG, all the fake serves.
var fakePNG = "\x89PNG\r\n\x1a\nfake"

func (f *fakeHerdr) ImageBytes(_ context.Context, path, uuid, block string) (int, string, []byte) {
	switch {
	case path == "local/wA:p1.md" && uuid == "11111111-1111-4111-8111-111111111111" && block == "0.0":
		return http.StatusOK, "image/png", []byte(fakePNG)
	case path == "local/wA:p1.md" && block == "x":
		return http.StatusBadRequest, "application/json", []byte(`{"error":"block must be \"<i>\" or \"<i>.<j>\""}`)
	case path == "local/wA:p1.md":
		return http.StatusNotFound, "application/json", []byte(`{"error":"no image","reason":"No line of the transcript has uuid ` + uuid + `"}`)
	case path == "workbox2/w4:p1.md":
		return http.StatusBadGateway, "application/json", []byte(`{"error":"ssh: connect timed out"}`)
	}
	return http.StatusNotFound, "application/json", []byte(`{"error":"unknown pane"}`)
}

func (f *fakeHerdr) FileBytes(_ context.Context, _, _ string) (int, string, []byte) {
	return http.StatusNotImplemented, "application/json", []byte(`{"error":"not in the fake"}`)
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
	if w := do(http.MethodGet, "/api/herdr/transcript?path=local%2FwA%3Ap1.md", ""); w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), `"unknown pane"`) {
		t.Errorf("no provider: transcript = %d %q, want 404 unknown pane", w.Code, w.Body.String())
	}
	if w := do(http.MethodPost, "/api/herdr/rename", `{"path":"local/wA:p1.md","name":"x"}`); w.Code != http.StatusNotFound {
		t.Errorf("no provider: rename status = %d, want 404", w.Code)
	}
	if w := do(http.MethodGet, "/api/herdr/image?path=local%2FwA%3Ap1.md&uuid=11111111-1111-4111-8111-111111111111&block=0.0", ""); w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), `"unknown pane"`) {
		t.Errorf("no provider: image = %d %q, want 404 unknown pane", w.Code, w.Body.String())
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
		{"transcript", http.MethodGet, "/api/herdr/transcript?path=local%2FwA%3Ap1.md&since=abc%3A1%3A42", "", http.StatusOK, `"since":"abc:1:42"`},
		{"transcript of a codex pane", http.MethodGet, "/api/herdr/transcript?path=local%2FwA%3Ap2.md", "", http.StatusNotFound, "No codex reader yet"},
		{"transcript unknown pane", http.MethodGet, "/api/herdr/transcript?path=gone.md", "", http.StatusNotFound, "unknown pane"},
		{"transcript read fails", http.MethodGet, "/api/herdr/transcript?path=workbox2%2Fw4%3Ap1.md", "", http.StatusBadGateway, "timed out"},
		{"transcript wrong method", http.MethodPost, "/api/herdr/transcript?path=local%2FwA%3Ap1.md", "", http.StatusMethodNotAllowed, ""},
		{"image", http.MethodGet, "/api/herdr/image?path=local%2FwA%3Ap1.md&uuid=11111111-1111-4111-8111-111111111111&block=0.0", "", http.StatusOK, fakePNG},
		{"image bad block", http.MethodGet, "/api/herdr/image?path=local%2FwA%3Ap1.md&uuid=11111111-1111-4111-8111-111111111111&block=x", "", http.StatusBadRequest, "block must be"},
		{"image no such line", http.MethodGet, "/api/herdr/image?path=local%2FwA%3Ap1.md&uuid=22222222-2222-4222-8222-222222222222&block=0", "", http.StatusNotFound, "No line of the transcript"},
		{"image unknown pane", http.MethodGet, "/api/herdr/image?path=gone.md&uuid=11111111-1111-4111-8111-111111111111&block=0", "", http.StatusNotFound, "unknown pane"},
		{"image read fails", http.MethodGet, "/api/herdr/image?path=workbox2%2Fw4%3Ap1.md&uuid=11111111-1111-4111-8111-111111111111&block=0", "", http.StatusBadGateway, "timed out"},
		{"image wrong method", http.MethodPost, "/api/herdr/image?path=local%2FwA%3Ap1.md&uuid=11111111-1111-4111-8111-111111111111&block=0.0", "", http.StatusMethodNotAllowed, ""},
		{"rename", http.MethodPost, "/api/herdr/rename", `{"path":"local/wA:p1.md","name":"vert"}`, http.StatusOK, `"name":"vert"`},
		{"rename bad name", http.MethodPost, "/api/herdr/rename", `{"path":"local/wA:p1.md","name":""}`, http.StatusBadRequest, "start with a lowercase letter"},
		{"rename unknown pane", http.MethodPost, "/api/herdr/rename", `{"path":"gone.md","name":"x"}`, http.StatusNotFound, ""},
		{"rename herdr fails", http.MethodPost, "/api/herdr/rename", `{"path":"workbox2/w4:p1.md","clear":true}`, http.StatusBadGateway, "exit status"},
		{"rename too large", http.MethodPost, "/api/herdr/rename", `{"path":"` + strings.Repeat("x", 70<<10) + `"}`, http.StatusBadRequest, ""},
		{"rename wrong method", http.MethodGet, "/api/herdr/rename", "", http.StatusMethodNotAllowed, ""},
		// /herdr is its own page (web/herdr.html), not a file under "/".
		{"herdr page wrong method", http.MethodPost, "/herdr", "", http.StatusMethodNotAllowed, ""},
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

	image := "/api/herdr/image?path=local%2FwA%3Ap1.md&uuid=11111111-1111-4111-8111-111111111111&block=0.0"
	for _, site := range []string{"", "same-origin", "none", "same-site", "cross-site"} {
		req := httptest.NewRequest(http.MethodGet, image, nil)
		if site != "" {
			req.Header.Set("Sec-Fetch-Site", site)
		}
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		h := w.Header()
		switch site {
		case "same-site", "cross-site":
			if w.Code != http.StatusForbidden || strings.Contains(w.Body.String(), "PNG") {
				t.Errorf("image with Sec-Fetch-Site %q = %d, want 403 and no picture", site, w.Code)
			}
		default:
			if w.Code != http.StatusOK || w.Body.String() != fakePNG || h.Get("Content-Type") != "image/png" || h.Get("X-Content-Type-Options") != "nosniff" ||
				h.Get("Cache-Control") != "private, max-age=86400" || h.Get("Content-Length") != strconv.Itoa(len(fakePNG)) {
				t.Errorf("image with Sec-Fetch-Site %q = %d %v", site, w.Code, h)
			}
		}
	}

	f.renamed = ""
	req = httptest.NewRequest(http.MethodPost, "/api/herdr/rename", strings.NewReader(`{"path":"local/wA:p1.md","name":"evil"}`))
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden || f.renamed != "" {
		t.Errorf("cross-site rename must be rejected before it reaches herdr: status %d, body %q", w.Code, f.renamed)
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
