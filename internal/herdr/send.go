package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tomasz-tomczyk/crit/internal/session"
)

// sendRequest is the body of POST /api/herdr/send: a pane (or its plan file), the
// state_change_seq the page last saw for it, and exactly one of text, keys or
// option.
type sendRequest struct {
	Path      string   `json:"path"`
	ExpectSeq *int64   `json:"expect_state_change_seq"`
	Text      string   `json:"text"`
	Keys      []string `json:"keys"`
	Option    *int     `json:"option"`
}

// Delivery is what the controller remembers about the last send to a pane, in
// memory only. Acknowledged turns true when a sweep sees state_change_seq move
// past SeqBefore: the agent took the input.
type Delivery struct {
	At           string `json:"at"`
	SeqBefore    int64  `json:"seq_before"`
	Kind         string `json:"kind"` // text or keys
	Acknowledged bool   `json:"acknowledged"`
}

// refusal is a send that was not made, with the HTTP status the handler answers.
type refusal struct {
	status int
	body   map[string]any
}

func (r *refusal) Error() string { return fmt.Sprint(r.body["error"]) }

func refuse(status int, msg string) *refusal {
	return &refusal{status, map[string]any{"error": msg}}
}

// keyGap is the pause between two send-keys calls: keys sent in a burst are
// dropped by the agent CLIs.
const keyGap = 150 * time.Millisecond

// SendJSON delivers one reply to one pane and answers as HTTP: status and body.
// It reads the pane fresh (`herdr agent get`) before sending anything: a
// different state_change_seq than the page expected is 409 stale, a dialog up
// refuses text (409 blocked), a working agent refuses keys (409 working).
func (c *Controller) SendJSON(ctx context.Context, body []byte) (int, []byte) {
	var req sendRequest
	if err := json.Unmarshal(body, &req); err != nil || req.Path == "" || req.ExpectSeq == nil {
		return marshalStatus(refuse(http.StatusBadRequest, "body must be {\"path\", \"expect_state_change_seq\", and one of \"text\", \"keys\", \"option\"}"))
	}
	res, err := c.send(ctx, req)
	if err != nil {
		var r *refusal
		if errors.As(err, &r) {
			return marshalStatus(r)
		}
		return marshalStatus(refuse(http.StatusBadGateway, err.Error()))
	}
	return marshalStatus(&refusal{http.StatusOK, res})
}

// KeysJSON is GET /api/herdr/keys: the keys SendJSON would press for option n on
// the pane's dialog, so the page can show them before the owner confirms.
func (c *Controller) KeysJSON(path string, option int) (int, []byte) {
	c.mu.Lock()
	it, ok := c.itemAt(path)
	c.mu.Unlock()
	if !ok {
		return marshalStatus(refuse(http.StatusNotFound, ErrNoPane.Error()))
	}
	keys, err := OptionKeys(it, option)
	if err != nil {
		return marshalStatus(refuse(http.StatusBadRequest, err.Error()))
	}
	return marshalStatus(&refusal{http.StatusOK, map[string]any{
		"path": path, "machine": it.Machine, "pane_id": it.PaneID, "option": option, "keys": keys,
	}})
}

func marshalStatus(r *refusal) (int, []byte) {
	data, _ := json.Marshal(r.body)
	return r.status, data
}

// itemAt finds the pane at path (a pane file or its plan file) in the latest
// sweep. The caller holds c.mu.
func (c *Controller) itemAt(path string) (Item, bool) {
	if c.snap == nil {
		return Item{}, false
	}
	for _, it := range c.snap.Items {
		if itemPath(it) == path || (hasPlanFile(it) && planFilePath(it) == path) {
			return it, true
		}
	}
	return Item{}, false
}

func (c *Controller) send(ctx context.Context, req sendRequest) (map[string]any, error) {
	c.mu.Lock()
	it, ok := c.itemAt(req.Path)
	run, sess := c.col.run, c.sess
	c.mu.Unlock()
	if !ok {
		return nil, refuse(http.StatusNotFound, ErrNoPane.Error())
	}
	if !paneIDRE.MatchString(it.PaneID) {
		return nil, refuse(http.StatusBadRequest, fmt.Sprintf("unsafe pane id %q", it.PaneID))
	}
	kind, keys, err := resolveInput(it, req)
	if err != nil {
		return nil, err
	}
	m := machine{Label: it.Machine}
	if it.Machine != localLabel {
		m.Target = it.Machine
	}
	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// The sweep is up to 15 s old: read the pane now, from herdr itself.
	fresh, err := agentGet(callCtx, run, m, it.PaneID)
	if err != nil {
		return nil, err
	}
	if err := checkFresh(fresh, *req.ExpectSeq, kind); err != nil {
		return nil, err
	}
	seqBefore := *fresh.StateChangeSeq
	if err := c.press(callCtx, run, m, it.PaneID, kind, req.Text, keys); err != nil {
		return nil, err
	}

	at := c.col.now().UTC().Format(time.RFC3339)
	c.mu.Lock()
	c.sent[itemPath(it)] = Delivery{At: at, SeqBefore: seqBefore, Kind: kind}
	c.mu.Unlock()
	c.logSent(map[string]any{
		"time": at, "path": req.Path, "machine": it.Machine, "pane": it.PaneID,
		"kind": kind, "text": req.Text, "keys": keys, "seq_before": seqBefore,
	})
	if sess != nil {
		sess.Notify(session.SSEEvent{Type: UpdatedEvent})
	}
	select {
	case c.kick <- struct{}{}:
	default:
	}
	return map[string]any{
		"path": req.Path, "machine": it.Machine, "pane_id": it.PaneID,
		"sent": kind, "state_change_seq_before": seqBefore,
	}, nil
}

// checkFresh refuses a send the pane's state, read just now, no longer fits:
// a moved seq is stale, a dialog refuses text, a working agent refuses keys.
func checkFresh(fresh *herdrAgent, expect int64, kind string) error {
	if fresh.StateChangeSeq == nil || *fresh.StateChangeSeq != expect {
		return &refusal{http.StatusConflict, map[string]any{"error": "stale", "state_change_seq": fresh.StateChangeSeq}}
	}
	switch {
	case kind == "text" && fresh.AgentStatus == "blocked":
		return refuse(http.StatusConflict, "blocked")
	case kind == "keys" && fresh.AgentStatus == "working":
		return refuse(http.StatusConflict, "working")
	}
	return nil
}

// press types the input into the pane: text as one `agent prompt`, keys as
// one `agent send-keys` call each, keyGap apart.
func (c *Controller) press(ctx context.Context, run runner, m machine, paneID, kind, text string, keys []string) error {
	if kind == "text" {
		argv, err := herdrArgv(m, "agent", "prompt", paneID, text)
		if err != nil {
			return err
		}
		_, err = run(ctx, nil, argv)
		return err
	}
	for i, k := range keys {
		if i > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(c.keyGap):
			}
		}
		argv, err := herdrArgv(m, "agent", "send-keys", paneID, k)
		if err != nil {
			return err
		}
		if _, err := run(ctx, nil, argv); err != nil {
			return fmt.Errorf("key %d of %d (%s): %w", i+1, len(keys), k, err)
		}
	}
	return nil
}

// resolveInput picks the one input the request carries and validates it. An
// option is turned into keys through OptionKeys.
func resolveInput(it Item, req sendRequest) (kind string, keys []string, err error) {
	given := 0
	for _, has := range []bool{req.Text != "", len(req.Keys) > 0, req.Option != nil} {
		if has {
			given++
		}
	}
	if given != 1 {
		return "", nil, refuse(http.StatusBadRequest, "send exactly one of text, keys or option")
	}
	switch {
	case req.Text != "":
		// The text is one argv element, never a shell string. herdr takes "- a
		// list" or "-x" as the text, but a bare flag word such as "--wait"
		// would be read as the flag, and herdr has no `--`.
		if strings.HasPrefix(req.Text, "-") && !strings.ContainsAny(req.Text, " \t\n") {
			return "", nil, refuse(http.StatusBadRequest, "a one-word text starting with '-' would be read as a herdr flag")
		}
		return "text", nil, nil
	case len(req.Keys) > 0:
		if err := validateKeys(req.Keys); err != nil {
			return "", nil, refuse(http.StatusBadRequest, err.Error())
		}
		return "keys", req.Keys, nil
	default:
		keys, err := OptionKeys(it, *req.Option)
		if err != nil {
			return "", nil, refuse(http.StatusBadRequest, err.Error())
		}
		return "keys", keys, nil
	}
}

// agentGet is one `herdr [--machine X] agent get <pane>`: status and seqs as of
// now. A pane herdr no longer lists is 404.
func agentGet(ctx context.Context, run runner, m machine, paneID string) (*herdrAgent, error) {
	argv, err := herdrArgv(m, "agent", "get", paneID)
	if err != nil {
		return nil, err
	}
	out, runErr := run(ctx, nil, argv)
	var body struct {
		Result struct {
			Agent *herdrAgent `json:"agent"`
		} `json:"result"`
		Error *struct {
			Code, Message string
		} `json:"error"`
	}
	if jsonErr := json.Unmarshal(out, &body); jsonErr == nil && body.Error != nil {
		if body.Error.Code == "agent_not_found" {
			return nil, refuse(http.StatusNotFound, body.Error.Message)
		}
		return nil, fmt.Errorf("herdr agent get: %s: %s", body.Error.Code, body.Error.Message)
	}
	if runErr != nil {
		return nil, runErr
	}
	if body.Result.Agent == nil {
		return nil, errors.New("herdr agent get: no agent in the answer")
	}
	return body.Result.Agent, nil
}

// logSent appends one line to herdr-sent.jsonl next to the read marks.
func (c *Controller) logSent(entry map[string]any) {
	if c.opts.ReadStatePath == "" {
		return
	}
	path := filepath.Join(filepath.Dir(c.opts.ReadStatePath), "herdr-sent.jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	line, _ := json.Marshal(entry)
	_, _ = f.Write(append(line, '\n'))
}

// deliveryFor is the pane's last send with its acknowledgement computed from
// the item in the latest sweep. The caller holds c.mu.
func (c *Controller) deliveryFor(it Item) *Delivery {
	d, ok := c.sent[itemPath(it)]
	if !ok {
		return nil
	}
	d.Acknowledged = it.StateChangeSeq != nil && *it.StateChangeSeq > d.SeqBefore
	return &d
}
