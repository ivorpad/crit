package herdr

import (
	"path/filepath"
	"regexp"
	"strings"
)

// Item statuses beyond herdr's own (idle, working, blocked, done).
const (
	statusBackground  = "background"            // a watched agent left its pane (gateway)
	statusExited      = "exited"                // the agent is gone; its pane shows how to resume
	statusUnreadable  = "unreadable_by_gateway" // herdr lists it, the gateway's allowedRoots hide it
	sourceTranscript  = "transcript"
	sourceScreen      = "screen"
	refClaudeJSONL    = "claude_jsonl"
	refCodexRollout   = "codex_rollout"
	refPiJSONL        = "pi_jsonl"
	refOpencodeSQLite = "opencode_sqlite"
	refGrokJSONL      = "grok_jsonl"
	refCursorJSONL    = "cursor_jsonl"
	attentionDialog   = "dialog"
	attentionQuestion = "question"
)

// Item is one agent pane on one machine: what it is, what state herdr says it is
// in, and what it needs from the owner. Nothing here is used to send anything yet.
type Item struct {
	// ID is "<machine label>/<pane id>". Pane IDs are only unique per herdr server.
	ID          string `json:"id"`
	Machine     string `json:"machine"`
	Box         string `json:"instabox,omitempty"`
	PaneID      string `json:"pane_id"`
	Agent       string `json:"agent"` // claude, codex, opencode, pi, cursor, ...
	Name        string `json:"name,omitempty"`
	Status      string `json:"status"`                 // herdr's status, or background / exited / unreadable_by_gateway
	HerdrStatus string `json:"herdr_status,omitempty"` // herdr's own agent_status, when it lists an agent
	CWD         string `json:"cwd,omitempty"`
	Title       string `json:"title,omitempty"`
	WorkspaceID string `json:"workspace_id,omitempty"`
	TabID       string `json:"tab_id,omitempty"`

	// The turn signal and the binding key for replies, read from herdr itself. Null
	// only when herdr lists no agent in the pane (exited, background).
	CompletionSeq  *int64 `json:"completion_seq"`
	StateChangeSeq *int64 `json:"state_change_seq"`

	// Attention is the gateway's verdict (attention.ts): "dialog" when a menu is up,
	// "question" when the last answer asks the owner something, empty otherwise.
	Attention string `json:"attention,omitempty"`
	NeedsYou  bool   `json:"needs_you"`

	// Dialog is the menu as the gateway's dialog.ts parsed it. When herdr says
	// blocked but dialog.ts could not read the menu, DialogScreen holds the screen.
	Dialog       *Dialog `json:"dialog,omitempty"`
	DialogScreen string  `json:"dialog_screen,omitempty"`
	// Plan is Claude's plan when the dialog is ExitPlanMode.
	Plan *Plan `json:"plan,omitempty"`

	// Last is the agent's last answer, or the turn in progress.
	Last *LastText `json:"last,omitempty"`
	// TranscriptRef says where the agent's full transcript is, for rendering turns.
	TranscriptRef *TranscriptRef `json:"transcript_ref,omitempty"`

	// InputLine is what sits on the agent's input line.
	InputLine *InputLine `json:"input_line,omitempty"`

	// ResumeCommand is the line an exited agent left on screen (claude --resume <id>).
	ResumeCommand string `json:"resume_command,omitempty"`

	// Notes says what this item is missing and why: a gateway without a reader for
	// this agent, a read the gateway refused, an ANSI read it does not support yet.
	Notes []string `json:"notes,omitempty"`
}

// Dialog mirrors dialogView in the gateway's answer-ops.ts.
type Dialog struct {
	ID       string   `json:"dialog_id"`
	Kind     string   `json:"kind"` // question, permission, gated, or a go-ahead kind (trust, update, ...)
	Text     string   `json:"text"`
	Options  []Choice `json:"options"`
	Multi    bool     `json:"multi"`
	FreeText bool     `json:"free_text"`
	GoAhead  *int     `json:"go_ahead"` // the option WorkDone would pick for a go-ahead menu
}

// Choice mirrors Choice in the gateway's dialog.ts.
type Choice struct {
	N        int    `json:"n"`
	Label    string `json:"label"`
	Current  bool   `json:"current,omitempty"`
	Checked  bool   `json:"checked,omitempty"`
	FreeText bool   `json:"free_text,omitempty"`
}

// Plan is a plan file named on an ExitPlanMode dialog. Text is empty when the
// gateway could not read it; Error says why.
type Plan struct {
	Path  string `json:"path"`
	Text  string `json:"text,omitempty"`
	Error string `json:"error,omitempty"`
	// Source is where Text came from: "gateway" or "local_disk" (this Mac's own
	// plan, read directly when the gateway refuses the path).
	Source string `json:"-"`
}

// LastText is the agent's last answer and where it came from.
type LastText struct {
	Text string `json:"text"`
	// Source is "transcript" (the gateway's transcript.ts) or "screen" (the last
	// lines of the viewport, input box cut off, used when there is no transcript).
	Source     string      `json:"source"`
	At         string      `json:"at,omitempty"`
	Truncated  bool        `json:"truncated"`
	InProgress *InProgress `json:"in_progress,omitempty"`
}

// InProgress is a prompt newer than the last answer.
type InProgress struct {
	Prompt      string `json:"prompt"`
	LatestText  string `json:"latest_text,omitempty"`
	Interrupted bool   `json:"interrupted"`
}

// InputLine is the text on an agent's input line. Ghost is true for a suggestion
// the agent drew dim (never send it as the owner's words), false for typed text,
// and null when it could not be told: no ANSI read, or a mix of both.
type InputLine struct {
	Text  string `json:"text"`
	Ghost *bool  `json:"ghost"`
}

// TranscriptRef points at an agent's transcript on its machine.
type TranscriptRef struct {
	Kind    string `json:"kind"` // claude_jsonl, cursor_jsonl, codex_rollout, opencode_sqlite, pi_jsonl
	Path    string `json:"path,omitempty"`
	Session string `json:"session,omitempty"`
}

// snapshotAgent is the part of an overview agent (gateway agent-ops.ts) the
// collector reads.
type snapshotAgent struct {
	Name         *string    `json:"name"`
	PaneID       string     `json:"pane_id"`
	Agent        string     `json:"agent"`
	Status       string     `json:"status"`
	CWD          string     `json:"cwd"`
	Title        *string    `json:"title"`
	WorkspaceID  string     `json:"workspace_id"`
	TabID        string     `json:"tab_id"`
	Attention    *string    `json:"attention"`
	Choices      *Dialog    `json:"choices"`
	DialogScreen string     `json:"dialog"`
	ScreenTail   string     `json:"screen_tail"`
	LastReply    *wireReply `json:"last_reply"`
}

// wireReply is a reply as overview (clipped) or read_agent source=reply (full) sends it.
type wireReply struct {
	Text       string `json:"text"`
	At         string `json:"at"`
	Truncated  bool   `json:"truncated"`
	InProgress *struct {
		Prompt      string  `json:"prompt"`
		LatestText  *string `json:"latest_text"`
		Interrupted bool    `json:"interrupted"`
	} `json:"in_progress"`
	// Ref is sent by gateways with the herdr-control-center transcript readers.
	Ref *TranscriptRef `json:"ref"`
}

type snapshotBackground struct {
	Name   *string `json:"name"`
	PaneID string  `json:"pane_id"`
	Agent  *string `json:"agent"`
	Status string  `json:"status"`
	CWD    string  `json:"cwd"`
}

type consoleSnapshot struct {
	Agents     []snapshotAgent      `json:"agents"`
	Background []snapshotBackground `json:"background"`
}

// overviewClip is the length the gateway's overview clips last_reply.text to.
const overviewClip = 500

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func lastFromReply(r *wireReply, clippedAt int) *LastText {
	out := &LastText{Text: r.Text, Source: sourceTranscript, At: r.At, Truncated: r.Truncated}
	if clippedAt > 0 && len([]rune(r.Text)) >= clippedAt && strings.HasSuffix(r.Text, "…") {
		out.Truncated = true
	}
	if p := r.InProgress; p != nil {
		out.InProgress = &InProgress{Prompt: p.Prompt, LatestText: deref(p.LatestText), Interrupted: p.Interrupted}
	}
	return out
}

// itemFromAgent builds an item from the gateway's view of an agent, with herdr's
// own record (seqs, session) when herdr listed it.
func itemFromAgent(m machine, a snapshotAgent, h *herdrAgent) Item {
	it := Item{
		ID: m.Label + "/" + a.PaneID, Machine: m.Label, Box: m.Box, PaneID: a.PaneID,
		Agent: a.Agent, Name: deref(a.Name), Status: a.Status, CWD: a.CWD, Title: deref(a.Title),
		WorkspaceID: a.WorkspaceID, TabID: a.TabID, Attention: deref(a.Attention),
		Dialog: a.Choices,
	}
	if h != nil {
		it.HerdrStatus = h.AgentStatus
		if it.Name == "" {
			it.Name = deref(h.Name)
		}
		it.CompletionSeq, it.StateChangeSeq = h.CompletionSeq, h.StateChangeSeq
	}
	if it.Dialog == nil && a.DialogScreen != "" {
		it.DialogScreen = a.DialogScreen
	}
	switch {
	case a.LastReply != nil:
		it.Last = lastFromReply(a.LastReply, overviewClip)
		it.TranscriptRef = a.LastReply.Ref
	case a.ScreenTail != "":
		it.Last = &LastText{Text: cutInputBox(a.ScreenTail), Source: sourceScreen, Truncated: true}
		if line, ok := inputLine(a.ScreenTail); ok && it.Dialog == nil {
			it.InputLine = &InputLine{Text: line}
		}
	}
	if it.TranscriptRef == nil {
		it.TranscriptRef = fallbackRef(m, it.Agent, it.CWD, h)
	}
	if it.Last != nil && it.Last.Source == sourceScreen {
		it.Notes = append(it.Notes, screenNote(it.Agent, h))
	}
	if path := planPath(it.dialogText()); path != "" {
		it.Plan = &Plan{Path: path}
	}
	it.NeedsYou = it.Attention != ""
	return it
}

// screenNote says why the last answer came from the screen.
func screenNote(agent string, h *herdrAgent) string {
	switch agent {
	case "claude", "cursor":
		if h == nil || h.AgentSession == nil {
			return "last answer from the screen: herdr reports no agent_session for this pane (herdr's agent integration is not installed on this machine), so the gateway cannot find its transcript"
		}
		return "last answer from the screen: the gateway found no transcript for this session"
	default:
		return "last answer from the screen: this machine's gateway has no " + agent + " transcript reader yet"
	}
}

func (it Item) dialogText() string {
	if it.Dialog != nil {
		return it.Dialog.Text
	}
	return it.DialogScreen
}

// itemFromHerdr is an agent herdr lists but the gateway does not show: its cwd is
// outside the gateway's allowedRoots. Only herdr's fields are known.
func itemFromHerdr(m machine, h herdrAgent) Item {
	it := Item{
		ID: m.Label + "/" + h.PaneID, Machine: m.Label, Box: m.Box, PaneID: h.PaneID,
		Agent: deref(h.Agent), Name: deref(h.Name), Status: statusUnreadable, HerdrStatus: h.AgentStatus, CWD: h.cwd(),
		Title: deref(h.Title), WorkspaceID: h.WorkspaceID, TabID: h.TabID,
		CompletionSeq: h.CompletionSeq, StateChangeSeq: h.StateChangeSeq,
		Notes: []string{"outside the gateway's allowedRoots: herdr's fields only, nothing read from the pane"},
	}
	it.TranscriptRef = fallbackRef(m, it.Agent, it.CWD, &h)
	return it
}

func itemFromBackground(m machine, b snapshotBackground) Item {
	return Item{
		ID: m.Label + "/" + b.PaneID, Machine: m.Label, Box: m.Box, PaneID: b.PaneID,
		Agent: deref(b.Agent), Name: deref(b.Name), Status: b.Status, CWD: b.CWD,
	}
}

// itemFromExited is a pane herdr lists with no agent whose screen shows a resume line.
func itemFromExited(m machine, p herdrAgent, cmd, kind string) Item {
	it := Item{
		ID: m.Label + "/" + p.PaneID, Machine: m.Label, Box: m.Box, PaneID: p.PaneID,
		Agent: kind, Status: statusExited, CWD: p.cwd(), Title: deref(p.Title),
		WorkspaceID: p.WorkspaceID, TabID: p.TabID, ResumeCommand: cmd,
	}
	it.TranscriptRef = fallbackRef(m, kind, it.CWD, &p)
	if it.TranscriptRef == nil && kind == "claude" {
		if id := strings.TrimPrefix(cmd, "claude --resume "); id != cmd {
			it.TranscriptRef = claudeRef(m, it.CWD, id)
		}
	}
	return it
}

var uuidRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// fallbackRef is the transcript reference the collector can name without the
// gateway's readers, from herdr's agent_session: Claude's session id, the pi
// session file on this Mac, opencode's, grok's and cursor-agent's session id.
func fallbackRef(m machine, agent, cwd string, h *herdrAgent) *TranscriptRef {
	if h == nil || h.AgentSession == nil {
		return nil
	}
	s := h.AgentSession
	if r, ok := sessionIDRefs[agent]; ok && s.Kind == "id" && r.id.MatchString(s.Value) {
		return &TranscriptRef{Kind: r.kind, Session: s.Value}
	}
	switch {
	case agent == "claude" && uuidRE.MatchString(s.Value):
		return claudeRef(m, cwd, s.Value)
	case agent == "pi" && s.Kind == "path" && m.Target == "" && isPiSession(s.Value):
		return &TranscriptRef{Kind: refPiJSONL, Path: s.Value}
	}
	return nil
}

// sessionIDRefs are the agents herdr names by a session id: the ref kind each
// gets, and the form the id must have.
var sessionIDRefs = map[string]struct {
	kind string
	id   *regexp.Regexp
}{
	"opencode": {refOpencodeSQLite, opencodeIDRE},
	"grok":     {refGrokJSONL, uuidRE},
	"cursor":   {refCursorJSONL, uuidRE},
}

// claudeRef names a Claude transcript. On the local machine the path is checked
// in every config dir (~/.claude, ~/.claude-work, $CLAUDE_CONFIG_DIR); elsewhere
// only the session is known.
func claudeRef(m machine, cwd, session string) *TranscriptRef {
	ref := &TranscriptRef{Kind: refClaudeJSONL, Session: session}
	if m.Target == "" && cwd != "" {
		for _, d := range claudeConfigDirs() {
			if p := filepath.Join(d, "projects", claudeSlug(cwd), session+".jsonl"); isFile(p) {
				ref.Path = p
				break
			}
		}
	}
	return ref
}

var slugRE = regexp.MustCompile(`[^A-Za-z0-9-]`)

// claudeSlug is Claude Code's project directory name: the cwd with every character
// other than a letter, digit or dash replaced by a dash.
func claudeSlug(cwd string) string { return slugRE.ReplaceAllString(cwd, "-") }

var planPathRE = regexp.MustCompile(`~/\.claude/plans/[A-Za-z0-9._-]+\.md`)

// planPath finds the plan file on Claude's ExitPlanMode dialog (S9).
func planPath(dialog string) string { return planPathRE.FindString(dialog) }

var (
	ruleRE   = regexp.MustCompile(`^\s*[─━═]{10,}\s*$`)
	promptRE = regexp.MustCompile(`^\s*[❯›>][\s\x{00a0}]*(.*?)\s*$`)
)

// inputBoxAt returns the index of the prompt line of the input box at the bottom
// of a screen (a prompt line drawn right under a horizontal rule), or -1.
func inputBoxAt(lines []string) int {
	for i := len(lines) - 1; i > 0; i-- {
		if promptRE.MatchString(lines[i]) && ruleRE.MatchString(lines[i-1]) {
			return i
		}
	}
	return -1
}

// inputLine reads the input box from plain text. ok is false when there is none or
// it is empty.
func inputLine(screen string) (string, bool) {
	lines := strings.Split(screen, "\n")
	i := inputBoxAt(lines)
	if i < 0 {
		return "", false
	}
	m := promptRE.FindStringSubmatch(lines[i])
	text := strings.TrimSpace(strings.ReplaceAll(m[1], " ", " "))
	return text, text != ""
}

// cutInputBox drops the input box and the status lines under it from a screen tail,
// so the answer reads without the agent's chrome.
func cutInputBox(screen string) string {
	lines := strings.Split(screen, "\n")
	if i := inputBoxAt(lines); i > 0 {
		lines = lines[:i-1]
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n ")
}
