package server

import (
	"encoding/json"
	"log"
	"net/http"
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
}

// refuseHerdrRound answers 409 for Finish and round-complete in a herdr session:
// nothing is delivered yet, and an Approve would stop the daemon and, with
// cleanup_on_approve, delete the review folder and every read mark.
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
