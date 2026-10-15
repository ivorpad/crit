package main

import (
	"context"
	"errors"

	"github.com/tomasz-tomczyk/crit/internal/herdr"
)

// herdrProvider adapts the herdr controller to server.HerdrProvider, which keeps
// the server free of the herdr package's error types.
type herdrProvider struct{ *herdr.Controller }

func (p herdrProvider) Focus(ctx context.Context, path string) (string, string, bool, error) {
	machine, paneID, err := p.Controller.Focus(ctx, path)
	return machine, paneID, errors.Is(err, herdr.ErrNoPane), err
}
