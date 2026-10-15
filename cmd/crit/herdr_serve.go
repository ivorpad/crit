package main

import (
	"context"
	"os"
	"path/filepath"

	"github.com/tomasz-tomczyk/crit/internal/herdr"
	"github.com/tomasz-tomczyk/crit/internal/server"
	"github.com/tomasz-tomczyk/crit/internal/session"
)

// herdrSession sweeps every herdr machine once and builds the review session from
// it: one markdown file per agent pane. The returned controller keeps it current.
func herdrSession(ctx context.Context, sc *server.DaemonCLIConfig) (*Session, *herdr.Controller, error) {
	ctrl := herdr.NewController(herdr.Options{
		Overrides:     sc.Cfg.HerdrInstaboxBoxes,
		ReadStatePath: filepath.Join(sc.ReviewPath, "herdr-read.json"),
	})
	files, err := ctrl.Sweep(ctx)
	if err != nil {
		return nil, nil, err
	}
	// No repository is behind these files: /files/ and the @-mention list read
	// from RepoRoot, so point it at an empty directory.
	root := filepath.Join(sc.ReviewPath, "herdr-root")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, nil, err
	}
	sess, err := session.NewSyntheticSession("herdr", []string{"herdr"}, files, root, sc.ReviewPath)
	if err != nil {
		return nil, nil, err
	}
	return sess, ctrl, nil
}
