package panel

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marioolf/cortex/internal/collector"
	"github.com/marioolf/cortex/internal/config"
	"github.com/marioolf/cortex/internal/engine"
	"github.com/marioolf/cortex/internal/session"
)

const tok = "secret-token"

type fakeSource struct{}

func (fakeSource) ID() string       { return "fake" }
func (fakeSource) Detect() []string { return []string{"/x"} }
func (fakeSource) Reset()           {}
func (fakeSource) Collect(fn func(*session.Session), warn func(error)) {
	fn(&session.Session{Source: "fake", ID: "s1", Cwd: "/work/<alpha>", Title: "t",
		Messages: []session.Message{{Role: "user", Text: "hi", Time: time.Now()}}})
}

type env struct {
	h         http.Handler
	cfgPath   string
	clients   map[string]string // config file of each client, in a temp dir
	autostart bool
	opened    []string
	vault     string // the vault folder
	tmp       string // where uploads are spooled
	eng       *engine.Engine
}

const pairing = "pair-code-0123456789abcdef"

func setup(t *testing.T) *env { return setupWith(t, nil, nil) }

// setupWith lets a test change the config before the engine reads it, and the
// panel's options before it is built.
func setupWith(t *testing.T, cfgFn func(*config.Config), optFn func(*Options)) *env {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	cfg := &config.Config{Vault: filepath.Join(dir, "vault"), ExtensionToken: pairing}
	if cfgFn != nil {
		cfgFn(cfg)
	}
	if err := cfg.Save(cfgPath); err != nil {
		t.Fatal(err)
	}
	e, err := engine.New(cfgPath, []collector.Collector{fakeSource{}})
	if err != nil {
		t.Fatal(err)
	}
	e.Sync()
	en := &env{cfgPath: cfgPath, eng: e, vault: cfg.Vault, tmp: filepath.Join(dir, "tmp"), clients: map[string]string{
		"claude-desktop": filepath.Join(dir, "Claude", "claude_desktop_config.json"),
		"cursor":         filepath.Join(dir, ".cursor", "mcp.json"),
	}}
	os.MkdirAll(en.tmp, 0o700)
	opt := Options{
		Port:             47999,
		TempDir:          en.tmp,
		Exe:              func() (string, error) { return `C:\Apps\Cortex\cortex.exe`, nil },
		ClientPaths:      en.clients,
		GOOS:             "windows",
		WSLAvailable:     func() bool { return true },
		AutostartEnabled: func() bool { return en.autostart },
		SetAutostart:     func(on bool) error { en.autostart = on; return nil },
		Open:             func(p string) error { en.opened = append(en.opened, p); return nil },
	}
	if optFn != nil {
		optFn(&opt)
	}
	en.h = New(e, tok, opt)
	return en
}

func (en *env) raw(method, path string, hdr map[string]string, body io.Reader) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, body)
	r.Host = good
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	en.h.ServeHTTP(w, r)
	return w
}

func (en *env) do(method, path, host, token, ctype, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Host = host
	if token != "" {
		r.Header.Set("X-Cortex-Token", token)
	}
	if ctype != "" {
		r.Header.Set("Content-Type", ctype)
	}
	w := httptest.NewRecorder()
	en.h.ServeHTTP(w, r)
	return w
}

const good = "127.0.0.1:47999"

func TestHostCheck(t *testing.T) {
	en := setup(t)
	for host, want := range map[string]int{
		good: 200, "localhost:47999": 200,
		"evil.example": 403, "127.0.0.1": 403, "127.0.0.1:1": 403, "localhost": 403,
		"127.0.0.1:47999.evil.example": 403, "": 403,
	} {
		if got := en.do("GET", "/", host, "", "", "").Code; got != want {
			t.Errorf("Host %q: %d, want %d", host, got, want)
		}
		if got := en.do("GET", "/api/state", host, tok, "", "").Code; got != want {
			t.Errorf("api Host %q: %d, want %d", host, got, want)
		}
	}
}

func TestTokenCheck(t *testing.T) {
	en := setup(t)
	for token, want := range map[string]int{"": 403, "wrong": 403, tok + "x": 403, tok: 200} {
		if got := en.do("GET", "/api/state", good, token, "", "").Code; got != want {
			t.Errorf("token %q: %d, want %d", token, got, want)
		}
	}
	if got := en.do("POST", "/api/config", good, "", "application/json", `{}`).Code; got != 403 {
		t.Errorf("post without token: %d", got)
	}
	if got := en.do("POST", "/api/open-vault", good, "", "application/json", `{}`).Code; got != 403 {
		t.Errorf("open-vault without token: %d", got)
	}
	if len(en.opened) != 0 {
		t.Error("vault opened without a token")
	}
}

func TestPostLimits(t *testing.T) {
	en := setup(t)
	for _, ct := range []string{"", "text/plain", "application/x-www-form-urlencoded", "application/jsonx"} {
		if got := en.do("POST", "/api/config", good, tok, ct, `{"paused":true}`).Code; got != 415 {
			t.Errorf("Content-Type %q: %d, want 415", ct, got)
		}
	}
	if got := en.do("POST", "/api/config", good, tok, "application/json; charset=utf-8", `{"paused":true}`).Code; got != 200 {
		t.Errorf("json with charset: %d", got)
	}
	big := `{"vault":"/` + strings.Repeat("a", 70<<10) + `"}`
	if got := en.do("POST", "/api/config", good, tok, "application/json", big).Code; got != 400 {
		t.Errorf("oversized body: %d, want 400", got)
	}
	if got := en.do("POST", "/api/config", good, tok, "application/json", `{"nope":1}`).Code; got != 400 {
		t.Errorf("unknown field: %d, want 400", got)
	}
}

func TestPageHeadersAndToken(t *testing.T) {
	en := setup(t)
	w := en.do("GET", "/", good, "", "", "")
	h := w.Header()
	for k, want := range map[string]string{
		"Cache-Control": "no-store", "X-Content-Type-Options": "nosniff", "X-Frame-Options": "DENY",
	} {
		if h.Get(k) != want {
			t.Errorf("%s = %q", k, h.Get(k))
		}
	}
	csp := h.Get("Content-Security-Policy")
	body := w.Body.String()
	if strings.Contains(csp, "unsafe") || !strings.Contains(csp, "default-src 'none'") {
		t.Errorf("csp = %q", csp)
	}
	i := strings.Index(csp, "'nonce-")
	nonce := csp[i+7 : i+7+strings.Index(csp[i+7:], "'")]
	if nonce == "" || strings.Count(body, `nonce="`+nonce+`"`) != 2 {
		t.Errorf("nonce %q not applied to the page's style and script", nonce)
	}
	if !strings.Contains(body, `content="`+tok+`"`) || strings.Contains(body, "{{") {
		t.Error("token or placeholders not handled")
	}
	if strings.Contains(body, "innerHTML") || strings.Contains(body, "http://") || strings.Contains(body, "https://") {
		t.Error("page uses innerHTML or an external resource")
	}
	// API responses carry the headers too.
	if en.do("GET", "/api/state", good, tok, "", "").Header().Get("X-Frame-Options") != "DENY" {
		t.Error("api lacks headers")
	}
	if en.do("GET", "/nothing", good, "", "", "").Code != 404 {
		t.Error("unknown path should be 404")
	}
}

func decode(t *testing.T, w *httptest.ResponseRecorder) engine.State {
	t.Helper()
	var s engine.State
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &s) != nil {
		t.Fatalf("code %d body %s", w.Code, w.Body)
	}
	return s
}

func TestConfigChanges(t *testing.T) {
	en := setup(t)
	post := func(body string) *httptest.ResponseRecorder {
		return en.do("POST", "/api/config", good, tok, "application/json", body)
	}
	s := decode(t, en.do("GET", "/api/state", good, tok, "", ""))
	if s.Paused || len(s.Projects) != 1 || s.Projects[0].Name != "<alpha>" || s.Autostart {
		t.Fatalf("initial state = %+v", s)
	}

	if s := decode(t, post(`{"paused":true}`)); !s.Paused {
		t.Error("pause not reflected")
	}
	if s := decode(t, post(`{"project":{"name":"<alpha>","excluded":true}}`)); !s.Projects[0].Excluded {
		t.Error("exclusion not reflected")
	}
	if s := decode(t, post(`{"source":{"id":"fake","enabled":false}}`)); s.Sources[0].Enabled {
		t.Error("source still enabled")
	}
	if s := decode(t, post(`{"autostart":true}`)); !s.Autostart || !en.autostart {
		t.Error("autostart not applied through the injected toggle")
	}
	if post(`{"source":{"id":"ghost","enabled":false}}`).Code != 400 {
		t.Error("unknown source accepted")
	}
	if post(`{"vault":"relative/dir"}`).Code != 400 {
		t.Error("relative vault accepted")
	}
	newVault := filepath.Join(t.TempDir(), "v2")
	if s := decode(t, post(`{"vault":"`+strings.ReplaceAll(newVault, `\`, `\\`)+`"}`)); s.Vault != newVault {
		t.Errorf("vault = %q", s.Vault)
	}

	saved, err := config.LoadFile(en.cfgPath)
	if err != nil || !saved.Paused || saved.Vault != newVault ||
		len(saved.DisabledSources) != 1 || len(saved.ExcludedProjects) != 1 || saved.ExcludedProjects[0] != "<alpha>" {
		t.Errorf("saved config = %+v, %v", saved, err)
	}

	if w := en.do("POST", "/api/open-vault", good, tok, "application/json", `{}`); w.Code != 200 || len(en.opened) != 1 || en.opened[0] != newVault {
		t.Errorf("open-vault: %d %v", w.Code, en.opened)
	}
}

func TestEveryResponseCarriesTheCortexHeader(t *testing.T) {
	en := setup(t)
	for name, w := range map[string]*httptest.ResponseRecorder{
		"page":      en.do("GET", "/", good, "", "", ""),
		"state":     en.do("GET", "/api/state", good, tok, "", ""),
		"bad host":  en.do("GET", "/", "evil.example", "", "", ""),
		"bad token": en.do("GET", "/api/state", good, "nope", "", ""),
	} {
		if w.Header().Get(Header) != "1" {
			t.Errorf("%s (status %d): no %s header", name, w.Code, Header)
		}
	}
}

func TestRunning(t *testing.T) {
	ours := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(Header, "1")
		http.Error(w, "forbidden", http.StatusForbidden) // the header counts whatever the status
	}))
	defer ours.Close()
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("hello"))
	}))
	defer other.Close()
	gone := httptest.NewServer(http.NotFoundHandler())
	gone.Close()

	for name, c := range map[string]struct {
		url  string
		want bool
	}{"cortex": {ours.URL, true}, "another program": {other.URL, false}, "nothing listening": {gone.URL, false}} {
		if got := Running(c.url, time.Second); got != c.want {
			t.Errorf("%s: Running = %v, want %v", name, got, c.want)
		}
	}
}

func TestAutostartIsCached(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	(&config.Config{Vault: filepath.Join(dir, "vault")}).Save(cfgPath)
	e, err := engine.New(cfgPath, []collector.Collector{fakeSource{}})
	if err != nil {
		t.Fatal(err)
	}
	now, lookups, on := time.Now(), 0, false
	h := New(e, tok, Options{
		Port:             47999,
		Now:              func() time.Time { return now },
		Exe:              func() (string, error) { return "/opt/cortex", nil },
		ClientPaths:      map[string]string{},
		AutostartEnabled: func() bool { lookups++; return on },
		SetAutostart:     func(v bool) error { on = v; return nil },
	})
	get := func() bool {
		r := httptest.NewRequest("GET", "/api/state", nil)
		r.Host = good
		r.Header.Set("X-Cortex-Token", tok)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		var s engine.State
		json.NewDecoder(w.Body).Decode(&s)
		return s.Autostart
	}
	for i := 0; i < 7; i++ {
		get()
		now = now.Add(5 * time.Second)
	}
	if lookups != 2 { // at 0s and at 30s
		t.Errorf("lookups over 0..30s = %d, want 2", lookups)
	}

	r := httptest.NewRequest("POST", "/api/config", strings.NewReader(`{"autostart":true}`))
	r.Host = good
	r.Header.Set("X-Cortex-Token", tok)
	r.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(httptest.NewRecorder(), r)
	if !get() {
		t.Error("the value was not refreshed right after SetAutostart")
	}
}

func (en *env) state(t *testing.T) stateJSON {
	t.Helper()
	w := en.do("GET", "/api/state", good, tok, "", "")
	var s stateJSON
	if err := json.NewDecoder(w.Body).Decode(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestStateReportsMCPCommand(t *testing.T) {
	en := setup(t)
	m := en.state(t).MCP
	if m.Command != `C:\Apps\Cortex\cortex.exe` || len(m.Args) != 1 || m.Args[0] != "mcp" {
		t.Errorf("mcp = %+v", m)
	}
	if want := `claude mcp add --scope user cortex -- 'C:\Apps\Cortex\cortex.exe' mcp`; m.ClaudeCode != want {
		t.Errorf("claude_code = %s", m.ClaudeCode)
	}
	if want := `claude mcp add --scope user cortex -- '/mnt/c/Apps/Cortex/cortex.exe' mcp`; m.ClaudeCodeWSL != want {
		t.Errorf("claude_code_wsl = %s", m.ClaudeCodeWSL)
	}
	if m.ClaudeShell != "PowerShell" {
		t.Errorf("shell = %q", m.ClaudeShell)
	}
	if strings.Contains(m.ClaudeCodeWSL, "-vault") {
		t.Error("the WSL variant must not need -vault")
	}
	// The Windows backslashes survive JSON encoding.
	raw := en.do("GET", "/api/state", good, tok, "", "").Body.String()
	if !strings.Contains(raw, `"command":"C:\\Apps\\Cortex\\cortex.exe"`) {
		t.Errorf("command not escaped in %s", raw)
	}
}

func TestNoWSLVariantWithoutDistrosOrOnOtherOSes(t *testing.T) {
	dir := t.TempDir()
	e, _ := engine.New(filepath.Join(dir, "c.json"), nil)
	for name, o := range map[string]Options{
		"no distros": {GOOS: "windows", WSLAvailable: func() bool { return false }},
		"linux":      {GOOS: "linux", WSLAvailable: func() bool { return true }},
	} {
		o.Port, o.ClientPaths, o.Exe = 47999, map[string]string{}, func() (string, error) { return `C:\x\cortex.exe`, nil }
		r := httptest.NewRequest("GET", "/api/state", nil)
		r.Host = good
		r.Header.Set("X-Cortex-Token", tok)
		w := httptest.NewRecorder()
		New(e, tok, o).ServeHTTP(w, r)
		var st stateJSON
		json.NewDecoder(w.Body).Decode(&st)
		if st.MCP.Command == "" || st.MCP.ClaudeCodeWSL != "" {
			t.Errorf("%s: %+v", name, st.MCP)
		}
	}
}

func TestConnectClients(t *testing.T) {
	en := setup(t)
	post := func(body string) *httptest.ResponseRecorder {
		return en.do("POST", "/api/connect", good, tok, "application/json", body)
	}
	status := func(id string) clientState {
		for _, c := range en.state(t).MCP.Clients {
			if c.ID == id {
				return c
			}
		}
		t.Fatalf("no client %s", id)
		return clientState{}
	}

	// Neither client is installed: no folder, no button, and POST refuses without creating it.
	if c := status("cursor"); c.Available {
		t.Error("cursor available without its folder")
	}
	if w := post(`{"client":"cursor"}`); w.Code != 400 || !strings.Contains(w.Body.String(), "by hand") {
		t.Errorf("uninstalled: %d %s", w.Code, w.Body)
	}
	if _, err := os.Stat(filepath.Dir(en.clients["cursor"])); err == nil {
		t.Error("the client's folder was created")
	}

	// Installed, with other settings in the file.
	os.MkdirAll(filepath.Dir(en.clients["cursor"]), 0o755)
	orig := `{"mcpServers": {"other": {"command": "x"}}, "keep":  [1, 2]}`
	os.WriteFile(en.clients["cursor"], []byte(orig), 0o600)
	if c := status("cursor"); !c.Available || c.Status != "" {
		t.Errorf("before = %+v", c)
	}
	if w := post(`{"client":"cursor"}`); w.Code != 200 {
		t.Fatalf("connect: %d %s", w.Code, w.Body)
	}
	if c := status("cursor"); c.Status != "connected" {
		t.Errorf("after = %+v", c)
	}
	got, _ := os.ReadFile(en.clients["cursor"])
	if !strings.Contains(string(got), `"other": {"command": "x"}`) || !strings.Contains(string(got), `[1, 2]`) {
		t.Errorf("file = %s", got)
	}
	if bak, _ := os.ReadFile(en.clients["cursor"] + ".bak"); string(bak) != orig {
		t.Errorf("bak = %s", bak)
	}

	// A file that is not an object is refused and left alone.
	os.MkdirAll(filepath.Dir(en.clients["claude-desktop"]), 0o755)
	os.WriteFile(en.clients["claude-desktop"], []byte(`[1]`), 0o600)
	if w := post(`{"client":"claude-desktop"}`); w.Code != 400 || !strings.Contains(w.Body.String(), "by hand") {
		t.Errorf("refusal: %d %s", w.Code, w.Body)
	}
	if got, _ := os.ReadFile(en.clients["claude-desktop"]); string(got) != `[1]` {
		t.Errorf("file changed: %s", got)
	}
	if c := status("claude-desktop"); c.Error == "" {
		t.Error("the problem is not reported in the state")
	}

	for body, code := range map[string]int{`{"client":"vscode"}`: 400, `{}`: 400, `{"client":"cursor","x":1}`: 400, `nope`: 400} {
		if w := post(body); w.Code != code {
			t.Errorf("%s: %d", body, w.Code)
		}
	}
}

func TestConnectGoesThroughTheGuard(t *testing.T) {
	en := setup(t)
	os.MkdirAll(filepath.Dir(en.clients["cursor"]), 0o755)
	body := `{"client":"cursor"}`
	for name, w := range map[string]*httptest.ResponseRecorder{
		"no token":    en.do("POST", "/api/connect", good, "", "application/json", body),
		"wrong token": en.do("POST", "/api/connect", good, "nope", "application/json", body),
		"bad host":    en.do("POST", "/api/connect", "evil.example", tok, "application/json", body),
		"bad type":    en.do("POST", "/api/connect", good, tok, "text/plain", body),
		"get":         en.do("GET", "/api/connect", good, tok, "", ""),
	} {
		if w.Code == 200 {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := os.Stat(en.clients["cursor"]); err == nil {
		t.Error("a config file was written without passing the guard")
	}
}

func TestPageHasTheWebChatsSection(t *testing.T) {
	body := setup(t).do("GET", "/", good, "", "", "").Body.String()
	for _, want := range []string{"Web chats", "Import an export", "Browser extension", "ChatGPT (web)", "Claude (web)", "Gemini (web)",
		"Data controls", "Settings, Privacy", "chrome://extensions", `id="ext-code"`, "readonly", "needs the browser extension or an import"} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}

// The page names the shell of every command and shows both OpenCode shapes.
func TestPageNamesShellsAndOpenCodeShapes(t *testing.T) {
	en := setup(t)
	page := en.do("GET", "/", good, "", "", "").Body.String()
	for _, want := range []string{`m.claude_code_shell`, `inside WSL (bash)`, `mounted under /mnt`,
		`{ mcp: { servers: { cortex: ocServer } } }`, `{ mcp: { cortex: Object.assign({ enabled: true }, ocServer) } }`} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}
