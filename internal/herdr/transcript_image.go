package herdr

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
)

// GET /api/herdr/image: one picture of a pane's transcript, decoded. The page
// gets the transcript with every base64 string cut (transcript_filter.go), so it
// asks for each picture here by its line's uuid and the block's place in the
// line: "i" for message.content[i] (a pasted image), "i.j" for content[j] of the
// tool_result at message.content[i] (a Read of a PNG, a screenshot tool).

var imageBlockRE = regexp.MustCompile(`^(\d{1,3})(?:\.(\d{1,3}))?$`)

// imageTypes are the pictures served. Anything else, SVG above all, is refused:
// the bytes come from agents on other machines.
var imageTypes = map[string]bool{"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true}

// maxImageBytes caps one decoded picture. Claude Code sends far smaller ones.
// Tests lower it.
var maxImageBytes = 32 << 20

// imageLineMatches is how many lines with the uuid in them a lookup reads: the
// line itself, and before it nothing in a real transcript, but the lines after
// it name it as their parentUuid.
const imageLineMatches = 8

// ImageBytes is GET /api/herdr/image as HTTP status, content type and body: the
// picture's bytes on 200, else a JSON error.
func (c *Controller) ImageBytes(ctx context.Context, path, uuid, block string) (int, string, []byte) {
	i, j, ok := parseImageBlock(block)
	switch {
	case !uuidRE.MatchString(uuid):
		return imageRefusal(refuse(http.StatusBadRequest, "uuid must be the uuid of a transcript line"))
	case !ok:
		return imageRefusal(refuse(http.StatusBadRequest, `block must be "<i>" or "<i>.<j>"`))
	}
	c.mu.Lock()
	it, ok := c.itemAt(path)
	var e *transcriptEntry
	if st := c.transcripts[it.ID]; ok && st != nil {
		e = st.entry
	}
	m, run := c.machineOf(it), c.col.run
	c.mu.Unlock()
	if !ok {
		return imageRefusal(refuse(http.StatusNotFound, "unknown pane"))
	}
	ctx, cancel := context.WithTimeout(ctx, transcriptTimeout)
	defer cancel()
	if e == nil {
		// Asked before the transcript (a daemon restart under an open page): the
		// read joins the pane to its file.
		var err error
		if e, err = c.transcript(ctx, it); err != nil {
			return imageRefusal(transcriptRefusal(err))
		}
	}
	var lines [][]byte
	var err error
	if e.source == "ssh" {
		lines, err = remoteImageLines(ctx, run, m, e, uuid)
	} else {
		lines, err = localImageLines(e.path, uuid)
	}
	if err != nil {
		return imageRefusal(refuse(http.StatusBadGateway, err.Error()))
	}
	mediaType, data, r := imageIn(lines, uuid, i, j)
	if r != nil {
		return imageRefusal(r)
	}
	return http.StatusOK, mediaType, data
}

// parseImageBlock reads "i" (j is -1) or "i.j".
func parseImageBlock(block string) (i, j int, ok bool) {
	m := imageBlockRE.FindStringSubmatch(block)
	if m == nil {
		return 0, 0, false
	}
	i, _ = strconv.Atoi(m[1])
	j = -1
	if m[2] != "" {
		j, _ = strconv.Atoi(m[2])
	}
	return i, j, true
}

func imageRefusal(r *refusal) (int, string, []byte) {
	status, body := marshalStatus(r)
	return status, "application/json", body
}

// transcriptRefusal is a failed transcript read as HTTP: no transcript is a 404
// with the reason, anything else a 502.
func transcriptRefusal(err error) *refusal {
	var nt *noTranscript
	if errors.As(err, &nt) {
		return &refusal{http.StatusNotFound, map[string]any{"error": "no transcript", "reason": nt.reason}}
	}
	return refuse(http.StatusBadGateway, err.Error())
}

// localImageLines reads the transcript at p a line at a time and keeps the
// lines with uuid in them, stopping past maxReadBytes like a transcript read.
func localImageLines(p, uuid string) ([][]byte, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	lines, err := linesWith(io.LimitReader(f, maxReadBytes+1), uuid)
	if err != nil {
		return nil, fmt.Errorf("reading the transcript: %w", err)
	}
	return lines, nil
}

// linesWith is every line of r with s in it, up to imageLineMatches. It holds
// one line at a time; past maxReadBytes it stops with an error.
func linesWith(r io.Reader, s string) ([][]byte, error) {
	br := bufio.NewReaderSize(r, 1<<20)
	needle := []byte(s)
	var out [][]byte
	var read int64
	for len(out) < imageLineMatches {
		line, err := br.ReadBytes('\n')
		read += int64(len(line))
		if read > maxReadBytes {
			return nil, fmt.Errorf("stopped at %d MB, far past any real transcript", maxReadBytes>>20)
		}
		if bytes.Contains(line, needle) {
			out = append(out, line)
		}
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// imageScript prints "== lines" and then, gzipped, the first imageLineMatches
// lines of the transcript at path (under ~) with uuid in them, or "== missing"
// when the file is gone. Only checked values go in it.
func imageScript(path, uuid string) (string, error) {
	if !remoteFileRE.MatchString(path) || !uuidRE.MatchString(uuid) {
		return "", fmt.Errorf("unsafe image read: %q %q", path, uuid)
	}
	return fmt.Sprintf(`cd ~ || exit 1; f=%s; [ -f "$f" ] || { echo "== missing"; exit 0; }; echo "== lines"; grep -F -m %d -e %s "$f" | gzip -c`,
		path, imageLineMatches, uuid), nil
}

// remoteImageLines runs imageScript on the pane's machine: one SSH call.
func remoteImageLines(ctx context.Context, run runner, m machine, e *transcriptEntry, uuid string) ([][]byte, error) {
	script, err := imageScript(e.path, uuid)
	if err != nil {
		return nil, err
	}
	argv, err := sshArgv(m, script)
	if err != nil {
		return nil, err
	}
	out, err := run(ctx, nil, argv)
	if err != nil {
		return nil, err
	}
	head, rest, ok := cutMarker(out, "== ")
	switch {
	case ok && head == "missing":
		return nil, fmt.Errorf("the transcript %s is gone", e.file)
	case !ok || head != "lines":
		return nil, badFetch(string(out))
	}
	zr, err := gzip.NewReader(bytes.NewReader(rest))
	if err != nil {
		return nil, fmt.Errorf("reading the transcript: %w", err)
	}
	data, err := readCapped(zr)
	if err != nil {
		return nil, err
	}
	var lines [][]byte
	for line := range bytes.SplitSeq(data, []byte("\n")) {
		if len(line) > 0 {
			lines = append(lines, line)
		}
	}
	return lines, nil
}

// imageLine is the part of a transcript line a picture lookup reads.
type imageLine struct {
	UUID    string `json:"uuid"`
	Message struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// imageBlock is a content block that may be a picture.
type imageBlock struct {
	Type   string `json:"type"`
	Source struct {
		Type      string `json:"type"`
		MediaType string `json:"media_type"`
		Data      string `json:"data"`
	} `json:"source"`
}

// imageIn finds the line whose own uuid is uuid among lines and decodes the
// picture at block i (j < 0) or i.j of its message.
func imageIn(lines [][]byte, uuid string, i, j int) (mediaType string, data []byte, r *refusal) {
	for _, line := range lines {
		var l imageLine
		if json.Unmarshal(line, &l) != nil || l.UUID != uuid {
			continue
		}
		return imageAt(l, i, j)
	}
	return "", nil, noImage("No line of the transcript has uuid " + uuid)
}

func imageAt(l imageLine, i, j int) (string, []byte, *refusal) {
	at := strconv.Itoa(i)
	if j >= 0 {
		at += "." + strconv.Itoa(j)
	}
	var im imageBlock
	b, ok := blockAt(l.Message.Content, i, j)
	if !ok || json.Unmarshal(b, &im) != nil || im.Type != "image" || im.Source.Type != "base64" {
		return "", nil, noImage("Block " + at + " of line " + l.UUID + " is not a base64 image")
	}
	mt := im.Source.MediaType
	if !imageTypes[mt] {
		return "", nil, refuse(http.StatusUnsupportedMediaType, fmt.Sprintf("%q is not shown: png, jpeg, gif and webp only", mt))
	}
	if base64.StdEncoding.DecodedLen(len(im.Source.Data)) > maxImageBytes {
		return "", nil, refuse(http.StatusRequestEntityTooLarge, fmt.Sprintf("the image is over %d MB", maxImageBytes>>20))
	}
	data, err := base64.StdEncoding.DecodeString(im.Source.Data)
	if err != nil {
		return "", nil, refuse(http.StatusUnsupportedMediaType, "the image data is not base64")
	}
	if got := http.DetectContentType(data); got != mt {
		return "", nil, refuse(http.StatusUnsupportedMediaType, fmt.Sprintf("the bytes are %s, not %s", got, mt))
	}
	return mt, data, nil
}

// blockAt is content[i] of a message's content array, or with j >= 0 content[j]
// of the tool_result there.
func blockAt(content json.RawMessage, i, j int) (json.RawMessage, bool) {
	var blocks []json.RawMessage
	if json.Unmarshal(content, &blocks) != nil || i >= len(blocks) {
		return nil, false
	}
	if j < 0 {
		return blocks[i], true
	}
	var tr struct {
		Type    string          `json:"type"`
		Content json.RawMessage `json:"content"`
	}
	var inner []json.RawMessage
	if json.Unmarshal(blocks[i], &tr) != nil || tr.Type != "tool_result" || json.Unmarshal(tr.Content, &inner) != nil || j >= len(inner) {
		return nil, false
	}
	return inner[j], true
}

func noImage(reason string) *refusal {
	return &refusal{http.StatusNotFound, map[string]any{"error": "no image", "reason": reason}}
}
