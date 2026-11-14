package panel

import (
	"io/fs"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marioolf/cortex/internal/config"
)

const extOrigin = "chrome-extension://abcdefghijklmnop"

// ext sends a request to an extension route the way the extension's worker does.
func (en *env) ext(method, path, code, origin, body string) *httptest.ResponseRecorder {
	h := map[string]string{"Content-Type": "application/json"}
	if code != "" {
		h["X-Cortex-Extension"] = code
	}
	if origin != "" {
		h["Origin"] = origin
	}
	return en.raw(method, path, h, strings.NewReader(body))
}

func (en *env) ingest(code, body string) *httptest.ResponseRecorder {
	return en.ext("POST", "/api/ext/ingest", code, extOrigin, body)
}

// noteFiles lists the web notes in the vault (the fake collector's one lives under Projects).
func (en *env) noteFiles(t *testing.T) []string {
	t.Helper()
	var out []string
	filepath.WalkDir(en.vault, func(p string, d fs.DirEntry, _ error) error {
		if d != nil && !d.IsDir() && strings.HasSuffix(p, ".md") && d.Name() != "README.md" && !strings.Contains(p, "Projects") {
			out = append(out, p)
		}
		return nil
	})
	return out
}

func conv(extra string) string {
	return `{"source":"chatgpt","id":"abc-123","title":"Garden plan","url":"https://chatgpt.com/c/abc-123",` + extra + `"messages":[` +
		`{"role":"user","text":"Which plants like shade?"},{"role":"assistant","text":"Ferns and hostas."}]}`
}

func TestExtPing(t *testing.T) {
	en := setup(t)
	en.eng.Version = "9.9"
	if w := en.ext("GET", "/api/ext/ping", pairing, extOrigin, ""); w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"ok":true,"version":"9.9"}` {
		t.Errorf("right code: %d %s", w.Code, w.Body)
	}
	for name, code := range map[string]string{"none": "", "wrong": "nope", "prefix": pairing[:10], "longer": pairing + "x"} {
		if w := en.ext("GET", "/api/ext/ping", code, extOrigin, ""); w.Code != 403 {
			t.Errorf("%s: %d", name, w.Code)
		}
	}
	// Without a configured code the routes are closed, also for an empty header.
	closed := setupWith(t, func(c *config.Config) { c.ExtensionToken = "" }, nil)
	for _, code := range []string{"", "anything", pairing} {
		if w := closed.ext("GET", "/api/ext/ping", code, extOrigin, ""); w.Code != 403 {
			t.Errorf("no code configured, header %q: %d", code, w.Code)
		}
		if w := closed.ingest(code, conv("")); w.Code != 403 {
			t.Errorf("no code configured, ingest with %q: %d", code, w.Code)
		}
	}
	if n := len(closed.noteFiles(t)); n != 0 {
		t.Errorf("closed route wrote %d notes", n)
	}
}

func TestExtAuthIsSeparateFromThePanelToken(t *testing.T) {
	en := setup(t)
	// The panel token is not enough for the extension routes...
	for _, path := range []string{"/api/ext/ping", "/api/ext/ingest"} {
		method := "GET"
		if strings.HasSuffix(path, "ingest") {
			method = "POST"
		}
		w := en.raw(method, path, map[string]string{"X-Cortex-Token": tok, "Content-Type": "application/json"}, strings.NewReader(conv("")))
		if w.Code != 403 {
			t.Errorf("%s with the panel token: %d", path, w.Code)
		}
	}
	// ...and the pairing code is not enough for anything else.
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/state"}, {"POST", "/api/config"}, {"POST", "/api/open-vault"}, {"POST", "/api/connect"},
		{"POST", "/api/quit"}, {"POST", "/api/import"}, {"POST", "/api/ext/regenerate"}, {"GET", "/api/ext/other"},
	} {
		w := en.raw(c.method, c.path, map[string]string{"X-Cortex-Extension": pairing, "Content-Type": "application/json"}, strings.NewReader(`{}`))
		if w.Code != 403 {
			t.Errorf("%s %s with the pairing code: %d", c.method, c.path, w.Code)
		}
	}
	if n := len(en.noteFiles(t)); n != 0 {
		t.Errorf("%d notes written", n)
	}
}

func TestExtOriginRules(t *testing.T) {
	en := setup(t)
	for origin, want := range map[string]int{
		"":                               200, // no Origin: not a browser page (curl, or the worker without one)
		"chrome-extension://abcdefghijk": 200,
		"moz-extension://1234-5678":      200,
		"https://evil.example":           403,
		"https://chatgpt.com":            403,
		"http://127.0.0.1:47999":         403, // even our own panel's origin: the page has no business here
		"null":                           403,
		"chrome-extension:/x":            403,
		"file://":                        403,
	} {
		if w := en.ext("GET", "/api/ext/ping", pairing, origin, ""); w.Code != want {
			t.Errorf("Origin %q on ping: %d, want %d", origin, w.Code, want)
		}
		if w := en.ext("POST", "/api/ext/ingest", pairing, origin, `{}`); (w.Code == 403) != (want == 403) {
			t.Errorf("Origin %q on ingest: %d", origin, w.Code)
		}
	}
	// Never a CORS header, and no preflight handler.
	w := en.ext("GET", "/api/ext/ping", pairing, extOrigin, "")
	for k := range w.Header() {
		if strings.HasPrefix(strings.ToLower(k), "access-control-") {
			t.Errorf("CORS header %s", k)
		}
	}
	if w := en.ext("OPTIONS", "/api/ext/ingest", pairing, extOrigin, ""); w.Code != 405 {
		t.Errorf("OPTIONS: %d, want 405", w.Code)
	}
	if got := en.ext("GET", "/api/ext/ping", pairing, extOrigin, "").Header(); got.Get("X-Frame-Options") != "DENY" || got.Get("Cache-Control") != "no-store" || got.Get(Header) != "1" {
		t.Errorf("security headers missing: %v", got)
	}
}

func TestExtStillChecksTheHost(t *testing.T) {
	en := setup(t)
	r := httptest.NewRequest("GET", "/api/ext/ping", nil)
	r.Host = "evil.example"
	r.Header.Set("X-Cortex-Extension", pairing)
	w := httptest.NewRecorder()
	en.h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Errorf("foreign Host: %d", w.Code)
	}
}

func TestExtIngestValidation(t *testing.T) {
	en := setup(t)
	msg := `"messages":[{"role":"user","text":"hi"}]`
	long := strings.Repeat("é", 501)
	many := `"messages":[` + strings.TrimSuffix(strings.Repeat(`{"role":"user","text":"x"},`, 5001), ",") + `]`
	for name, c := range map[string]struct {
		body string
		code int
	}{
		"ok minimal":      {`{"source":"gemini","id":"g_1-x",` + msg + `}`, 200},
		"ok with time":    {`{"source":"claude-ai","id":"c1","messages":[{"role":"user","text":"hi","time":"2026-01-02T03:04:05Z"}]}`, 200},
		"ok long title":   {`{"source":"gemini","id":"g2","title":"` + long + `",` + msg + `}`, 200},
		"ok openai host":  {`{"source":"chatgpt","id":"o1","url":"https://chat.openai.com/c/o1",` + msg + `}`, 200},
		"ok empty bubble": {`{"source":"gemini","id":"g3","messages":[{"role":"user","text":"  "},{"role":"assistant","text":"yes"}]}`, 200},
		"bad source":      {`{"source":"copilot","id":"x1",` + msg + `}`, 400},
		"no source":       {`{"id":"x1",` + msg + `}`, 400},
		"id empty":        {`{"source":"gemini","id":"",` + msg + `}`, 400},
		"id slash":        {`{"source":"gemini","id":"a/b",` + msg + `}`, 400},
		"id dots":         {`{"source":"gemini","id":"..",` + msg + `}`, 400},
		"id too long":     {`{"source":"gemini","id":"` + strings.Repeat("a", 129) + `",` + msg + `}`, 400},
		"id 128":          {`{"source":"gemini","id":"` + strings.Repeat("a", 128) + `",` + msg + `}`, 200},
		"no messages":     {`{"source":"gemini","id":"m1","messages":[]}`, 400},
		"missing msgs":    {`{"source":"gemini","id":"m1"}`, 400},
		"5001 messages":   {`{"source":"gemini","id":"m2",` + many + `}`, 400},
		"bad role":        {`{"source":"gemini","id":"m3","messages":[{"role":"system","text":"x"}]}`, 400},
		"all blank":       {`{"source":"gemini","id":"m4","messages":[{"role":"user","text":" \n\t"}]}`, 400},
		"bad time":        {`{"source":"gemini","id":"m5","messages":[{"role":"user","text":"x","time":"yesterday"}]}`, 400},
		"http url":        {`{"source":"chatgpt","id":"u1","url":"http://chatgpt.com/c/u1",` + msg + `}`, 400},
		"foreign url":     {`{"source":"chatgpt","id":"u2","url":"https://claude.ai/chat/u2",` + msg + `}`, 400},
		"lookalike url":   {`{"source":"chatgpt","id":"u3","url":"https://chatgpt.com.evil.example/c/u3",` + msg + `}`, 400},
		"userinfo url":    {`{"source":"chatgpt","id":"u4","url":"https://chatgpt.com@evil.example/c/u4",` + msg + `}`, 400},
		"js url":          {`{"source":"chatgpt","id":"u5","url":"javascript:alert(1)",` + msg + `}`, 400},
		"unknown field":   {`{"source":"gemini","id":"f1","extra":1,` + msg + `}`, 400},
		"unknown in msg":  {`{"source":"gemini","id":"f2","messages":[{"role":"user","text":"x","html":"y"}]}`, 400},
		"not json":        {`nope`, 400},
		"trailing":        {`{"source":"gemini","id":"t1",` + msg + `} {}`, 400},
		"wrong type":      {`{"source":"gemini","id":"t2","messages":"hi"}`, 400},
	} {
		if w := en.ingest(pairing, c.body); w.Code != c.code {
			t.Errorf("%s: %d, want %d (%s)", name, w.Code, c.code, w.Body)
		}
	}
	// A long title is cut to 500 characters (the vault then shortens it again for the file name).
	req := ingestRequest{Source: "gemini", ID: "g2", Title: long, Messages: []ingestMessage{{Role: "user", Text: "hi"}}}
	if sess, err := req.session(); err != nil || len([]rune(sess.Title)) != 500 {
		t.Errorf("title = %d characters, %v", len([]rune(sess.Title)), err)
	}
	if w := en.ext("POST", "/api/ext/ingest", pairing, extOrigin, ""); w.Code != 400 {
		t.Errorf("empty body: %d", w.Code)
	}
	if w := en.raw("POST", "/api/ext/ingest", map[string]string{"X-Cortex-Extension": pairing, "Content-Type": "text/plain"}, strings.NewReader(conv(""))); w.Code != 415 {
		t.Errorf("wrong content type: %d", w.Code)
	}
}

func TestExtIngestTooLarge(t *testing.T) {
	en := setup(t)
	big := `{"source":"gemini","id":"big","messages":[{"role":"user","text":"` + strings.Repeat("a", 5<<20) + `"}]}`
	if w := en.ingest(pairing, big); w.Code != 413 {
		t.Errorf("over 5 MB: %d", w.Code)
	}
	// Just under the limit is accepted.
	// (Padded with whitespace: redacting megabytes of text would only slow the test.)
	ok := `{"source":"gemini","id":"big","messages":[{"role":"user","text":"hi"}]}` + strings.Repeat(" ", 5<<20-200)
	if w := en.ingest(pairing, ok); w.Code != 200 {
		t.Errorf("under 5 MB: %d %.100s", w.Code, w.Body)
	}
}

func TestExtIngestLifecycle(t *testing.T) {
	en := setup(t)
	result := func(body string) string {
		t.Helper()
		w := en.ingest(pairing, body)
		if w.Code != 200 {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
		return strings.TrimSpace(w.Body.String())
	}
	if got := result(conv("")); got != `{"result":"created"}` {
		t.Errorf("first = %s", got)
	}
	if got := result(conv("")); got != `{"result":"unchanged"}` {
		t.Errorf("same body = %s", got)
	}
	changed := strings.Replace(conv(""), "Ferns and hostas.", "Ferns, hostas and a secret password=hunter2hunter2.", 1)
	if got := result(changed); got != `{"result":"updated"}` {
		t.Errorf("changed = %s", got)
	}
	// The whole conversation was replaced, not appended to.
	files := en.noteFiles(t)
	if len(files) != 1 {
		t.Fatalf("notes = %v", files)
	}
	b, _ := os.ReadFile(files[0])
	note := string(b)
	if strings.Count(note, "Which plants like shade?") != 1 || strings.Contains(note, "Ferns and hostas.") || !strings.Contains(note, "Ferns, hostas and a secret") {
		t.Errorf("note not replaced:\n%s", note)
	}
	if strings.Contains(note, "hunter2hunter2") || !strings.Contains(note, "[REDACTED]") {
		t.Errorf("secret not redacted:\n%s", note)
	}
	for _, want := range []string{`source: "chatgpt"`, `session: "abc-123"`, `url: "https://chatgpt.com/c/abc-123"`, "started: ", "# Garden plan", "## User\n\nWhich plants", "## Assistant\n\nFerns"} {
		if !strings.Contains(note, want) {
			t.Errorf("note lacks %q:\n%s", want, note)
		}
	}
	// And the panel counts it.
	var n int
	for _, s := range en.state(t).Sources {
		if s.ID == "chatgpt" {
			n = s.Sessions
		}
	}
	if n != 1 {
		t.Errorf("chatgpt sessions = %d", n)
	}
}

func TestExtIngestSkippedWhenPausedOrSourceOff(t *testing.T) {
	en := setup(t)
	cfg := func(body string) {
		t.Helper()
		if w := en.do("POST", "/api/config", good, tok, "application/json", body); w.Code != 200 {
			t.Fatalf("%s: %d %s", body, w.Code, w.Body)
		}
	}
	skipped := func(why string) {
		t.Helper()
		w := en.ingest(pairing, conv(""))
		if w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"result":"skipped"}` {
			t.Errorf("%s: %d %s", why, w.Code, w.Body)
		}
		if n := len(en.noteFiles(t)); n != 0 {
			t.Errorf("%s: %d notes written", why, n)
		}
	}
	cfg(`{"paused":true}`)
	skipped("paused")
	cfg(`{"paused":false}`)
	cfg(`{"source":{"id":"chatgpt","enabled":false}}`)
	skipped("source off")
	// Another source is unaffected.
	if w := en.ingest(pairing, `{"source":"gemini","id":"g1","messages":[{"role":"user","text":"hi"}]}`); !strings.Contains(w.Body.String(), "created") {
		t.Errorf("gemini: %s", w.Body)
	}
}

func TestExtRegenerate(t *testing.T) {
	en := setup(t)
	old := en.state(t).Extension
	if old.Code != pairing || old.Port != 47999 {
		t.Fatalf("extension state = %+v", old)
	}
	if w := en.do("POST", "/api/ext/regenerate", good, "", "application/json", `{}`); w.Code != 403 {
		t.Errorf("without the panel token: %d", w.Code)
	}
	if w := en.do("POST", "/api/ext/regenerate", good, tok, "application/json", `{}`); w.Code != 200 {
		t.Fatalf("regenerate: %d %s", w.Code, w.Body)
	}
	fresh := en.state(t).Extension.Code
	if len(fresh) != 64 || fresh == old.Code {
		t.Errorf("new code = %q", fresh)
	}
	if w := en.ext("GET", "/api/ext/ping", old.Code, extOrigin, ""); w.Code != 403 {
		t.Errorf("old code still works: %d", w.Code)
	}
	if w := en.ingest(old.Code, conv("")); w.Code != 403 {
		t.Errorf("old code still ingests: %d", w.Code)
	}
	if w := en.ext("GET", "/api/ext/ping", fresh, extOrigin, ""); w.Code != 200 {
		t.Errorf("new code: %d", w.Code)
	}
	if saved, err := config.LoadFile(en.cfgPath); err != nil || saved.ExtensionToken != fresh {
		t.Errorf("saved token = %q, %v", saved.ExtensionToken, err)
	}
}
