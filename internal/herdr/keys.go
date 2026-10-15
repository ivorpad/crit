package herdr

import (
	"errors"
	"fmt"
	"strconv"
)

// allowedKeys are the key names POST /api/herdr/send passes to
// `herdr agent send-keys`: the names the lab sent (herdr-tui-control.md §1)
// minus ctrl+c, which exits Codex (§4). Anything else is a 400.
var allowedKeys = map[string]bool{
	"enter": true, "esc": true, "up": true, "down": true, "left": true, "right": true,
	"tab": true, "shift+tab": true, "shift+left": true, "shift+right": true,
	"space": true, "backspace": true, "ctrl+]": true, "y": true, "n": true, "p": true,
	"1": true, "2": true, "3": true, "4": true, "5": true, "6": true, "7": true, "8": true, "9": true,
}

// validateKeys refuses an empty list and any key send-keys was not seen with.
func validateKeys(keys []string) error {
	if len(keys) == 0 {
		return errors.New("keys is empty")
	}
	for _, k := range keys {
		if !allowedKeys[k] {
			return fmt.Errorf("key %q is not allowed", k)
		}
	}
	return nil
}

// OptionKeys is the key sequence that picks option n on the pane's dialog, per
// agent and dialog kind, from herdr-tui-control.md §4. An option the table does
// not cover is an error, so nothing is pressed on a guess.
func OptionKeys(it Item, n int) ([]string, error) {
	d := it.Dialog
	if d == nil {
		return nil, errors.New("the pane has no parsed dialog")
	}
	var opt *Choice
	for i := range d.Options {
		if d.Options[i].N == n {
			opt = &d.Options[i]
		}
	}
	if opt == nil {
		return nil, fmt.Errorf("the dialog has no option %d", n)
	}
	if opt.FreeText {
		return nil, fmt.Errorf("option %d takes free text (herdr pane send-text), which is not supported yet", n)
	}
	switch it.Agent {
	case "claude":
		return claudeKeys(d, n)
	case "codex":
		return codexKeys(d, n)
	case "opencode":
		return opencodeKeys(d, n)
	case "pi":
		return nil, errors.New("pi asks in prose: answer with text")
	}
	return nil, fmt.Errorf("no key map for agent %q", it.Agent)
}

func digit(n int) (string, error) {
	if n < 1 || n > 9 {
		return "", fmt.Errorf("option %d has no digit key", n)
	}
	return strconv.Itoa(n), nil
}

// cursorMoves walks a menu without number keys (trust dialogs, opencode's
// permission buttons) from the option the cursor is on to option n. The cursor
// starts on option 1 when the gateway marks none as current.
func cursorMoves(d *Dialog, n int, fwd, back string) []string {
	cur := 1
	for _, c := range d.Options {
		if c.Current {
			cur = c.N
		}
	}
	var keys []string
	for ; cur < n; cur++ {
		keys = append(keys, fwd)
	}
	for ; cur > n; cur-- {
		keys = append(keys, back)
	}
	return append(keys, "enter")
}

// claudeKeys: numbered menus (AskUserQuestion, permissions, ExitPlanMode) take
// `<n>`, `enter`. The trust dialog has no numbers: `down`, `enter` (§4).
// Multi-select is the one sequence the lab did not run end to end as written:
// `<n>` toggles the option, the cursor stays on option 1, Submit sits one row
// under the last option, then the review screen takes a second `enter`.
func claudeKeys(d *Dialog, n int) ([]string, error) {
	if d.Kind == "trust" {
		return cursorMoves(d, n, "down", "up"), nil
	}
	k, err := digit(n)
	if err != nil {
		return nil, err
	}
	if d.Multi {
		keys := []string{k}
		for range d.Options {
			keys = append(keys, "down")
		}
		return append(keys, "enter", "enter"), nil
	}
	return []string{k, "enter"}, nil
}

// codexKeys: request_user_input needs `shift+left` to open the answer form
// first; a command approval answers with its letter (y, p, esc); the trust
// dialog with `enter`.
func codexKeys(d *Dialog, n int) ([]string, error) {
	switch d.Kind {
	case "question":
		k, err := digit(n)
		if err != nil {
			return nil, err
		}
		return []string{"shift+left", k, "enter"}, nil
	case "permission":
		switch n {
		case 1:
			return []string{"y"}, nil
		case 2:
			return []string{"p"}, nil
		case 3:
			return []string{"esc"}, nil
		}
		return nil, fmt.Errorf("codex approval has no option %d", n)
	case "trust":
		return cursorMoves(d, n, "down", "up"), nil
	}
	return nil, fmt.Errorf("no key map for codex %s dialogs", d.Kind)
}

// opencodeKeys: the question tool takes `<n>`, `enter`; the permission prompt
// is a row of buttons walked with `right` (Allow once, Allow always, Reject).
func opencodeKeys(d *Dialog, n int) ([]string, error) {
	switch d.Kind {
	case "question":
		k, err := digit(n)
		if err != nil {
			return nil, err
		}
		return []string{k, "enter"}, nil
	case "permission":
		return cursorMoves(d, n, "right", "left"), nil
	}
	return nil, fmt.Errorf("no key map for opencode %s dialogs", d.Kind)
}
