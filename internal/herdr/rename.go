package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// agentNameRE is herdr's own rule for agent names. herdr refuses any other
// name, so it is checked here and refused with herdr's words as a 400.
var agentNameRE = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

var errAgentName = errors.New("agent name must start with a lowercase letter and contain only lowercase letters, digits, '-' or '_' (1-32 characters)")

// renameRequest is the body of POST /api/herdr/rename: a new herdr agent name
// for the pane, or clear to drop it.
type renameRequest struct {
	Path  string  `json:"path"`
	Name  *string `json:"name"`
	Clear bool    `json:"clear"`
}

// RenameJSON is POST /api/herdr/rename: `herdr [--machine X] agent rename
// <pane> <name>` (or --clear), answered as HTTP status and body. The name shows
// on the item from the next sweep.
func (c *Controller) RenameJSON(ctx context.Context, body []byte) (int, []byte) {
	var req renameRequest
	if err := json.Unmarshal(body, &req); err != nil || req.Path == "" || (req.Name != nil) == req.Clear {
		return marshalStatus(refuse(http.StatusBadRequest, `body must be {"path", "name"} or {"path", "clear": true}`))
	}
	name := ""
	if req.Name != nil {
		var err error
		if name, err = agentName(*req.Name); err != nil {
			return marshalStatus(refuse(http.StatusBadRequest, err.Error()))
		}
	}
	c.mu.Lock()
	it, ok := c.itemAt(req.Path)
	run := c.col.run
	c.mu.Unlock()
	if !ok {
		return marshalStatus(refuse(http.StatusNotFound, ErrNoPane.Error()))
	}
	if !paneIDRE.MatchString(it.PaneID) {
		return marshalStatus(refuse(http.StatusBadRequest, fmt.Sprintf("unsafe pane id %q", it.PaneID)))
	}
	m := machine{Label: it.Machine}
	if it.Machine != localLabel {
		m.Target = it.Machine // any non-empty target makes herdrArgv use --machine <label>
	}
	arg := name
	if req.Clear {
		arg = "--clear"
	}
	argv, err := herdrArgv(m, "agent", "rename", it.PaneID, arg)
	if err != nil {
		return marshalStatus(refuse(http.StatusBadGateway, err.Error()))
	}
	callCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if _, err := run(callCtx, nil, argv); err != nil {
		return marshalStatus(refuse(http.StatusBadGateway, err.Error()))
	}
	return marshalStatus(&refusal{http.StatusOK, map[string]any{
		"path": req.Path, "machine": it.Machine, "pane_id": it.PaneID, "name": name,
	}})
}

// agentName trims a new agent name and checks it against herdr's rule. A name
// that passes cannot start with "-", so herdr never reads it as a flag.
func agentName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if !agentNameRE.MatchString(name) {
		return "", errAgentName
	}
	return name, nil
}
