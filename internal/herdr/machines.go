package herdr

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// localLabel names the Mac's own herdr server. herdr calls it "Local"; it has no
// saved profile and no SSH target.
const localLabel = "local"

// machine is one herdr server the collector polls: the local one, or a saved SSH
// profile from herdr's endpoint catalog.
type machine struct {
	Label  string // herdr label, e.g. "workbox5"
	Target string // SSH target from the profile, e.g. "workdone-ib5"; empty for local
	Box    string // instabox name, e.g. "ib5"; empty when the machine is not a box
}

// endpointsPath is where herdr keeps saved SSH machines (`herdr machine list`
// reads the same file). Reading it avoids shelling out to the herdr CLI.
func endpointsPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "herdr", "client", "endpoints.json")
}

// loadMachines returns the local machine followed by every enabled saved profile.
func loadMachines(path string) ([]machine, error) {
	out := []machine{{Label: localLabel}}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	var catalog struct {
		SSH []struct {
			Label   string `json:"label"`
			Target  string `json:"target"`
			Enabled bool   `json:"enabled"`
		} `json:"ssh"`
	}
	if err := json.Unmarshal(data, &catalog); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	for _, p := range catalog.SSH {
		if !p.Enabled || p.Target == "" || strings.HasPrefix(p.Target, "-") {
			continue
		}
		out = append(out, machine{Label: p.Label, Target: p.Target})
	}
	return out, nil
}

// assignBoxes maps each machine to its instabox name. An explicit override (by herdr
// label) wins; otherwise the SSH target with the "workdone-" prefix stripped is used
// when instabox lists a box of that name (workdone-ib5 -> ib5). ib1 is named
// box-059ed8 on instabox, which no rule can infer, so it needs the override.
func assignBoxes(machines []machine, boxes map[string]boxState, overrides map[string]string) {
	for i := range machines {
		m := &machines[i]
		if b, ok := overrides[m.Label]; ok {
			m.Box = b
			continue
		}
		if m.Target == "" {
			continue
		}
		name := strings.TrimPrefix(m.Target, "workdone-")
		if _, ok := boxes[name]; ok {
			m.Box = name
		}
	}
}

// gatewayLauncher is where scripts/install-gateway.sh puts the WorkDone gateway on
// every machine. The remote shell expands the tilde.
const gatewayLauncher = "~/.local/libexec/herdr-chatgpt/herdr-gateway-launcher.sh"

// command returns the argv that starts this machine's gateway on stdin/stdout.
func (m machine) command() []string {
	if m.Target == "" {
		home, _ := os.UserHomeDir()
		return []string{filepath.Join(home, strings.TrimPrefix(gatewayLauncher, "~/"))}
	}
	return []string{"ssh", "-T", "-o", "BatchMode=yes", "-o", "ConnectTimeout=8", m.Target, gatewayLauncher}
}
