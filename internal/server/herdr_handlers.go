package server

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strconv"
)

// HerdrProvider is the herdr-control-center collector as the server sees it. The
// server never imports the herdr package; cmd/crit wires the two together.
type HerdrProvider interface {
	// SnapshotJSON is the latest sweep: machines and one item per pane, each with
	// its review path and whether the owner marked its current turn read.
	SnapshotJSON() ([]byte, error)
	// SetViewed marks the pane at path read (bound to its current turn) or unread.
	SetViewed(path string, viewed bool) error
	// PlanJSON is the ExitPlanMode plan of the pane at path; an error means the
	// pane has no plan in the latest sweep.
	PlanJSON(path string) ([]byte, error)
	// Focus brings the pane at path to the front of the owner's herdr TUI.
	// notFound reports a path that is not in the latest sweep; any other error
	// is herdr's own failure.
	Focus(ctx context.Context, path string) (machine, paneID string, notFound bool, err error)
	// SendJSON delivers one reply (text, keys or a dialog option) to the pane at
	// path and answers as HTTP: the status and a JSON body. See docs/herdr-api.md.
	SendJSON(ctx context.Context, body []byte) (status int, resp []byte)
	// KeysJSON is the key sequence SendJSON would press for option on the pane's
	// dialog, as HTTP status and JSON body.
	KeysJSON(path string, option int) (status int, resp []byte)
	// TranscriptJSON is the Claude transcript of the pane at path, filtered, as
	// HTTP status and JSON body; with since, only the lines after that version.
	TranscriptJSON(ctx context.Context, path, since string) (status int, resp []byte)
	// ChildrenJSON lists the sub-agents of the pane at path (Claude subagents and
	// teammates, Codex sub-agents; this Mac only), as HTTP status and JSON body.
	ChildrenJSON(ctx context.Context, path string) (status int, resp []byte)
	// ChildTranscriptJSON is one sub-agent's transcript, shaped like
	// TranscriptJSON's.
	ChildTranscriptJSON(ctx context.Context, path, child, since string) (status int, resp []byte)
	// RenameJSON sets or clears herdr's agent name for the pane at path, as HTTP
	// status and JSON body.
	RenameJSON(ctx context.Context, body []byte) (status int, resp []byte)
	// ExitJSON quits the agent in the pane at path with its own keys, then types
	// exit into its shell, which closes the pane; as HTTP status and JSON body.
	ExitJSON(ctx context.Context, body []byte) (status int, resp []byte)
	// CloseJSON closes the pane at path (`herdr pane close`), as HTTP status and
	// JSON body.
	CloseJSON(ctx context.Context, body []byte) (status int, resp []byte)
	// ImageBytes is one picture of the pane's transcript: the line with that uuid,
	// block "i" or "i.j" of its message. On 200 the body is the picture and
	// contentType its media type; otherwise a JSON error.
	ImageBytes(ctx context.Context, path, uuid, block string) (status int, contentType string, body []byte)
	// FileBytes is a picture on the pane's machine by its absolute path (one an
	// agent linked in an answer). On 200 the body is the picture and contentType
	// its sniffed type; otherwise a JSON error.
	FileBytes(ctx context.Context, path, file string) (status int, contentType string, body []byte)
}

// GET /api/herdr/transcript?path=<review path>[&child=<sub-agent id>][&since=<version>]
func (s *Server) handleHerdrTranscript(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	p := s.herdrProvider()
	if p == nil {
		writeHerdrJSON(w, http.StatusNotFound, []byte(`{"error":"unknown pane"}`))
		return
	}
	q := r.URL.Query()
	if child := q.Get("child"); child != "" {
		status, resp := p.ChildTranscriptJSON(r.Context(), q.Get("path"), child, q.Get("since"))
		writeHerdrJSON(w, status, resp)
		return
	}
	status, resp := p.TranscriptJSON(r.Context(), q.Get("path"), q.Get("since"))
	writeHerdrJSON(w, status, resp)
}

// GET /api/herdr/children?path=<review path>
func (s *Server) handleHerdrChildren(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	p := s.herdrProvider()
	if p == nil {
		writeHerdrJSON(w, http.StatusNotFound, []byte(`{"error":"unknown pane"}`))
		return
	}
	status, resp := p.ChildrenJSON(r.Context(), r.URL.Query().Get("path"))
	writeHerdrJSON(w, status, resp)
}

// GET /api/herdr/image?path=<review path>&uuid=<line uuid>&block=<i>[.<j>]
func (s *Server) handleHerdrImage(w http.ResponseWriter, r *http.Request) {
	if p := s.herdrPictureProvider(w, r); p != nil {
		q := r.URL.Query()
		status, contentType, body := p.ImageBytes(r.Context(), q.Get("path"), q.Get("uuid"), q.Get("block"))
		writeHerdrPicture(w, status, contentType, body, "private, max-age=86400")
	}
}

// GET /api/herdr/file?path=<review path>&file=<absolute path on the pane's machine>
func (s *Server) handleHerdrFile(w http.ResponseWriter, r *http.Request) {
	if p := s.herdrPictureProvider(w, r); p != nil {
		q := r.URL.Query()
		status, contentType, body := p.FileBytes(r.Context(), q.Get("path"), q.Get("file"))
		// The file can change under the same path.
		writeHerdrPicture(w, status, contentType, body, "no-cache")
	}
}

// herdrPictureProvider is the provider for a picture GET, or nil once it has
// answered the request itself. A GET passes the server's Sec-Fetch-Site check,
// so picture routes refuse other sites here: no page elsewhere can put a pane's
// pictures in an <img>.
func (s *Server) herdrPictureProvider(w http.ResponseWriter, r *http.Request) HerdrProvider {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return nil
	}
	// "none" is the person opening the URL themselves (address bar, bookmark).
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		writeHerdrJSON(w, http.StatusForbidden, []byte(`{"error":"cross-site request"}`))
		return nil
	}
	p := s.herdrProvider()
	if p == nil {
		writeHerdrJSON(w, http.StatusNotFound, []byte(`{"error":"unknown pane"}`))
	}
	return p
}

// writeHerdrPicture writes a picture on 200, else the JSON error in body.
func writeHerdrPicture(w http.ResponseWriter, status int, contentType string, body []byte, cacheControl string) {
	if status != http.StatusOK {
		writeHerdrJSON(w, status, body)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", cacheControl)
	_, _ = w.Write(body)
}

// POST /api/herdr/rename {"path", "name"} or {"path", "clear": true}
func (s *Server) handleHerdrRename(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	p := s.herdrProvider()
	if p == nil {
		http.Error(w, "Not a herdr session", http.StatusNotFound)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
	if err != nil {
		http.Error(w, "Body too large or unreadable", http.StatusBadRequest)
		return
	}
	status, resp := p.RenameJSON(r.Context(), body)
	writeHerdrJSON(w, status, resp)
}

// POST /api/herdr/exit {"path"} and POST /api/herdr/close {"path"}: the pane's
// agent quits and its shell closes the pane, or herdr closes the pane outright.
// An exit takes a few seconds; the page has asked the owner before either.
func (s *Server) handleHerdrExit(w http.ResponseWriter, r *http.Request) {
	s.handleHerdrEnd(w, r, HerdrProvider.ExitJSON)
}

func (s *Server) handleHerdrClose(w http.ResponseWriter, r *http.Request) {
	s.handleHerdrEnd(w, r, HerdrProvider.CloseJSON)
}

func (s *Server) handleHerdrEnd(w http.ResponseWriter, r *http.Request, end func(HerdrProvider, context.Context, []byte) (int, []byte)) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	p := s.herdrProvider()
	if p == nil {
		http.Error(w, "Not a herdr session", http.StatusNotFound)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
	if err != nil {
		http.Error(w, "Body too large or unreadable", http.StatusBadRequest)
		return
	}
	status, resp := end(p, r.Context(), body)
	writeHerdrJSON(w, status, resp)
}

// POST /api/herdr/send {"path", "expect_state_change_seq", "text"|"keys"|"option"}
func (s *Server) handleHerdrSend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	p := s.herdrProvider()
	if p == nil {
		http.Error(w, "Not a herdr session", http.StatusNotFound)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
	if err != nil {
		http.Error(w, "Body too large or unreadable", http.StatusBadRequest)
		return
	}
	status, resp := p.SendJSON(r.Context(), body)
	writeHerdrJSON(w, status, resp)
}

// GET /api/herdr/keys?path=<review path>&option=<n>
func (s *Server) handleHerdrKeys(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	p := s.herdrProvider()
	if p == nil {
		http.Error(w, "Not a herdr session", http.StatusNotFound)
		return
	}
	q := r.URL.Query()
	option, err := strconv.Atoi(q.Get("option"))
	if q.Get("path") == "" || err != nil {
		http.Error(w, "Query must have path and a numeric option", http.StatusBadRequest)
		return
	}
	status, resp := p.KeysJSON(q.Get("path"), option)
	writeHerdrJSON(w, status, resp)
}

func writeHerdrJSON(w http.ResponseWriter, status int, resp []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(resp)
}

// POST /api/herdr/focus {"path": "<review path>"}
func (s *Server) handleHerdrFocus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	p := s.herdrProvider()
	if p == nil {
		http.Error(w, "Not a herdr session", http.StatusNotFound)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Path == "" {
		http.Error(w, "Body must be {\"path\": string}", http.StatusBadRequest)
		return
	}
	machine, paneID, notFound, err := p.Focus(r.Context(), req.Path)
	switch {
	case notFound:
		http.Error(w, err.Error(), http.StatusNotFound)
	case err != nil:
		writeJSONStatus(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
	default:
		writeJSON(w, map[string]string{"path": req.Path, "machine": machine, "pane_id": paneID})
	}
}

// refuseHerdrRound answers 409 for Finish and round-complete in a herdr session:
// replies go out one at a time through /api/herdr/send, there is no batch yet,
// and an Approve would stop the daemon and, with cleanup_on_approve, delete the
// review folder and every read mark.
func refuseHerdrRound(w http.ResponseWriter, sess *Session) bool {
	if sess == nil || sess.ReviewType != "herdr" {
		return false
	}
	writeJSONStatus(w, http.StatusConflict, map[string]string{"error": "herdr sessions do not deliver anything yet"})
	return true
}

// GET /api/herdr/plan?path=<review path>
func (s *Server) handleHerdrPlan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	p := s.herdrProvider()
	if p == nil {
		http.Error(w, "Not a herdr session", http.StatusNotFound)
		return
	}
	data, err := p.PlanJSON(r.URL.Query().Get("path"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}

// SetHerdrProvider enables the /api/herdr routes.
func (s *Server) SetHerdrProvider(p HerdrProvider) {
	s.herdrMu.Lock()
	s.herdr = p
	s.herdrMu.Unlock()
}

func (s *Server) herdrProvider() HerdrProvider {
	s.herdrMu.RLock()
	defer s.herdrMu.RUnlock()
	return s.herdr
}

// GET /api/herdr — the latest sweep.
func (s *Server) handleHerdr(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	p := s.herdrProvider()
	if p == nil {
		http.Error(w, "Not a herdr session", http.StatusNotFound)
		return
	}
	data, err := p.SnapshotJSON()
	if err != nil {
		log.Printf("herdr snapshot: %v", err)
		http.Error(w, "No herdr snapshot yet", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}

// POST /api/herdr/viewed {"path": "<machine>/<pane>.md", "viewed": true}
func (s *Server) handleHerdrViewed(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	p := s.herdrProvider()
	if p == nil {
		http.Error(w, "Not a herdr session", http.StatusNotFound)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var req struct {
		Path   string `json:"path"`
		Viewed bool   `json:"viewed"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Path == "" {
		http.Error(w, "Body must be {\"path\": string, \"viewed\": bool}", http.StatusBadRequest)
		return
	}
	if err := p.SetViewed(req.Path, req.Viewed); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	writeJSON(w, map[string]any{"path": req.Path, "viewed": req.Viewed})
}
