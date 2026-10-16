package herdr

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"regexp"
	"strings"
)

// GET /api/herdr/file: a picture an agent names by its absolute path in an
// answer (![alt](/abs/path.png)), read from the pane's machine. Only pictures
// leave: anything else is refused once its first bytes are sniffed.

// maxFilePath caps the file parameter.
const maxFilePath = 4096

// maxFileBytes caps one picture read from a pane's machine.
const maxFileBytes = 32 << 20

// sshPathRE is a path crit puts in a remote command: nothing a shell would
// expand or split (no space, quote, $, glob or ~).
var sshPathRE = regexp.MustCompile(`^/[A-Za-z0-9._@+=,:/-]+$`)

// FileBytes is GET /api/herdr/file as HTTP status, content type and body: the
// picture on 200, else a JSON error.
func (c *Controller) FileBytes(ctx context.Context, pane, file string) (int, string, []byte) {
	switch {
	case !strings.HasPrefix(file, "/"):
		return imageRefusal(badFile("file must be an absolute path"))
	case len(file) > maxFilePath:
		return imageRefusal(badFile(fmt.Sprintf("file is longer than %d bytes", maxFilePath)))
	case strings.ContainsAny(file, "\x00\r\n"):
		return imageRefusal(badFile("file has a NUL or a newline in it"))
	}
	file = path.Clean(file)
	c.mu.Lock()
	it, ok := c.itemAt(pane)
	m, run := c.machineOf(it), c.col.run
	c.mu.Unlock()
	if !ok {
		return imageRefusal(refuse(http.StatusNotFound, "unknown pane"))
	}
	var data []byte
	var r *refusal
	if m.Label == localLabel {
		data, r = readLocalFile(file)
	} else {
		ctx, cancel := context.WithTimeout(ctx, transcriptTimeout)
		defer cancel()
		data, r = readRemoteFile(ctx, run, m, file)
	}
	if r != nil {
		return imageRefusal(r)
	}
	ct := http.DetectContentType(data)
	if !imageTypes[ct] {
		return imageRefusal(refuse(http.StatusUnsupportedMediaType, ct+" is not shown: png, jpeg, gif and webp only"))
	}
	return http.StatusOK, ct, data
}

func badFile(reason string) *refusal {
	return &refusal{http.StatusBadRequest, map[string]any{"error": "bad file", "reason": reason}}
}

func tooBig() *refusal {
	return refuse(http.StatusRequestEntityTooLarge, fmt.Sprintf("the file is over %d MB", maxFileBytes>>20))
}

// readLocalFile reads a regular file on this Mac, up to maxFileBytes.
func readLocalFile(file string) ([]byte, *refusal) {
	fi, err := os.Stat(file)
	switch {
	case errors.Is(err, fs.ErrNotExist) || (err == nil && !fi.Mode().IsRegular()):
		return nil, refuse(http.StatusNotFound, "no such file")
	case err != nil:
		return nil, refuse(http.StatusBadGateway, err.Error())
	case fi.Size() > maxFileBytes:
		return nil, tooBig()
	}
	f, err := os.Open(file)
	if err != nil {
		return nil, refuse(http.StatusBadGateway, err.Error())
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxFileBytes+1))
	switch {
	case err != nil:
		return nil, refuse(http.StatusBadGateway, err.Error())
	case len(data) > maxFileBytes:
		return nil, tooBig()
	}
	return data, nil
}

// readRemoteFile reads a regular file on the pane's machine in one SSH call:
// "== file" and its first maxFileBytes+1 bytes, or "== missing".
func readRemoteFile(ctx context.Context, run runner, m machine, file string) ([]byte, *refusal) {
	if !sshPathRE.MatchString(file) {
		return nil, badFile("this path has characters drover does not pass to ssh")
	}
	script := fmt.Sprintf(`f=%s; [ -f "$f" ] || { echo "== missing"; exit 0; }; echo "== file"; head -c %d "$f"`, file, maxFileBytes+1)
	argv, err := sshArgv(m, script)
	if err != nil {
		return nil, refuse(http.StatusBadGateway, err.Error())
	}
	out, err := run(ctx, nil, argv)
	if err != nil {
		return nil, refuse(http.StatusBadGateway, err.Error())
	}
	head, rest, ok := cutMarker(out, "== ")
	switch {
	case ok && head == "missing":
		return nil, refuse(http.StatusNotFound, "no such file on "+m.Label)
	case !ok || head != "file":
		return nil, refuse(http.StatusBadGateway, badFetch(string(out)).Error())
	case len(rest) > maxFileBytes:
		return nil, tooBig()
	}
	return rest, nil
}
