package session

import (
	"path/filepath"
	"testing"
)

func TestReplaceSyntheticFiles(t *testing.T) {
	s, err := NewSyntheticSession("herdr", []string{"herdr"}, []SyntheticFile{
		{Path: "m/a.md", Content: "a1"},
		{Path: "m/b.md", Content: "b1"},
		{Path: "m/c.md", Content: "c1"},
	}, t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	if s.ReviewType != "herdr" || s.Mode != "files" || len(s.Files) != 3 || s.Files[0].FileType != "markdown" || s.Files[0].FileHash == "" {
		t.Fatalf("session = %+v", s)
	}
	s.Files[1].Comments = []Comment{{ID: "c1", Body: "reply"}}
	hashA := s.Files[0].FileHash

	steps := []struct {
		name        string
		files       []SyntheticFile
		wantChanged bool
		wantPaths   []string
	}{
		{"same content", []SyntheticFile{{Path: "m/a.md", Content: "a1"}, {Path: "m/b.md", Content: "b1"}, {Path: "m/c.md", Content: "c1"}}, false, []string{"m/a.md", "m/b.md", "m/c.md"}},
		{"content change", []SyntheticFile{{Path: "m/a.md", Content: "a2"}, {Path: "m/b.md", Content: "b1"}, {Path: "m/c.md", Content: "c1"}}, true, []string{"m/a.md", "m/b.md", "m/c.md"}},
		{"pane gone, no comments", []SyntheticFile{{Path: "m/a.md", Content: "a2"}, {Path: "m/b.md", Content: "b1"}}, true, []string{"m/a.md", "m/b.md"}},
		{"pane with comments gone is kept", []SyntheticFile{{Path: "m/a.md", Content: "a2"}}, false, []string{"m/a.md", "m/b.md"}},
		{"new pane", []SyntheticFile{{Path: "n/d.md", Content: "d1"}, {Path: "m/a.md", Content: "a2"}}, true, []string{"n/d.md", "m/a.md", "m/b.md"}},
	}
	for _, st := range steps {
		if got := s.ReplaceSyntheticFiles(st.files); got != st.wantChanged {
			t.Errorf("%s: changed = %v, want %v", st.name, got, st.wantChanged)
		}
		var paths []string
		for _, f := range s.Files {
			paths = append(paths, f.Path)
		}
		if len(paths) != len(st.wantPaths) {
			t.Fatalf("%s: paths = %v, want %v", st.name, paths, st.wantPaths)
		}
		for i := range paths {
			if paths[i] != st.wantPaths[i] {
				t.Errorf("%s: paths = %v, want %v", st.name, paths, st.wantPaths)
				break
			}
		}
	}
	a := s.Files[1]
	if a.Content != "a2" || a.PreviousContent != "a1" || a.FileHash == hashA {
		t.Errorf("a = content %q previous %q", a.Content, a.PreviousContent)
	}
	if b := s.Files[2]; len(b.Comments) != 1 || b.Content != "b1" {
		t.Errorf("b lost its comments or content: %+v", b)
	}
	// A baseline (herdr: the pane as last read) replaces the previous sweep.
	base := "a0"
	if !s.ReplaceSyntheticFiles([]SyntheticFile{{Path: "n/d.md", Content: "d1"}, {Path: "m/a.md", Content: "a2", Previous: &base}}) {
		t.Errorf("a new baseline must count as a change")
	}
	if a := s.Files[1]; a.PreviousContent != "a0" || a.Content != "a2" {
		t.Errorf("baseline: previous %q content %q", a.PreviousContent, a.Content)
	}
	if s.ReplaceSyntheticFiles([]SyntheticFile{{Path: "n/d.md", Content: "d1"}, {Path: "m/a.md", Content: "a2", Previous: &base}}) {
		t.Errorf("same files and baseline: no change")
	}
}

func TestSyntheticSessionRecordsReviewType(t *testing.T) {
	dir := t.TempDir()
	reviewPath := filepath.Join(dir, "review")
	s, err := NewSyntheticSession("herdr", []string{"herdr"}, nil, dir, reviewPath)
	if err != nil {
		t.Fatal(err)
	}
	snap := s.snapshotForWrite(reviewPath)
	if snap.reviewType != "herdr" {
		t.Errorf("snapshot reviewType = %q", snap.reviewType)
	}
	if cj := buildCritJSON(snap); cj.ReviewType != "herdr" {
		t.Errorf("review.json review_type = %q, want herdr", cj.ReviewType)
	}
}
