package herdr

import (
	"bytes"
	"encoding/json"
	"fmt"
	"unicode/utf8"
)

// clipMax is the longest JSON string a transcript line keeps.
const clipMax = 64 << 10

// requestDebug are the transcript line types left out: request debug, most of a
// transcript's bytes and nothing the page shows.
var requestDebug = map[string]bool{"api-request": true, "api-request-blob": true, "api-request-shape": true}

// filterJSONL keeps data's complete lines minus the ones drop names (Claude's
// request debug, Codex's rollout noise), with every JSON string over clipMax
// cut. used is data's length up to and including its last newline: a line
// still being written waits for the next read.
func filterJSONL(data []byte, drop func([]byte) bool) (out []byte, used, dropped int64, clipped int) {
	end := bytes.LastIndexByte(data, '\n') + 1
	for rest := data[:end]; len(rest) > 0; {
		i := bytes.IndexByte(rest, '\n')
		line := rest[:i]
		rest = rest[i+1:]
		switch {
		case len(bytes.TrimSpace(line)) == 0:
			continue
		case drop(line):
			dropped += int64(len(line) + 1)
			continue
		case len(line) > clipMax:
			var n int
			line, n = clipStrings(line)
			clipped += n
		}
		out = append(out, line...)
		out = append(out, '\n')
	}
	return out, int64(end), dropped, clipped
}

func isRequestDebug(line []byte) bool {
	if !bytes.Contains(line, []byte(`"api-request`)) {
		return false
	}
	var l struct {
		Type string `json:"type"`
	}
	return json.Unmarshal(line, &l) == nil && requestDebug[l.Type]
}

// clipStrings cuts every JSON string in line longer than clipMax bytes (as
// written, escapes included) to clipMax, plus a note of how many bytes went. A
// string of only base64 characters (a picture's data) is cut to the note alone:
// the page fetches the picture whole from GET /api/herdr/image. The rest of the
// line stays byte for byte, so only these lines change.
func clipStrings(line []byte) ([]byte, int) {
	var out []byte
	n, copied := 0, 0
	for i := 0; i < len(line); i++ {
		if line[i] != '"' {
			continue
		}
		start, j := i+1, i+1
		for j < len(line) && line[j] != '"' {
			if line[j] == '\\' {
				j++
			}
			j++
		}
		j = min(j, len(line))
		if j-start > clipMax {
			cut := start
			if !isBase64(line[start:j]) {
				cut += safeCut(line[start:j], clipMax)
			}
			out = append(out, line[copied:cut]...)
			out = append(out, fmt.Sprintf(" [… %d bytes cut]", j-cut)...)
			copied = j
			n++
		}
		i = j
	}
	if n == 0 {
		return line, 0
	}
	return append(out, line[copied:]...), n
}

// isBase64 reports a string as written that holds only base64 characters. Text
// has spaces or escapes in it long before 64 KB.
func isBase64(s []byte) bool {
	for _, b := range s {
		if (b < 'A' || b > 'Z') && (b < 'a' || b > 'z') && (b < '0' || b > '9') && b != '+' && b != '/' && b != '=' {
			return false
		}
	}
	return true
}

// safeCut is the largest length up to limit that ends neither inside an escape
// (\n, \uXXXX) nor inside a UTF-8 sequence.
func safeCut(s []byte, limit int) int {
	k := 0
	for k < limit {
		step := 1
		switch {
		case s[k] == '\\' && k+1 < len(s) && s[k+1] == 'u':
			step = 6
		case s[k] == '\\':
			step = 2
		case s[k] >= utf8.RuneSelf:
			_, step = utf8.DecodeRune(s[k:])
		}
		if k+step > limit {
			return k
		}
		k += step
	}
	return k
}
