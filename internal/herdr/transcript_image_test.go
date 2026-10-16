package herdr

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	imgPasted = "11111111-1111-4111-8111-111111111111"
	imgRead   = "22222222-2222-4222-8222-222222222222"
	imgChild  = "44444444-4444-4444-8444-444444444444"
	imgSVG    = "55555555-5555-4555-8555-555555555555"
	imgLies   = "66666666-6666-4666-8666-666666666666"
	imgText   = "77777777-7777-4777-8777-777777777777"
	imgBadB64 = "99999999-9999-4999-8999-999999999999"
	// imgOrphan is only ever a parentUuid: lines mention it, none has it.
	imgOrphan = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
)

// tinyPictures is a 2x2 PNG and a 2x2 JPEG, made here so their bytes are real.
func tinyPictures(t *testing.T) (pngData, jpegData []byte) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 200, A: 255})
	var p, j bytes.Buffer
	if err := png.Encode(&p, img); err != nil {
		t.Fatal(err)
	}
	if err := jpeg.Encode(&j, img, nil); err != nil {
		t.Fatal(err)
	}
	return p.Bytes(), j.Bytes()
}

func imageBlockJSON(media, data string) string {
	return `{"type":"image","source":{"type":"base64","media_type":"` + media + `","data":"` + data + `"}}`
}

// imageTranscript has a pasted picture (block 1), a Read of one (block 0.0,
// stored twice like Claude Code does) and the cases a lookup must refuse.
func imageTranscript(pngData, jpegData []byte) string {
	p64, j64 := base64.StdEncoding.EncodeToString(pngData), base64.StdEncoding.EncodeToString(jpegData)
	return strings.Join([]string{
		// It names imgRead as its parent before imgRead's own line: the lookup
		// must check the line's own uuid, not only that the uuid is in it.
		`{"parentUuid":"` + imgRead + `","type":"assistant","uuid":"` + imgChild + `","message":{"role":"assistant","content":[{"type":"text","text":"seen"}]}}`,
		`{"parentUuid":"` + imgOrphan + `","type":"user","uuid":"` + imgPasted + `","message":{"role":"user","content":[{"type":"text","text":"look"},` + imageBlockJSON("image/png", p64) + `]}}`,
		`{"parentUuid":"` + imgPasted + `","type":"user","uuid":"` + imgRead + `","message":{"role":"user","content":[{"tool_use_id":"toolu_1","type":"tool_result","content":[` + imageBlockJSON("image/jpeg", j64) + `]}]},"toolUseResult":{"type":"image","file":{"base64":"` + j64 + `","type":"image/jpeg"}}}`,
		`{"type":"user","uuid":"` + imgSVG + `","message":{"role":"user","content":[` + imageBlockJSON("image/svg+xml", base64.StdEncoding.EncodeToString([]byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`))) + `]}}`,
		`{"type":"user","uuid":"` + imgLies + `","message":{"role":"user","content":[` + imageBlockJSON("image/png", j64) + `]}}`,
		`{"type":"user","uuid":"` + imgBadB64 + `","message":{"role":"user","content":[` + imageBlockJSON("image/png", "not*base64") + `]}}`,
		`{"type":"user","uuid":"` + imgText + `","message":{"role":"user","content":"just text"}}`,
	}, "\n") + "\n"
}

// imageFixture is a local pane (wA:p1) and a box pane (workbox3 w3:p4) whose
// transcripts are imageTranscript, and a gemini pane (no reader).
func imageFixture(t *testing.T) (c *Controller, f *fakeBoxes, pngData, jpegData []byte, boxFile string) {
	t.Helper()
	home, box := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	pngData, jpegData = tinyPictures(t)
	text := imageTranscript(pngData, jpegData)
	mustWrite(t, filepath.Join(home, ".claude", "sessions", "4242.json"), `{"sessionId":"`+sessionA+`","cwd":"/src/a"}`)
	mustWrite(t, filepath.Join(home, ".claude", "projects", "-src-a", sessionA+".jsonl"), text)
	mustWrite(t, filepath.Join(box, ".claude", "sessions", "777.json"), `{"sessionId":"`+sessionB+`","cwd":"/home/u/app"}`)
	boxFile = filepath.Join(box, ".claude", "projects", "-home-u-app", sessionB+".jsonl")
	mustWrite(t, boxFile, text)
	f = &fakeBoxes{
		procs: map[string]string{
			"local/wA:p1":    `[{"argv":["claude"],"argv0":"claude","name":"2.1.300","pid":4242}]`,
			"workbox3/w3:p4": `[{"argv":["claude"],"name":"claude","pid":777}]`,
		},
		homes: map[string]string{"workdone-ib3": box},
	}
	c = newTranscriptController(f,
		Item{Machine: localLabel, PaneID: "wA:p1", Agent: "claude"},
		Item{Machine: "workbox3", Box: "ib3", PaneID: "w3:p4", Agent: "claude"},
		Item{Machine: localLabel, PaneID: "wA:p2", Agent: "gemini"},
	)
	return c, f, pngData, jpegData, boxFile
}

type imageError struct {
	Error  string `json:"error"`
	Reason string `json:"reason"`
}

func getImage(t *testing.T, c *Controller, path, uuid, block string) (int, string, []byte, imageError) {
	t.Helper()
	status, ctype, body := c.ImageBytes(context.Background(), path, uuid, block)
	var e imageError
	if status != http.StatusOK {
		if ctype != "application/json" || json.Unmarshal(body, &e) != nil {
			t.Fatalf("a refusal must be JSON: %d %q %q", status, ctype, body)
		}
	}
	return status, ctype, body, e
}

func TestImageBytes(t *testing.T) {
	c, f, pngData, jpegData, _ := imageFixture(t)
	tests := []struct {
		name, uuid, block string
		want              int
		media             string
		data              []byte
		says              string // in the error or the reason
	}{
		{"a pasted picture", imgPasted, "1", http.StatusOK, "image/png", pngData, ""},
		{"a Read of a picture: the tool_result's content", imgRead, "0.0", http.StatusOK, "image/jpeg", jpegData, ""},
		{"a text block", imgPasted, "0", http.StatusNotFound, "", nil, "Block 0 of line " + imgPasted + " is not a base64 image"},
		{"past the last block", imgPasted, "5", http.StatusNotFound, "", nil, "is not a base64 image"},
		{"a picture is not a tool_result", imgPasted, "1.0", http.StatusNotFound, "", nil, "Block 1.0 of line"},
		{"past the tool_result's content", imgRead, "0.1", http.StatusNotFound, "", nil, "is not a base64 image"},
		{"the tool_result itself", imgRead, "0", http.StatusNotFound, "", nil, "is not a base64 image"},
		{"a message that is only text", imgText, "0", http.StatusNotFound, "", nil, "is not a base64 image"},
		{"no line has the uuid", "88888888-8888-4888-8888-888888888888", "0", http.StatusNotFound, "", nil, "No line of the transcript has uuid"},
		{"a uuid only a parentUuid names", imgOrphan, "0", http.StatusNotFound, "", nil, "No line of the transcript has uuid"},
		{"SVG is refused", imgSVG, "0", http.StatusUnsupportedMediaType, "", nil, `"image/svg+xml" is not shown: png, jpeg, gif and webp only`},
		{"bytes that are not what the line says", imgLies, "0", http.StatusUnsupportedMediaType, "", nil, "the bytes are image/jpeg, not image/png"},
		{"data that is not base64", imgBadB64, "0", http.StatusUnsupportedMediaType, "", nil, "not base64"},
		{"a bad uuid", "../../etc/passwd", "0", http.StatusBadRequest, "", nil, "uuid"},
		{"an uppercase uuid", strings.ToUpper("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"), "0", http.StatusBadRequest, "", nil, "uuid"},
		{"a uuid with a shell word in it", imgPasted + ";touch x", "1", http.StatusBadRequest, "", nil, "uuid"},
		{"a block that is no number", imgPasted, "x", http.StatusBadRequest, "", nil, "block"},
		{"a block of three parts", imgPasted, "1.0.0", http.StatusBadRequest, "", nil, "block"},
		{"a negative block", imgPasted, "-1", http.StatusBadRequest, "", nil, "block"},
		{"a block past 999", imgPasted, "1000", http.StatusBadRequest, "", nil, "block"},
		{"no block", imgPasted, "", http.StatusBadRequest, "", nil, "block"},
	}
	for _, path := range []string{"local/wA:p1.md", "workbox3/w3:p4.md"} {
		for _, tt := range tests {
			t.Run(path+"/"+tt.name, func(t *testing.T) {
				status, ctype, body, e := getImage(t, c, path, tt.uuid, tt.block)
				if status != tt.want {
					t.Fatalf("status = %d %+v, want %d", status, e, tt.want)
				}
				if status == http.StatusOK && (ctype != tt.media || !bytes.Equal(body, tt.data)) {
					t.Errorf("got %q, %d bytes; want %q, %d bytes", ctype, len(body), tt.media, len(tt.data))
				}
				if status != http.StatusOK && !strings.Contains(e.Error+" "+e.Reason, tt.says) {
					t.Errorf("refusal %+v does not say %q", e, tt.says)
				}
			})
		}
	}
	for _, call := range f.calls {
		if strings.Contains(strings.Join(call, " "), "touch") || strings.Contains(strings.Join(call, " "), "passwd") {
			t.Errorf("an unchecked value reached a command: %q", call)
		}
	}
}

func TestImageBytesPanesAndCalls(t *testing.T) {
	c, f, pngData, _, boxFile := imageFixture(t)

	if status, _, _, e := getImage(t, c, "gone.md", imgPasted, "1"); status != http.StatusNotFound || e.Error != "unknown pane" {
		t.Errorf("unknown pane: %d %+v", status, e)
	}
	if status, _, _, e := getImage(t, c, "local/wA:p2.md", imgPasted, "1"); status != http.StatusNotFound || e.Error != "no transcript" || e.Reason != "No gemini reader yet" {
		t.Errorf("gemini pane: %d %+v", status, e)
	}

	// A picture asked for before the transcript: the read joins the pane first.
	n := f.count()
	if status, _, body, e := getImage(t, c, "workbox3/w3:p4.md", imgPasted, "1"); status != http.StatusOK || !bytes.Equal(body, pngData) {
		t.Fatalf("box picture before any transcript read: %d %+v", status, e)
	}
	if h, s := f.callsSince(n); h != 1 || s != 3 {
		t.Errorf("first picture from a box: %d herdr and %d ssh calls, want 1 and 3 (process-info, locate, fetch, then the picture)", h, s)
	}
	n = f.count()
	if status, _, _, e := getImage(t, c, "workbox3/w3:p4.md", imgRead, "0.0"); status != http.StatusOK {
		t.Fatalf("second box picture: %d %+v", status, e)
	}
	if h, s := f.callsSince(n); h != 0 || s != 1 {
		t.Errorf("a later picture from a box: %d herdr and %d ssh calls, want 0 and 1", h, s)
	}
	n = f.count()
	if status, _, _, e := getImage(t, c, "local/wA:p1.md", imgPasted, "1"); status != http.StatusOK {
		t.Fatalf("local picture: %d %+v", status, e)
	}
	if status, _, _, e := getImage(t, c, "local/wA:p1.md", imgRead, "0.0"); status != http.StatusOK {
		t.Fatalf("second local picture: %d %+v", status, e)
	}
	if h, s := f.callsSince(n); h != 1 || s != 0 {
		t.Errorf("local pictures: %d herdr and %d ssh calls, want 1 (the join) and 0", h, s)
	}

	if err := os.Remove(boxFile); err != nil {
		t.Fatal(err)
	}
	if status, _, _, e := getImage(t, c, "workbox3/w3:p4.md", imgPasted, "1"); status != http.StatusBadGateway || !strings.Contains(e.Error, "is gone") {
		t.Errorf("a box transcript that is gone: %d %+v", status, e)
	}
}

func TestImageBytesCaps(t *testing.T) {
	c, _, _, _, _ := imageFixture(t)

	oldImage := maxImageBytes
	maxImageBytes = 16
	t.Cleanup(func() { maxImageBytes = oldImage })
	if status, _, _, e := getImage(t, c, "local/wA:p1.md", imgPasted, "1"); status != http.StatusRequestEntityTooLarge || !strings.Contains(e.Error, "over 0 MB") {
		t.Errorf("a picture over the cap: %d %+v", status, e)
	}
	maxImageBytes = oldImage

	oldRead := maxReadBytes
	maxReadBytes = 64
	t.Cleanup(func() { maxReadBytes = oldRead })
	for _, path := range []string{"local/wA:p1.md", "workbox3/w3:p4.md"} {
		// The box's transcript read stops first; locally the read for the picture does.
		if status, _, _, e := getImage(t, c, path, imgRead, "0.0"); status != http.StatusBadGateway || !strings.Contains(e.Error, "far past any real") {
			t.Errorf("%s: a transcript past the read cap: %d %+v", path, status, e)
		}
	}
}

func TestImageScriptChecksItsValues(t *testing.T) {
	good := ".claude/projects/-home-u-app/" + sessionA + ".jsonl"
	for _, tt := range []struct{ path, uuid string }{
		{"../etc/passwd", imgPasted},
		{".claude/projects/x;touch y/" + sessionA + ".jsonl", imgPasted},
		{good, "x;touch y"},
		{good, ""},
	} {
		if s, err := imageScript(tt.path, tt.uuid); err == nil {
			t.Errorf("imageScript(%q, %q) = %q, want an error", tt.path, tt.uuid, s)
		}
	}
	s, err := imageScript(good, imgPasted)
	if err != nil || !strings.Contains(s, "grep -F -m 8 -e "+imgPasted+` "$f" | gzip -c`) || strings.ContainsRune(s, '\'') {
		t.Errorf("imageScript = %q, %v", s, err)
	}
}
