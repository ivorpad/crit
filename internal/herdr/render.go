package herdr

import (
	"fmt"
	"strings"
)

// itemPath is the pane's file in the review: "<machine>/<pane id>.md". It stays
// the same while the pane lives, so comments on it survive title changes.
func itemPath(it Item) string { return it.Machine + "/" + it.PaneID + ".md" }

// planFilePath is the review file of a pane's plan, "<machine>/<pane id>.plan.md":
// its own file so the plan's lines take comments apart from the pane's.
func planFilePath(it Item) string { return it.Machine + "/" + it.PaneID + ".plan.md" }

// hasPlanFile reports whether a pane's plan gets its own review file.
func hasPlanFile(it Item) bool { return it.Plan != nil && it.Plan.Text != "" }

// renderItem turns one pane into the markdown the review page shows. Agent text
// goes in as written when it came from a transcript (it is markdown) and inside a
// text fence when it came from the screen (terminal layout, not markdown). The
// page renders herdr documents with raw HTML off.
func renderItem(it Item) string {
	var b strings.Builder
	renderHead(&b, it)
	renderDialog(&b, it)
	renderPlan(&b, it)
	renderLast(&b, it.Last)
	renderInput(&b, it.InputLine)
	if it.ResumeCommand != "" {
		b.WriteString("## Exited\n\n")
		fmt.Fprintf(&b, "The agent left the pane. To bring it back: `%s`\n\n", it.ResumeCommand)
	}
	if len(it.Notes) > 0 {
		b.WriteString("## Notes\n\n")
		for _, n := range it.Notes {
			fmt.Fprintf(&b, "- %s\n", oneLine(n))
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

func renderHead(b *strings.Builder, it Item) {
	title := it.Title
	if title == "" {
		title = it.PaneID
	}
	fmt.Fprintf(b, "# %s\n\n", oneLine(title))
	where := it.Machine
	if it.Box != "" {
		where += " (" + it.Box + ")"
	}
	fmt.Fprintf(b, "- **Where:** %s · pane `%s` · %s · %s\n", where, it.PaneID, orDash(it.Agent), it.Status)
	if it.CWD != "" {
		fmt.Fprintf(b, "- **Directory:** `%s`\n", it.CWD)
	}
	if it.CompletionSeq != nil || it.StateChangeSeq != nil {
		fmt.Fprintf(b, "- **Turn:** completion_seq %s · state_change_seq %s\n", seqText(it.CompletionSeq), seqText(it.StateChangeSeq))
	}
	b.WriteString("\n")
	switch it.Attention {
	case attentionDialog:
		b.WriteString("## Needs you: a menu is open\n\n")
	case attentionQuestion:
		b.WriteString("## Needs you: its last answer asks you something\n\n")
	}
}

func renderDialog(b *strings.Builder, it Item) {
	if it.Dialog == nil && it.DialogScreen == "" {
		return
	}
	kind, text := "unparsed", it.DialogScreen
	if it.Dialog != nil {
		kind, text = it.Dialog.Kind, it.Dialog.Text
	}
	fmt.Fprintf(b, "## Dialog (%s)\n\n", kind)
	writeFence(b, text)
	if it.Dialog == nil {
		return
	}
	for _, c := range it.Dialog.Options {
		extra := ""
		if c.FreeText {
			extra = " _(opens a text field)_"
		}
		fmt.Fprintf(b, "%d. %s%s\n", c.N, oneLine(c.Label), extra)
	}
	b.WriteString("\n")
}

func renderPlan(b *strings.Builder, it Item) {
	if it.Plan == nil {
		return
	}
	fmt.Fprintf(b, "## Plan `%s`\n\n", it.Plan.Path)
	if hasPlanFile(it) {
		fmt.Fprintf(b, "The plan is its own file: `%s`.\n\n", planFilePath(it))
		return
	}
	fmt.Fprintf(b, "Not read: %s\n\n", oneLine(it.Plan.Error))
}

func renderLast(b *strings.Builder, l *LastText) {
	if l == nil {
		return
	}
	if p := l.InProgress; p != nil {
		b.WriteString("## Turn in progress\n\n")
		writeQuote(b, p.Prompt)
		if p.LatestText != "" {
			b.WriteString(strings.TrimRight(p.LatestText, "\n") + "\n\n")
		}
		if p.Interrupted {
			b.WriteString("_Interrupted._\n\n")
		}
	}
	heading := "## Last answer"
	if l.At != "" {
		heading += " (" + l.At + ")"
	}
	b.WriteString(heading + "\n\n")
	if l.Source == sourceScreen {
		b.WriteString("_From the screen, not a transcript; earlier lines are cut off._\n\n")
		writeFence(b, l.Text)
	} else {
		b.WriteString(strings.TrimRight(l.Text, "\n") + "\n\n")
		if l.Truncated {
			b.WriteString("_Cut short by the gateway._\n\n")
		}
	}
	b.WriteString("_Only the last answer is shown: earlier turns need a transcript read the gateway does not offer yet._\n\n")
}

func renderInput(b *strings.Builder, il *InputLine) {
	if il == nil {
		return
	}
	b.WriteString("## On the input line\n\n")
	switch {
	case il.Ghost != nil && *il.Ghost:
		b.WriteString("A suggestion the agent drew dim. It was not typed by you and is never sent.\n\n")
	case il.Ghost != nil:
		b.WriteString("Typed into the pane, not sent.\n\n")
	default:
		b.WriteString("Unverified: this may be the agent's own suggestion, not something you typed.\n\n")
	}
	writeQuote(b, il.Text)
}

// writeFence puts text in a fence longer than any backtick run inside it.
func writeFence(b *strings.Builder, text string) {
	fence := "```"
	for strings.Contains(text, fence) {
		fence += "`"
	}
	fmt.Fprintf(b, "%stext\n%s\n%s\n\n", fence, strings.TrimRight(text, "\n"), fence)
}

func writeQuote(b *strings.Builder, text string) {
	for _, l := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		b.WriteString("> " + l + "\n")
	}
	b.WriteString("\n")
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
