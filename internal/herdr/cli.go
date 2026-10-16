// Package herdr is the read-only collector for herdr-control-center: it lists every
// agent pane on every herdr machine through the WorkDone gateway each machine runs,
// with the dialog or question that needs the owner. It never sends anything to a pane.
package herdr

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/tomasz-tomczyk/crit/internal/browser"
	"github.com/tomasz-tomczyk/crit/internal/clicmd"
	"github.com/tomasz-tomczyk/crit/internal/config"
	"github.com/tomasz-tomczyk/crit/internal/daemon"
)

// RunHerdr is `crit herdr`: the review page with one file per agent pane, served
// by a single daemon for every machine. --print and --json do one sweep instead.
func RunHerdr(args []string) {
	fs := flag.NewFlagSet("herdr", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "Print one sweep as JSON and exit")
	asText := fs.Bool("print", false, "Print one sweep as text and exit")
	wake := fs.Bool("wake", false, "With --print or --json: resume paused instabox boxes first")
	timeout := fs.Duration("timeout", 30*time.Second, "With --print or --json: per-machine call timeout")
	noOpen := fs.Bool("no-open", false, "Don't open a browser")
	port := fs.Int("port", 0, "Port to listen on")
	fs.IntVar(port, "p", 0, "Port (shorthand)")
	quiet := fs.Bool("quiet", false, "Suppress status lines")
	fs.BoolVar(quiet, "q", false, "Suppress status lines (shorthand)")
	if err := fs.Parse(args); err != nil {
		clicmd.Exit(clicmd.Usage(err.Error()))
	}
	if fs.NArg() > 0 {
		clicmd.Exit(clicmd.Usage("drover herdr takes no arguments"))
	}
	if *wake && !*asJSON && !*asText {
		clicmd.Exit(clicmd.Usage("--wake goes with --print or --json; the review page never wakes boxes"))
	}
	cwd, err := daemon.ResolvedCWD()
	if err != nil {
		clicmd.Exitf("Error: %v", err)
	}
	cfg := config.LoadConfig(cwd)
	if *asJSON || *asText {
		printOnce(cfg, *wake, *timeout, *asJSON)
		return
	}

	openControlCenter(cfg, *port, *noOpen || cfg.NoOpen, *quiet || cfg.Quiet)
}

// openControlCenter attaches to the herdr daemon, or starts it, and stays until
// Ctrl+C, which stops the daemon. The page is /herdr; a daemon it starts opens
// that page itself.
func openControlCenter(cfg config.Config, port int, noOpenResolved, quietResolved bool) {
	key := daemon.HerdrSessionKey()
	if entry, alive := daemon.FindAliveSession(key); alive {
		if !quietResolved {
			fmt.Fprintf(os.Stderr, "[drover] herdr control center already running: %s\n", pageURL(entry))
		}
		if !noOpenResolved && !daemon.DaemonHasBrowser(entry) {
			go browser.OpenBrowserWithCommand(pageURL(entry), cfg.OpenCmd)
		}
		daemon.RunReviewClient(entry, key, quietResolved)
		return
	}
	if !quietResolved {
		fmt.Fprintln(os.Stderr, "[drover] reading every herdr machine (a few seconds)...")
	}
	daemonArgs := daemon.AppendCommonDaemonFlags([]string{"--herdr"}, daemon.CommonDaemonFlags{
		Port:   config.ResolvePort(port, cfg.Port),
		Host:   config.ResolveHost("", cfg.Host),
		NoOpen: noOpenResolved,
		Quiet:  quietResolved,
	})
	// The daemon runs from the home directory: it serves every machine, not the
	// repository the command happened to be typed in.
	home, err := os.UserHomeDir()
	if err != nil {
		clicmd.Exitf("Error: %v", err)
	}
	entry, err := daemon.StartDaemonInDir(key, daemonArgs, home)
	if err != nil {
		clicmd.Exitf("Error: could not start the herdr daemon: %v", err)
	}
	if !quietResolved {
		fmt.Fprintf(os.Stderr, "[drover] herdr control center: %s (Ctrl+C stops it)\n", pageURL(entry))
	}
	installDaemonSignalHandler(entry.PID)
	daemon.RunReviewClient(entry, key, quietResolved)
}

// pageURL is the control center page of a running herdr daemon.
func pageURL(entry daemon.SessionEntry) string {
	return daemon.AdvertisedURL(entry.PublicURL, entry.Host, entry.Port, "/herdr")
}

func printOnce(cfg config.Config, wake bool, timeout time.Duration, asJSON bool) {
	snap, err := collect(context.Background(), collectOptions{
		endpoints:   endpointsPath(),
		overrides:   cfg.HerdrInstaboxBoxes,
		wake:        wake,
		callTimeout: timeout,
		run:         execRunner,
		now:         time.Now,
	})
	if err != nil {
		clicmd.Exitf("Error: %v", err)
	}
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		_ = enc.Encode(snap)
		return
	}
	printSnapshot(os.Stdout, snap)
}

func installDaemonSignalHandler(pid int) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		proc, err := os.FindProcess(pid)
		if err == nil {
			_ = daemon.TerminateProcess(proc)
		}
		os.Exit(0)
	}()
}

func printSnapshot(w io.Writer, s *Snapshot) {
	byMachine := map[string][]Item{}
	need := 0
	for _, it := range s.Items {
		byMachine[it.Machine] = append(byMachine[it.Machine], it)
		if it.NeedsYou {
			need++
		}
	}
	fmt.Fprintf(w, "%d of %d panes need you (%s)\n", need, len(s.Items), s.CollectedAt)
	if s.BoxesError != "" {
		fmt.Fprintf(w, "instabox: %s\n", s.BoxesError)
	}
	for _, m := range s.Machines {
		fmt.Fprintf(w, "\n%s\n", machineLine(m))
		for _, it := range byMachine[m.Label] {
			printItem(w, it)
		}
	}
}

func machineLine(m MachineStatus) string {
	name := m.Label
	if m.Box != "" {
		name += " (" + m.Box
		if m.BoxState != "" {
			name += ", " + m.BoxState
		}
		name += ")"
	}
	switch m.State {
	case "paused":
		return name + ": paused, not contacted; drover herdr --wake resumes it"
	case "reachable":
		woke := ""
		if m.Woken {
			woke = ", woken"
		}
		var line strings.Builder
		fmt.Fprintf(&line, "%s: %d agent pane%s, %.1fs%s", name, m.Agents, clicmd.Plural(m.Agents), float64(m.MS)/1000, woke)
		if m.ShellPanes+m.UninspectedPanes > 0 {
			fmt.Fprintf(&line, "; %d shell pane%s, %d not readable", m.ShellPanes, clicmd.Plural(m.ShellPanes), m.UninspectedPanes)
		}
		for _, e := range []string{m.DiscoveryError, m.GatewayError} {
			if e != "" {
				line.WriteString("\n  partial: " + e)
			}
		}
		return line.String()
	default:
		return fmt.Sprintf("%s: %s after %.1fs: %s", name, m.State, float64(m.MS)/1000, m.Error)
	}
}

func printItem(w io.Writer, it Item) {
	mark := " "
	if it.NeedsYou {
		mark = "*"
	}
	head := fmt.Sprintf("%s %-8s %-8s %-8s", mark, it.PaneID, it.Agent, it.Status)
	if it.Attention != "" {
		head += " " + it.Attention
	}
	if it.Name != "" {
		head += " [" + it.Name + "]"
	}
	if it.Title != "" {
		head += " " + it.Title
	}
	fmt.Fprintf(w, "  %s\n", head)
	if it.CWD != "" {
		fmt.Fprintf(w, "      cwd %s\n", it.CWD)
	}
	printNeed(w, it)
	printExtras(w, it)
}

// printNeed prints what the pane needs: its menu, or the answer that asks.
func printNeed(w io.Writer, it Item) {
	switch {
	case it.Dialog != nil:
		fmt.Fprintf(w, "      dialog (%s, id %.12s):\n", it.Dialog.Kind, it.Dialog.ID)
		writeIndented(w, it.Dialog.Text, 8, 0)
		for _, c := range it.Dialog.Options {
			extra := ""
			if c.FreeText {
				extra = "  (free text)"
			}
			fmt.Fprintf(w, "        [%d] %s%s\n", c.N, c.Label, extra)
		}
	case it.DialogScreen != "":
		fmt.Fprintf(w, "      blocked, menu not parsed; screen:\n")
		writeIndented(w, it.DialogScreen, 8, 0)
	case it.NeedsYou && it.Last != nil:
		trunc := ""
		if it.Last.Truncated {
			trunc = ", truncated"
		}
		fmt.Fprintf(w, "      last answer (%s%s):\n", it.Last.Source, trunc)
		writeIndented(w, it.Last.Text, 8, 12)
	}
}

func printExtras(w io.Writer, it Item) {
	if it.Plan != nil {
		if it.Plan.Error != "" {
			fmt.Fprintf(w, "      plan %s: not read: %s\n", it.Plan.Path, it.Plan.Error)
		} else {
			fmt.Fprintf(w, "      plan %s (%d chars)\n", it.Plan.Path, len(it.Plan.Text))
		}
	}
	if it.ResumeCommand != "" {
		fmt.Fprintf(w, "      resume: %s\n", it.ResumeCommand)
	}
	if it.InputLine != nil {
		fmt.Fprintf(w, "      input line (%s): %s\n", inputKind(it.InputLine), it.InputLine.Text)
	}
	if it.CompletionSeq != nil || it.StateChangeSeq != nil {
		fmt.Fprintf(w, "      seq completion %s, state %s\n", seqText(it.CompletionSeq), seqText(it.StateChangeSeq))
	}
	for _, n := range it.Notes {
		fmt.Fprintf(w, "      note: %s\n", n)
	}
}

func seqText(p *int64) string {
	if p == nil {
		return "null"
	}
	return strconv.FormatInt(*p, 10)
}

// writeIndented prints the last maxLines lines of text (all when maxLines is 0).
func writeIndented(w io.Writer, text string, indent, maxLines int) {
	lines := strings.Split(strings.TrimRight(text, "\n "), "\n")
	if maxLines > 0 && len(lines) > maxLines {
		fmt.Fprintf(w, "%s… %d earlier lines\n", strings.Repeat(" ", indent), len(lines)-maxLines)
		lines = lines[len(lines)-maxLines:]
	}
	pad := strings.Repeat(" ", indent)
	for _, l := range lines {
		fmt.Fprintf(w, "%s%s\n", pad, l)
	}
}

func inputKind(il *InputLine) string {
	switch {
	case il.Ghost == nil:
		return "unverified, may be a ghost suggestion"
	case *il.Ghost:
		return "ghost suggestion, never sent"
	default:
		return "typed, not sent"
	}
}
