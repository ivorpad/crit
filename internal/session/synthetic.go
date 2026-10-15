package session

import (
	"fmt"
	"os"
)

// SyntheticFile is a review file that exists only in memory: its content comes
// from the caller, never from disk. Used by herdr mode, where each file is one
// agent pane rendered as markdown.
type SyntheticFile struct {
	Path    string
	Content string
	// Previous, when set, is what the Document/Diff toggle compares Content with
	// (herdr: the pane as the owner last read it). Nil keeps the content the file
	// had before this replace.
	Previous *string
}

// NewSyntheticSession builds a files-mode session whose files are all synthetic
// markdown. repoRoot is what /files/ and the @-mention list would read from; pass
// an empty directory when there is no repository behind the review.
func NewSyntheticSession(reviewType string, cliArgs []string, files []SyntheticFile, repoRoot, reviewPath string) (*Session, error) {
	if reviewType == "" {
		return nil, fmt.Errorf("NewSyntheticSession: reviewType is empty")
	}
	if repoRoot == "" {
		repoRoot, _ = os.Getwd()
	}
	s := &Session{
		Mode:                "files",
		RepoRoot:            repoRoot,
		ReviewRound:         1,
		ReviewType:          reviewType,
		CLIArgs:             cliArgs,
		awaitingFirstReview: true,
		subscribers:         make(map[chan SSEEvent]struct{}),
		roundComplete:       make(chan struct{}, 1),
		Files:               make([]*FileEntry, 0, len(files)),
	}
	for _, f := range files {
		s.Files = append(s.Files, syntheticEntry(f))
	}
	if reviewPath != "" {
		s.ReviewFilePath = reviewPath
		s.loadCritJSON()
	}
	return s, nil
}

func syntheticEntry(f SyntheticFile) *FileEntry {
	fe := &FileEntry{
		Path:     f.Path,
		Status:   "modified",
		FileType: "markdown",
		Content:  f.Content,
		FileHash: fileHash([]byte(f.Content)),
		Comments: []Comment{},
	}
	if f.Previous != nil {
		fe.PreviousContent = *f.Previous
	}
	return fe
}

// ReplaceSyntheticFiles sets the session's files to files, in that order. A file
// that stays keeps its comments; when its content changes, the old content
// becomes PreviousContent (the Document/Diff toggle). A file that is gone is
// dropped unless it has comments, so a reply written to a pane that just closed
// is not lost. Reports whether anything changed.
func (s *Session) ReplaceSyntheticFiles(files []SyntheticFile) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := make(map[string]*FileEntry, len(s.Files))
	for _, f := range s.Files {
		old[f.Path] = f
	}
	changed := false
	next := make([]*FileEntry, 0, len(files))
	kept := make(map[string]bool, len(files))
	for _, f := range files {
		kept[f.Path] = true
		prev, ok := old[f.Path]
		if !ok {
			next = append(next, syntheticEntry(f))
			continue
		}
		if prev.Content != f.Content {
			prev.PreviousContent = prev.Content
			prev.Content = f.Content
			prev.FileHash = fileHash([]byte(f.Content))
			changed = true
		}
		if f.Previous != nil && prev.PreviousContent != *f.Previous {
			prev.PreviousContent = *f.Previous
			changed = true
		}
		next = append(next, prev)
	}
	for _, f := range s.Files {
		if !kept[f.Path] && len(f.Comments) > 0 {
			next = append(next, f)
		}
	}
	if len(next) != len(s.Files) {
		changed = true
	} else {
		for i := range next {
			if next[i] != s.Files[i] {
				changed = true
				break
			}
		}
	}
	s.Files = next
	return changed
}
