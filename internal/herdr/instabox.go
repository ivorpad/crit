package herdr

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"time"
)

// boxState is instabox's view of one box: "running", "suspended", "stopped", ...
type boxState struct {
	State           string `json:"state"`
	AutoSuspendedAt string `json:"autoSuspendedAt,omitempty"`
}

// paused reports whether calls to the box would hang on SSH. herdr waits 11 to 70 s
// on a suspended box before failing, so the collector does not contact one.
func (b boxState) paused() bool { return b.State == "suspended" || b.State == "paused" }

// listBoxes asks the local instabox CLI for every box's state, keyed by name.
func listBoxes(ctx context.Context, run runner) (map[string]boxState, error) {
	out, err := run(ctx, nil, []string{"instabox", "ls", "--json"})
	if err != nil {
		return nil, err
	}
	var list []struct {
		Name string `json:"name"`
		boxState
	}
	if err := json.Unmarshal(out, &list); err != nil {
		return nil, fmt.Errorf("parsing instabox ls: %w", err)
	}
	boxes := make(map[string]boxState, len(list))
	for _, b := range list {
		boxes[b.Name] = b.boxState
	}
	return boxes, nil
}

// boxNameRE matches what instabox accepts as a name; anything else never reaches argv.
var boxNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

// wakeReachTimeout is how long a resumed box gets to answer its gateway. The lab saw
// herdr reachable 11 s after `instabox resume`; WorkDone's wake_machine allows 45 s.
const wakeReachTimeout = 45 * time.Second

// wakeBox resumes a suspended box, then polls its gateway's bridge_status until it
// answers or wakeReachTimeout passes. A stopped box is not started: a stop ended
// every process on it, so starting it is the owner's call.
func wakeBox(ctx context.Context, run runner, m machine) (reachable bool, waited time.Duration, err error) {
	if !boxNameRE.MatchString(m.Box) {
		return false, 0, fmt.Errorf("bad instabox name %q", m.Box)
	}
	if _, err := run(ctx, nil, []string{"instabox", "resume", m.Box}); err != nil {
		return false, 0, err
	}
	start := time.Now()
	for {
		callCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		_, errs, err := callGateway(callCtx, run, m, []gatewayRequest{{ID: "wake", Op: "bridge_status", Params: map[string]any{}}})
		cancel()
		if err == nil && errs[0] == nil {
			return true, time.Since(start), nil
		}
		if time.Since(start) >= wakeReachTimeout {
			return false, time.Since(start), nil
		}
		select {
		case <-ctx.Done():
			return false, time.Since(start), ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
}
