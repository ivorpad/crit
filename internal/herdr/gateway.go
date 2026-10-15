package herdr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// runner executes argv with stdin and returns stdout. Tests replace it.
type runner func(ctx context.Context, stdin []byte, argv []string) (stdout []byte, err error)

func execRunner(ctx context.Context, stdin []byte, argv []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdin = bytes.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		return stdout.Bytes(), fmt.Errorf("%s: %w", argv[0], ctx.Err())
	}
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 300 {
			msg = msg[len(msg)-300:]
		}
		return stdout.Bytes(), fmt.Errorf("%s: %w: %s", argv[0], err, msg)
	}
	return stdout.Bytes(), nil
}

// gatewayRequest is one line of the WorkDone gateway protocol
// (gateway/herdr-gateway.ts): {"id","op","params"} in, {"id","ok","result"|"error"} out.
type gatewayRequest struct {
	ID     string         `json:"id"`
	Op     string         `json:"op"`
	Params map[string]any `json:"params"`
}

type gatewayResponse struct {
	ID     string          `json:"id"`
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// gatewayError is an error the gateway itself returned for one op.
type gatewayError struct{ Code, Message string }

func (e *gatewayError) Error() string { return e.Code + ": " + e.Message }

// readOnlyOps are the only gateway ops the collector may send. Everything else in
// the gateway can type into a pane, approve a menu, or write state.
var readOnlyOps = map[string]bool{
	"bridge_status":    true,
	"console_snapshot": true,
	"read_agent":       true,
	"read_file":        true,
	"read_pane":        true,
}

// callGateway starts the machine's gateway once and sends every request on that
// one process (one SSH connection for a remote machine). Results come back in
// request order; a per-request failure is returned in errs, not as err.
func callGateway(ctx context.Context, run runner, m machine, reqs []gatewayRequest) (results []json.RawMessage, errs []error, err error) {
	var in bytes.Buffer
	for _, r := range reqs {
		if !readOnlyOps[r.Op] {
			return nil, nil, fmt.Errorf("refusing gateway op %q: the collector is read-only", r.Op)
		}
		line, mErr := json.Marshal(r)
		if mErr != nil {
			return nil, nil, mErr
		}
		in.Write(line)
		in.WriteByte('\n')
	}
	out, runErr := run(ctx, in.Bytes(), m.command())
	byID := map[string]gatewayResponse{}
	for _, line := range bytes.Split(out, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var resp gatewayResponse
		if json.Unmarshal(line, &resp) == nil {
			byID[resp.ID] = resp
		}
	}
	if len(byID) == 0 {
		if runErr == nil {
			runErr = errors.New("gateway returned no response")
		}
		return nil, nil, runErr
	}
	results = make([]json.RawMessage, len(reqs))
	errs = make([]error, len(reqs))
	for i, r := range reqs {
		resp, ok := byID[r.ID]
		switch {
		case !ok:
			errs[i] = fmt.Errorf("no response to %s", r.Op)
		case !resp.OK && resp.Error != nil:
			errs[i] = &gatewayError{Code: resp.Error.Code, Message: resp.Error.Message}
		case !resp.OK:
			errs[i] = fmt.Errorf("%s failed", r.Op)
		default:
			results[i] = resp.Result
		}
	}
	return results, errs, nil
}
