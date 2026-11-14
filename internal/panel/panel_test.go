package panel

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	autostart bool
	opened    []string
}

func setup(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	if err := (&config.Config{Vault: filepath.Join(dir, "vault")}).Save(cfgPath); err != nil {
		t.Fatal(err)
	}
	e, err := engine.New(cfgPath, []collector.Collector{fakeSource{}})
	if err != nil {
		t.Fatal(err)
	}
	e.Sync()
	en := &env{cfgPath: cfgPath}
	en.h = New(e, tok, Options{
		Port:             47999,
		AutostartEnabled: func() bool { return en.autostart },
		SetAutostart:     func(on bool) error { en.autostart = on; return nil },
		Open:             func(p string) error { en.opened = append(en.opened, p); return nil },
	})
	return en
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
