package herdr

import (
	"regexp"
	"strconv"
	"strings"
)

var (
	sgrRE      = regexp.MustCompile(`\x1b\[([0-9;:]*)m`)
	otherCSIRE = regexp.MustCompile(`\x1b\[[0-9;:?<>=]*[A-Za-ln-z@]`) // every CSI but SGR (final byte m)
	oscRE      = regexp.MustCompile(`\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)
	escAnyRE   = regexp.MustCompile(`\x1b[@-Z\\-_]`)
)

func hasANSI(s string) bool { return strings.Contains(s, "\x1b[") }

// stripANSI removes every escape sequence.
func stripANSI(s string) string {
	s = oscRE.ReplaceAllString(s, "")
	s = sgrRE.ReplaceAllString(s, "")
	s = otherCSIRE.ReplaceAllString(s, "")
	return escAnyRE.ReplaceAllString(s, "")
}

// dimSpans splits an ANSI line into its visible text and, per rune, whether SGR
// dim (ESC[2m) was on when it was drawn. 22 or 0 turns dim off.
func dimSpans(line string) (text []rune, dim []bool) {
	line = oscRE.ReplaceAllString(line, "")
	line = otherCSIRE.ReplaceAllString(line, "")
	on := false
	for len(line) > 0 {
		loc := sgrRE.FindStringSubmatchIndex(line)
		chunk := line
		if loc != nil {
			chunk = line[:loc[0]]
		}
		for _, r := range escAnyRE.ReplaceAllString(chunk, "") {
			text = append(text, r)
			dim = append(dim, on)
		}
		if loc == nil {
			break
		}
		params := line[loc[2]:loc[3]]
		if params == "" {
			on = false
		}
		for _, p := range strings.FieldsFunc(params, func(r rune) bool { return r == ';' || r == ':' }) {
			switch n, _ := strconv.Atoi(p); n {
			case 0, 22:
				on = false
			case 2:
				on = true
			}
		}
		line = line[loc[1]:]
	}
	return text, dim
}

// inputLineANSI reads the input box from an ANSI screen. ghost is true when every
// visible character after the prompt mark is dim (a suggestion the agent drew),
// false when none is, and nil when it is mixed: then nobody can say what was typed.
func inputLineANSI(screen string) (text string, ghost *bool, ok bool) {
	lines := strings.Split(screen, "\n")
	plain := make([]string, len(lines))
	for i, l := range lines {
		plain[i] = stripANSI(l)
	}
	i := inputBoxAt(plain)
	if i < 0 {
		return "", nil, false
	}
	runes, dim := dimSpans(lines[i])
	text, ghost = classifyInput(runes, dim)
	return text, ghost, text != ""
}

// classifyInput takes the runes of a prompt line after its leading space and
// prompt mark, and says whether they were drawn dim.
func classifyInput(runes []rune, dim []bool) (string, *bool) {
	j := 0
	for j < len(runes) && (runes[j] == ' ' || runes[j] == '\u00a0') {
		j++
	}
	if j < len(runes) && strings.ContainsRune("❯›>", runes[j]) {
		j++
	}
	var b strings.Builder
	dimCount, litCount := 0, 0
	for ; j < len(runes); j++ {
		r := runes[j]
		if r == '\u00a0' {
			r = ' '
		}
		b.WriteRune(r)
		switch {
		case r == ' ':
		case dim[j]:
			dimCount++
		default:
			litCount++
		}
	}
	text := strings.TrimSpace(b.String())
	if text == "" {
		return "", nil
	}
	switch {
	case litCount == 0:
		t := true
		return text, &t
	case dimCount == 0:
		f := false
		return text, &f
	}
	return text, nil
}
