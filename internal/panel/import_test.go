package panel

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// Invented exports: one ChatGPT conversation and one claude.ai conversation.
const gptExport = `[{"id":"imp-gpt-1","title":"Kitchen shelves","current_node":"a1","mapping":{
 "root":{"id":"root","parent":"","children":["u1"],"message":null},
 "u1":{"id":"u1","parent":"root","children":["a1"],"message":{"author":{"role":"user"},"create_time":1700000010,"recipient":"all","content":{"content_type":"text","parts":["How deep should a pantry shelf be?"]},"metadata":{}}},
 "a1":{"id":"a1","parent":"u1","children":[],"message":{"author":{"role":"assistant"},"create_time":1700000020,"recipient":"all","content":{"content_type":"text","parts":["About 30 cm is plenty."]},"metadata":{}}}}}]`

const claudeExport = `[{"uuid":"imp-claude-1","name":"Bread baking","chat_messages":[
 {"sender":"human","text":"Why is my sourdough flat?","created_at":"2025-03-01T10:00:00Z"},
 {"sender":"assistant","text":"Probably under-proofed.","created_at":"2025-03-01T10:00:05Z"}]}]`

func zipOf(t *testing.T, name, data string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create(name)
	w.Write([]byte(data))
	zw.Close()
	return buf.Bytes()
}

func (en *env) upload(ctype string, body io.Reader) *httptest.ResponseRecorder {
	return en.raw("POST", "/api/import", map[string]string{"X-Cortex-Token": tok, "Content-Type": ctype}, body)
}

func (en *env) tempLeft(t *testing.T) {
	t.Helper()
	if left, _ := os.ReadDir(en.tmp); len(left) != 0 {
		t.Errorf("temp files left behind: %v", left)
	}
}

type counts struct {
	Conversations, Created, Updated, Unchanged, Failed, Unreadable int
}

func decodeCounts(t *testing.T, w *httptest.ResponseRecorder) counts {
	t.Helper()
	var c counts
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &c) != nil {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	return c
}

func TestImportHappyPath(t *testing.T) {
	for name, c := range map[string]struct {
		ctype string
		body  []byte
	}{
		"chatgpt zip":  {"application/zip", zipOf(t, "conversations.json", gptExport)},
		"chatgpt json": {"application/json", []byte(gptExport)},
		"claude zip":   {"application/octet-stream", zipOf(t, "conversations.json", claudeExport)},
		"claude json":  {"application/json; charset=utf-8", []byte(claudeExport)},
	} {
		t.Run(name, func(t *testing.T) {
			en := setup(t)
			got := decodeCounts(t, en.upload(c.ctype, bytes.NewReader(c.body)))
			if got != (counts{Conversations: 1, Created: 1}) {
				t.Errorf("counts = %+v", got)
			}
			if n := len(en.noteFiles(t)); n != 1 {
				t.Errorf("notes = %v", en.noteFiles(t))
			}
			en.tempLeft(t)
		})
	}
}

func TestImportRules(t *testing.T) {
	en := setup(t)
	body := func() io.Reader { return strings.NewReader(gptExport) }
	if w := en.raw("POST", "/api/import", map[string]string{"Content-Type": "application/json"}, body()); w.Code != 403 {
		t.Errorf("no token: %d", w.Code)
	}
	for _, ct := range []string{"", "text/plain", "multipart/form-data; boundary=x", "application/x-www-form-urlencoded"} {
		if w := en.upload(ct, body()); w.Code != 415 {
			t.Errorf("Content-Type %q: %d", ct, w.Code)
		}
	}
	// The exception is for this route only.
	if w := en.do("POST", "/api/config", good, tok, "application/zip", `{}`); w.Code != 415 {
		t.Errorf("zip on /api/config: %d", w.Code)
	}
	if w := en.upload("application/zip", strings.NewReader("hello, this is not an export")); w.Code != 400 || !strings.Contains(w.Body.String(), "unsupported file") {
		t.Errorf("unsupported: %d %s", w.Code, w.Body)
	}
	if w := en.upload("application/json", strings.NewReader(`[{"nothing":"here"}]`)); w.Code != 400 {
		t.Errorf("other json: %d", w.Code)
	}
	if w := en.upload("application/zip", bytes.NewReader(zipOf(t, "notes.txt", "x"))); w.Code != 400 || !strings.Contains(w.Body.String(), "unsupported file") {
		t.Errorf("zip without conversations: %d %s", w.Code, w.Body)
	}
	en.tempLeft(t)
	if n := len(en.noteFiles(t)); n != 0 {
		t.Errorf("%d notes from refused files", n)
	}
	if w := en.raw("GET", "/api/import", map[string]string{"X-Cortex-Token": tok}, nil); w.Code != 405 {
		t.Errorf("GET: %d", w.Code)
	}
}

func TestImportTooLarge(t *testing.T) {
	en := setupWith(t, nil, func(o *Options) { o.MaxImport = 1000 })
	if w := en.upload("application/json", strings.NewReader(gptExport+strings.Repeat(" ", 2000))); w.Code != 413 {
		t.Errorf("over the cap: %d %s", w.Code, w.Body)
	}
	en.tempLeft(t)
	if w := en.upload("application/json", strings.NewReader(claudeExport)); w.Code != 200 {
		t.Errorf("under the cap: %d %s", w.Code, w.Body)
	}
	en.tempLeft(t)
}

func TestImportOneAtATime(t *testing.T) {
	en := setup(t)
	pr, pw := io.Pipe()
	done := make(chan *httptest.ResponseRecorder)
	go func() { done <- en.upload("application/json", pr) }()
	// The first import is running once its temp file exists.
	for i := 0; ; i++ {
		if left, _ := os.ReadDir(en.tmp); len(left) > 0 {
			break
		}
		if i > 500 {
			t.Fatal("the first import never started")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if w := en.upload("application/json", strings.NewReader(gptExport)); w.Code != 409 {
		t.Errorf("second import: %d %s", w.Code, w.Body)
	}
	io.WriteString(pw, claudeExport)
	pw.Close()
	if w := <-done; w.Code != 200 {
		t.Errorf("first import: %d %s", w.Code, w.Body)
	}
	en.tempLeft(t)
	// Free again afterwards.
	if w := en.upload("application/json", strings.NewReader(gptExport)); w.Code != 200 {
		t.Errorf("after: %d %s", w.Code, w.Body)
	}
}

func TestImportIgnoresPauseAndSourceSwitch(t *testing.T) {
	en := setup(t)
	for _, body := range []string{`{"paused":true}`, `{"source":{"id":"chatgpt","enabled":false}}`, `{"source":{"id":"claude-ai","enabled":false}}`} {
		if w := en.do("POST", "/api/config", good, tok, "application/json", body); w.Code != 200 {
			t.Fatal(w.Body)
		}
	}
	if got := decodeCounts(t, en.upload("application/json", strings.NewReader(gptExport))); got.Created != 1 {
		t.Errorf("chatgpt while paused and off: %+v", got)
	}
	if got := decodeCounts(t, en.upload("application/json", strings.NewReader(claudeExport))); got.Created != 1 {
		t.Errorf("claude while paused and off: %+v", got)
	}
	if n := len(en.noteFiles(t)); n != 2 {
		t.Errorf("notes = %v", en.noteFiles(t))
	}
	// The extension, on the other hand, is still held back.
	if w := en.ingest(pairing, conv("")); !strings.Contains(w.Body.String(), "skipped") {
		t.Errorf("extension push while paused: %s", w.Body)
	}
}

func TestReimportIsIdempotent(t *testing.T) {
	en := setup(t)
	zipped := zipOf(t, "conversations.json", gptExport)
	if got := decodeCounts(t, en.upload("application/zip", bytes.NewReader(zipped))); got.Created != 1 {
		t.Fatalf("first: %+v", got)
	}
	if got := decodeCounts(t, en.upload("application/zip", bytes.NewReader(zipped))); got != (counts{Conversations: 1, Unchanged: 1}) {
		t.Errorf("second: %+v", got)
	}
	if n := len(en.noteFiles(t)); n != 1 {
		t.Errorf("notes = %v", en.noteFiles(t))
	}
}

func TestImportedThenPushedIsOneNote(t *testing.T) {
	en := setup(t)
	decodeCounts(t, en.upload("application/json", strings.NewReader(gptExport)))
	// The extension later sends the same conversation, with one more answer.
	push := `{"source":"chatgpt","id":"imp-gpt-1","title":"Kitchen shelves","url":"https://chatgpt.com/c/imp-gpt-1","messages":[` +
		`{"role":"user","text":"How deep should a pantry shelf be?"},{"role":"assistant","text":"About 30 cm is plenty."},` +
		`{"role":"user","text":"And the spacing?"}]}`
	if w := en.ingest(pairing, push); !strings.Contains(w.Body.String(), "updated") {
		t.Errorf("push: %d %s", w.Code, w.Body)
	}
	files := en.noteFiles(t)
	if len(files) != 1 {
		t.Fatalf("notes = %v", files)
	}
	b, _ := os.ReadFile(files[0])
	if !strings.Contains(string(b), "And the spacing?") {
		t.Errorf("push not in the note:\n%s", b)
	}
}

func TestImportReportsPartialFailures(t *testing.T) {
	en := setup(t)
	// Second element has the shape of a ChatGPT conversation but a wrong type inside.
	mixed := strings.TrimSuffix(gptExport, "]") + `,{"id":"bad","mapping":{"x":"not a node"}}]`
	w := en.upload("application/json", strings.NewReader(mixed))
	got := decodeCounts(t, w)
	if got.Created != 1 || got.Conversations != 2 || got.Unreadable != 1 {
		t.Errorf("counts = %+v (%s)", got, w.Body)
	}
}
