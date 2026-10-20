package herdr

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"
)

// The daemon keeps one gateway per remote machine running between sweeps, on
// one SSH session. DSM on the NAS logs every SSH session as a login, sessions
// multiplexed over a ControlMaster too (measured 2026-10-19: one master and
// three muxed commands made four rows), so starting the gateway for each call
// cost two logins per machine on every sweep.

const (
	// gatewayMaxAge is when a kept gateway is replaced, so a reinstalled
	// gateway is picked up within half an hour.
	gatewayMaxAge = 30 * time.Minute
	// gatewayIdle is how long a kept gateway may go unasked (sweeps are ~20 s
	// apart) before it is replaced instead: after the Mac sleeps, its sessions
	// are dead a minute before ssh notices. Measured on the wall clock, which
	// counts the sleep; the monotonic clock on macOS does not.
	gatewayIdle = time.Minute
)

// gatewaySessions holds the kept gateway of each remote machine, by SSH target.
type gatewaySessions struct {
	mu     sync.Mutex
	byHost map[string]*gatewaySession
}

func newGatewaySessions() *gatewaySessions {
	return &gatewaySessions{byHost: map[string]*gatewaySession{}}
}

// runner is run for the machine at target, with its gateway calls (argv from
// machine.command) sent to the kept gateway.
func (g *gatewaySessions) runner(target string, run runner) runner {
	g.mu.Lock()
	s := g.byHost[target]
	if s == nil {
		s = &gatewaySession{argv: keptGatewayArgv(target)}
		g.byHost[target] = s
	}
	g.mu.Unlock()
	return func(ctx context.Context, stdin []byte, argv []string) ([]byte, error) {
		if len(argv) == 0 || argv[len(argv)-1] != gatewayLauncher {
			return run(ctx, stdin, argv)
		}
		return s.call(ctx, stdin)
	}
}

// close ends every kept gateway. An exchange in flight ends with its sweep's
// context, which is the one that is done when drover stops.
func (g *gatewaySessions) close() {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, s := range g.byHost {
		s.mu.Lock()
		s.stop()
		s.mu.Unlock()
	}
}

// keptGatewayArgv is machine.command for a session that stays open: ssh checks
// the connection every 15 s and gives up after a minute without an answer.
func keptGatewayArgv(target string) []string {
	return []string{"ssh", "-T", "-o", "BatchMode=yes", "-o", "ConnectTimeout=8",
		"-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=4", target, gatewayLauncher}
}

// gatewaySession is one machine's kept gateway, one exchange at a time.
type gatewaySession struct {
	mu   sync.Mutex
	argv []string
	proc *gatewayProc // nil before the first call and after a failure
}

// call sends the request lines in stdin and returns the answer line to each.
// When a kept gateway fails before answering anything it is replaced and asked
// once more: its session may have ended since the last sweep (a dropped
// connection, a restarted sshd).
func (s *gatewaySession) call(ctx context.Context, stdin []byte) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var ids []string
	for _, line := range bytes.Split(stdin, []byte("\n")) {
		var req struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(line, &req) == nil && req.ID != "" {
			ids = append(ids, req.ID)
		}
	}
	for {
		kept := s.proc != nil && s.proc.alive() && time.Since(s.proc.started) < gatewayMaxAge &&
			time.Now().Round(0).Sub(s.proc.used) < gatewayIdle
		if !kept {
			s.stop()
			p, err := startGatewayProc(s.argv)
			if err != nil {
				return nil, err
			}
			s.proc = p
		}
		out, answered, err := s.proc.exchange(ctx, stdin, ids)
		if err == nil {
			s.proc.used = time.Now().Round(0)
			return out, nil
		}
		// The stream is at an unknown point now: never reuse it.
		s.stop()
		if !kept || answered > 0 || ctx.Err() != nil {
			return out, err
		}
	}
}

func (s *gatewaySession) stop() {
	if s.proc != nil {
		s.proc.end()
		s.proc = nil
	}
}

// gatewayProc is one gateway process on its SSH session.
type gatewayProc struct {
	cmd     *exec.Cmd
	in      io.WriteCloser
	outFile *os.File
	out     *bufio.Reader
	stderr  *tailBuffer
	exited  chan struct{}
	started time.Time
	used    time.Time // wall clock only (Round(0)): the last answer, or the start
}

func startGatewayProc(argv []string) (*gatewayProc, error) {
	cmd := exec.Command(argv[0], argv[1:]...)
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	// An os.Pipe rather than StdoutPipe: Wait, in its own goroutine, closes a
	// StdoutPipe as the process exits, under a read in progress.
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	p := &gatewayProc{cmd: cmd, in: in, outFile: r, out: bufio.NewReader(r), stderr: &tailBuffer{}, exited: make(chan struct{}), started: now, used: now.Round(0)}
	cmd.Stdout, cmd.Stderr = w, p.stderr
	err = cmd.Start()
	_ = w.Close()
	if err != nil {
		_ = r.Close()
		return nil, err
	}
	go func() {
		_ = cmd.Wait()
		close(p.exited)
	}()
	return p, nil
}

// exchange writes the request lines in, then reads until every id in ids is
// answered, skipping lines that answer none of them (anything a login shell
// prints). answered is how many came back before an error. A done ctx kills
// the process, which ends a read that is waiting.
func (p *gatewayProc) exchange(ctx context.Context, in []byte, ids []string) ([]byte, int, error) {
	defer context.AfterFunc(ctx, p.kill)()
	if _, err := p.in.Write(in); err != nil {
		return nil, 0, p.failed(ctx, err)
	}
	var out []byte
	pending := slices.Clone(ids)
	for len(pending) > 0 {
		line, err := p.out.ReadBytes('\n')
		if err != nil {
			return out, len(ids) - len(pending), p.failed(ctx, err)
		}
		var resp struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(line, &resp) != nil {
			continue
		}
		if i := slices.Index(pending, resp.ID); i >= 0 {
			out = append(out, line...)
			pending = slices.Delete(pending, i, i+1)
		}
	}
	return out, len(ids), nil
}

// failed is err as execRunner would put it, with what ssh printed last.
func (p *gatewayProc) failed(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return fmt.Errorf("ssh: %w", ctx.Err())
	}
	// ssh writes why it ended as it exits.
	select {
	case <-p.exited:
	case <-time.After(time.Second):
	}
	if msg := p.stderr.String(); msg != "" {
		return fmt.Errorf("ssh: %w: %s", err, msg)
	}
	return fmt.Errorf("ssh: %w", err)
}

func (p *gatewayProc) alive() bool {
	select {
	case <-p.exited:
		return false
	default:
		return true
	}
}

func (p *gatewayProc) kill() { _ = p.cmd.Process.Kill() }

// end closes the gateway's stdin, which ends it, and kills its ssh if it is
// still there 5 s later.
func (p *gatewayProc) end() {
	_ = p.in.Close()
	go func() {
		select {
		case <-p.exited:
		case <-time.After(5 * time.Second):
			p.kill()
			<-p.exited
		}
		_ = p.outFile.Close()
	}()
}

// tailBuffer keeps the last 300 bytes written to it.
type tailBuffer struct {
	mu sync.Mutex
	b  []byte
}

func (t *tailBuffer) Write(b []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.b = append(t.b, b...)
	if len(t.b) > 300 {
		t.b = append([]byte(nil), t.b[len(t.b)-300:]...)
	}
	return len(b), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.TrimSpace(string(t.b))
}
