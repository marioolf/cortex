// Package panel serves the local control panel: one embedded page and a small
// JSON API on top of the engine. It only ever listens on the loopback
// interface, but a browser can be talked into sending requests there (other
// websites, DNS rebinding), so every request is checked in guard.
package panel

import (
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/marioolf/cortex/internal/autostart"
	"github.com/marioolf/cortex/internal/config"
	"github.com/marioolf/cortex/internal/connect"
	"github.com/marioolf/cortex/internal/engine"
	"github.com/marioolf/cortex/internal/wsl"
)

//go:embed page.html
var page string

const maxBody = 64 << 10

// Options holds what the panel needs besides the engine. Nil functions fall
// back to the real system; tests pass fakes so they never touch it.
type Options struct {
	Port             int // the port we listen on, for the Host check
	AutostartEnabled func() bool
	SetAutostart     func(on bool) error
	Open             func(target string) error // opens a folder or URL in the OS
	Quit             func()                    // stops the whole process; nil hides nothing, the request just fails
	Now              func() time.Time          // clock for the autostart cache; nil means time.Now

	// For the "Connect your AI tools" section.
	Exe          func() (string, error) // path of this program; nil means the real one
	ClientPaths  map[string]string      // config file of each client (see connect.Paths); nil means the real ones
	GOOS         string                 // OS to build the command lines for; empty means runtime.GOOS
	WSLAvailable func() bool            // whether WSL distros exist; nil asks wsl.exe

	// For the install banner (Windows only; nil means not supported).
	InstallState func() InstallState
	// Install installs and arranges the hand-over; the panel then quits. A
	// warning is a partial failure (the program was copied, but the shortcut or
	// the login entry was not): the page shows it, and the hand-over still happens.
	Install func() (warning string, err error)

	// For importing exports.
	MaxImport int64  // largest upload in bytes; 0 means 2 GiB
	TempDir   string // where uploads are spooled; empty means the OS temp directory
}

// Header marks every response as coming from Cortex, so that a second instance
// can tell our port from some other program's.
const Header = "X-Cortex"

// autostartTTL is how long the autostart setting is reused: the page polls
// /api/state, and on Windows each lookup spawns reg.exe.
const autostartTTL = 30 * time.Second

type panel struct {
	e     *engine.Engine
	token string
	opt   Options

	connectMu  sync.Mutex  // one edit of a client's config file at a time
	importing  atomic.Bool // one import at a time
	installing atomic.Bool // one install at a time

	mu        sync.Mutex
	autostart bool
	autoAt    time.Time
	haveAuto  bool
}

func New(e *engine.Engine, token string, opt Options) http.Handler {
	if opt.AutostartEnabled == nil {
		opt.AutostartEnabled = autostart.Enabled
	}
	if opt.SetAutostart == nil {
		opt.SetAutostart = func(on bool) error {
			if on {
				return autostart.Enable()
			}
			return autostart.Disable()
		}
	}
	if opt.Open == nil {
		opt.Open = Open
	}
	if opt.Now == nil {
		opt.Now = time.Now
	}
	if opt.Exe == nil {
		opt.Exe = connect.Executable
	}
	if opt.ClientPaths == nil {
		opt.ClientPaths = connect.Paths()
	}
	if opt.GOOS == "" {
		opt.GOOS = runtime.GOOS
	}
	if opt.WSLAvailable == nil {
		opt.WSLAvailable = func() bool { return len(wsl.Roots()) > 0 }
	}
	if opt.MaxImport <= 0 {
		opt.MaxImport = defaultMaxImport
	}
	p := &panel{e: e, token: token, opt: opt}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", p.page)
	mux.HandleFunc("GET /api/state", p.state)
	mux.HandleFunc("POST /api/config", p.config)
	mux.HandleFunc("POST /api/open-vault", p.openVault)
	mux.HandleFunc("POST /api/connect", p.connect)
	mux.HandleFunc("POST /api/import", p.importFile)
	mux.HandleFunc("GET "+routePing, p.extPing)
	mux.HandleFunc("POST "+routeIngest, p.extIngest)
	mux.HandleFunc("POST "+routeRegenerate, p.extRegenerate)
	mux.HandleFunc("POST /api/install", p.install)
	mux.HandleFunc("POST "+routeQuit, p.extQuit)
	mux.HandleFunc("POST /api/quit", p.quit)
	return p.guard(mux)
}

// guard runs before every handler, so no route can forget a check.
func (p *panel) guard(next http.Handler) http.Handler {
	hosts := []string{fmt.Sprintf("127.0.0.1:%d", p.opt.Port), fmt.Sprintf("localhost:%d", p.opt.Port)}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set(Header, "1")
		h.Set("Cache-Control", "no-store")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")

		// A DNS-rebinding page reaches us under its own name, not ours.
		if !slices.Contains(hosts, r.Host) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		switch {
		case isExtRoute(r.URL.Path):
			// The pairing code, not the panel token: see ext.go.
			if !p.extAllowed(r) {
				jsonError(w, "forbidden", http.StatusForbidden)
				return
			}
		case strings.HasPrefix(r.URL.Path, "/api/"):
			got := r.Header.Get("X-Cortex-Token")
			if subtle.ConstantTimeCompare([]byte(got), []byte(p.token)) != 1 {
				jsonError(w, "forbidden", http.StatusForbidden)
				return
			}
		}
		if r.Method == http.MethodPost && !limitPost(w, r) {
			return
		}
		next.ServeHTTP(w, r)
	})
}

// limitPost checks the Content-Type of a POST and caps its body. Everything is
// JSON and small, except an uploaded export (any of the types a browser may
// give a zip or json file, with the handler's own cap) and the extension's
// conversations (5 MB). It reports whether the request may go on.
func limitPost(w http.ResponseWriter, r *http.Request) bool {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	max, ok := int64(maxBody), mt == "application/json"
	switch r.URL.Path {
	case "/api/import":
		max, ok = -1, ok || mt == "application/zip" || mt == "application/octet-stream"
	case routeIngest:
		max = maxIngest
	}
	if err != nil || !ok {
		msg := "Content-Type must be application/json"
		if r.URL.Path == "/api/import" {
			msg = "Content-Type must be application/zip, application/json or application/octet-stream"
		}
		jsonError(w, msg, http.StatusUnsupportedMediaType)
		return false
	}
	if max > 0 {
		r.Body = http.MaxBytesReader(w, r.Body, max)
	}
	return true
}

func (p *panel) page(w http.ResponseWriter, r *http.Request) {
	var b [16]byte
	rand.Read(b[:])
	nonce := hex.EncodeToString(b[:])
	// The page's own script and style are the only things allowed to run.
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'nonce-"+nonce+"'; style-src 'nonce-"+nonce+"'; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	strings.NewReplacer("{{NONCE}}", nonce, "{{TOKEN}}", p.token).WriteString(w, page)
}

// stateJSON is /api/state: the engine's state plus what the page needs to
// connect AI tools.
type stateJSON struct {
	engine.State
	MCP       mcpState     `json:"mcp"`
	Extension extState     `json:"extension"`
	Install   InstallState `json:"install"`
}

// InstallState tells the page whether to offer installing (see package install).
// RunningFrom is the folder of the running program.
type InstallState struct {
	Supported   bool   `json:"supported"`
	Installed   bool   `json:"installed"`
	RunningFrom string `json:"running_from"`
	Target      string `json:"target"`
}

// extState is what the page shows to pair the browser extension. The code is
// safe to show here: the state is behind the panel token and local.
type extState struct {
	Code string `json:"code"`
	Port int    `json:"port"`
}

type mcpState struct {
	Command       string        `json:"command"` // empty when our own path is unknown
	Args          []string      `json:"args"`
	ClaudeCode    string        `json:"claude_code"`               // command line for Claude Code
	ClaudeShell   string        `json:"claude_code_shell"`         // the shell that line is quoted for
	ClaudeCodeWSL string        `json:"claude_code_wsl,omitempty"` // the same, to run inside WSL (bash)
	Clients       []clientState `json:"clients"`
}

type clientState struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Path      string `json:"path"`
	Available bool   `json:"available"` // its config folder exists
	Status    string `json:"status"`    // "", "connected" or "outdated"
	Error     string `json:"error,omitempty"`
}

func (p *panel) snapshot() stateJSON {
	s := p.e.Snapshot()
	s.Autostart = p.autostartEnabled(false)
	var in InstallState
	if p.opt.InstallState != nil {
		in = p.opt.InstallState()
	}
	return stateJSON{s, p.mcpState(), extState{p.e.ExtensionToken(), p.opt.Port}, in}
}

func (p *panel) mcpState() mcpState {
	m := mcpState{Args: []string{"mcp"}, Clients: []clientState{}}
	exe, err := p.opt.Exe()
	if err != nil || exe == "" {
		return m
	}
	m.Command = exe
	m.ClaudeCode = connect.ClaudeCodeCommand(exe, p.opt.GOOS)
	m.ClaudeShell = "a terminal (bash or zsh)"
	if p.opt.GOOS == "windows" {
		m.ClaudeShell = "PowerShell"
	}
	if p.opt.GOOS == "windows" {
		// The Windows program started from WSL finds its own config through
		// interop, so no -vault is needed.
		if w, ok := connect.WSLPath(exe); ok && p.opt.WSLAvailable() {
			m.ClaudeCodeWSL = connect.ClaudeCodeCommand(w, "linux")
		}
	}
	want := connect.Entry{Command: exe, Args: m.Args}
	for _, c := range connect.Clients {
		path := p.opt.ClientPaths[c.ID]
		cs := clientState{ID: c.ID, Name: c.Name, Path: path, Available: connect.Available(path)}
		if cs.Available {
			var err error
			if cs.Status, err = connect.Status(path, want); err != nil {
				cs.Error = err.Error()
			}
		}
		m.Clients = append(m.Clients, cs)
	}
	return m
}

// autostartEnabled returns the cached setting, looking it up again when it is
// older than autostartTTL or when fresh is set.
func (p *panel) autostartEnabled(fresh bool) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if fresh || !p.haveAuto || p.opt.Now().Sub(p.autoAt) >= autostartTTL {
		p.autostart, p.autoAt, p.haveAuto = p.opt.AutostartEnabled(), p.opt.Now(), true
	}
	return p.autostart
}

func (p *panel) state(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, p.snapshot())
}

type configRequest struct {
	Paused    *bool   `json:"paused"`
	Vault     *string `json:"vault"`
	Autostart *bool   `json:"autostart"`
	Source    *struct {
		ID      string `json:"id"`
		Enabled bool   `json:"enabled"`
	} `json:"source"`
	Project *struct {
		Name     string `json:"name"`
		Excluded bool   `json:"excluded"`
	} `json:"project"`
	Summaries *summariesRequest `json:"summaries"`
}

// summariesRequest is all the panel may change about summaries. There is no
// field for the command, the idle time or the rate: a command settable here
// would turn a leaked panel token into code execution, so it can only be
// written by hand in the config file. Unknown fields are rejected.
type summariesRequest struct {
	Enabled  *bool     `json:"enabled"`
	Tool     *string   `json:"tool"`
	Projects *[]string `json:"projects"`
}

// checkSummaries refuses a request the engine could not honour, with a
// message for the person: a tool that is not there, no tool at all.
func (p *panel) checkSummaries(r *summariesRequest) error {
	cur := p.e.Snapshot().Summaries
	tool := cur.Tool
	if r.Tool != nil {
		tool = *r.Tool
		switch {
		case tool == "":
		case tool == config.ToolCustom:
			if !cur.CustomAvailable {
				return errors.New(`there is no custom command in the config file; it can only be written there by hand`)
			}
		case !slices.ContainsFunc(cur.Presets, func(x engine.SummaryPreset) bool { return x.ID == tool && x.Available }):
			return fmt.Errorf("%q is not installed (its program was not found on PATH) or is unknown", tool)
		}
	}
	if r.Enabled != nil && *r.Enabled && tool == "" {
		return errors.New("choose a tool first")
	}
	if r.Projects != nil {
		for _, name := range *r.Projects {
			if strings.TrimSpace(name) == "" {
				return errors.New("project name is empty")
			}
		}
	}
	return nil
}

// errInstallFirst is what the autostart switch answers on a copy that is not
// the installed one: the entry would point into Downloads.
const errInstallFirst = "Install Cortex first"

func (p *panel) config(w http.ResponseWriter, r *http.Request) {
	var req configRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		jsonError(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}

	if req.Vault != nil {
		v := strings.TrimSpace(*req.Vault)
		if !filepath.IsAbs(v) {
			jsonError(w, "the vault must be an absolute path", http.StatusBadRequest)
			return
		}
		req.Vault = &v
	}
	if req.Source != nil && !slices.ContainsFunc(p.e.Snapshot().Sources, func(s engine.SourceState) bool { return s.ID == req.Source.ID }) {
		jsonError(w, "unknown source", http.StatusBadRequest)
		return
	}
	if req.Project != nil && req.Project.Name == "" {
		jsonError(w, "project name is empty", http.StatusBadRequest)
		return
	}

	if req.Autostart != nil && *req.Autostart && p.opt.InstallState != nil {
		if in := p.opt.InstallState(); in.Supported && !in.Installed {
			jsonError(w, errInstallFirst, http.StatusConflict)
			return
		}
	}
	if req.Summaries != nil {
		if p.e.Snapshot().Summaries.Invalid {
			jsonError(w, "the summaries settings in the config file are not valid; fix the file first (see the warning at the top of the page)", http.StatusConflict)
			return
		}
		if err := p.checkSummaries(req.Summaries); err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
	}

	changed := req.Paused != nil || req.Vault != nil || req.Source != nil || req.Project != nil || req.Summaries != nil
	if changed {
		err := p.e.Update(func(c *config.Config) {
			if req.Paused != nil {
				c.Paused = *req.Paused
			}
			if req.Vault != nil {
				c.Vault = *req.Vault
			}
			if s := req.Source; s != nil {
				c.DisabledSources = toggle(c.DisabledSources, s.ID, !s.Enabled)
			}
			if pr := req.Project; pr != nil {
				c.ExcludedProjects = toggle(c.ExcludedProjects, pr.Name, pr.Excluded)
			}
			if sm := req.Summaries; sm != nil {
				if sm.Enabled != nil {
					c.Summaries.Enabled = *sm.Enabled
				}
				if sm.Tool != nil {
					c.Summaries.Tool = *sm.Tool
				}
				if sm.Projects != nil {
					c.Summaries.Projects = slices.Clone(*sm.Projects)
				}
			}
		})
		if err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	if req.Autostart != nil {
		if err := p.opt.SetAutostart(*req.Autostart); err != nil {
			jsonError(w, "autostart: "+err.Error(), http.StatusInternalServerError)
			return
		}
		p.autostartEnabled(true) // show the new value now, not in 30 seconds
	}
	if changed {
		go p.e.Sync() // so the panel shows the effect without waiting for the next tick
	}
	writeJSON(w, http.StatusOK, p.snapshot())
}

// toggle adds or removes v from list.
func toggle(list []string, v string, present bool) []string {
	list = slices.DeleteFunc(slices.Clone(list), func(s string) bool { return s == v })
	if present {
		list = append(list, v)
	}
	return list
}

// connect adds Cortex to the config file of one client.
func (p *panel) connect(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Client string `json:"client"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		jsonError(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	i := slices.IndexFunc(connect.Clients, func(c connect.Client) bool { return c.ID == req.Client })
	if i < 0 {
		jsonError(w, "unknown client", http.StatusBadRequest)
		return
	}
	name, path := connect.Clients[i].Name, p.opt.ClientPaths[req.Client]
	if !connect.Available(path) {
		jsonError(w, name+" does not seem to be installed (no "+filepath.Dir(path)+" folder); paste the snippet by hand instead", http.StatusBadRequest)
		return
	}
	exe, err := p.opt.Exe()
	if err != nil || exe == "" {
		jsonError(w, "cannot tell where Cortex is installed", http.StatusInternalServerError)
		return
	}
	p.connectMu.Lock()
	err = connect.Add(path, connect.Entry{Command: exe, Args: []string{"mcp"}})
	p.connectMu.Unlock()
	if err != nil {
		jsonError(w, name+": "+err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, p.snapshot())
}

func (p *panel) openVault(w http.ResponseWriter, r *http.Request) {
	if err := p.opt.Open(p.e.Snapshot().Vault); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// quit is how the user stops Cortex when it runs without a console window.
func (p *panel) quit(w http.ResponseWriter, r *http.Request) {
	if p.opt.Quit == nil {
		jsonError(w, "not available", http.StatusNotImplemented)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	go p.opt.Quit() // after the response is on its way
}

// install copies Cortex to its permanent place and hands over to that copy: the
// response goes out, then the process quits and the installed copy starts (it
// has a new panel token, so the page cannot go on talking to this one).
func (p *panel) install(w http.ResponseWriter, r *http.Request) {
	if p.opt.Install == nil || p.opt.Quit == nil {
		jsonError(w, "not available", http.StatusNotImplemented)
		return
	}
	if !p.installing.CompareAndSwap(false, true) {
		jsonError(w, "an install is already in progress", http.StatusConflict)
		return
	}
	warning, err := p.opt.Install()
	if err != nil {
		p.installing.Store(false)
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := struct {
		OK      bool   `json:"ok"`
		Warning string `json:"warning,omitempty"`
	}{OK: true, Warning: warning}
	writeJSON(w, http.StatusOK, out)
	go p.opt.Quit()
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// Running reports whether url is served by a Cortex panel: whatever status it
// answers with, it carries our header. Anything else (no listener, another
// program) is false.
func Running(url string, timeout time.Duration) bool {
	c := http.Client{Timeout: timeout}
	resp, err := c.Get(url)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.Header.Get(Header) != ""
}

// RunningExe asks the Cortex whose panel is at url which program file it runs
// from and which version it is, proving itself with the pairing code. It fails when the answer is not
// a Cortex's, the code is wrong or missing, or the Cortex is older than this
// field; the caller must then treat the running Cortex as unknown.
func RunningExe(url, code string, timeout time.Duration) (exe, version string, err error) {
	req, err := http.NewRequest(http.MethodGet, url+strings.TrimPrefix(routePing, "/"), nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set(pairingHeader, code)
	c := http.Client{Timeout: timeout}
	resp, err := c.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("it did not accept the pairing code (HTTP %d)", resp.StatusCode)
	}
	var ping struct {
		Exe     string `json:"exe"`
		Version string `json:"version"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&ping); err != nil {
		return "", "", fmt.Errorf("unexpected answer: %w", err)
	}
	if ping.Exe == "" {
		return "", "", errors.New("it does not say where it runs from (an older version)")
	}
	return ping.Exe, ping.Version, nil
}

// QuitRunning asks the Cortex whose panel is at url to quit, proving itself with
// the pairing code, and waits up to wait for it to stop answering. It does
// nothing when nothing answers like Cortex there.
func QuitRunning(url, code string, wait time.Duration) error {
	if !Running(url, 2*time.Second) {
		return nil
	}
	req, err := http.NewRequest(http.MethodPost, url+strings.TrimPrefix(routeQuit, "/"), strings.NewReader("{}"))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(pairingHeader, code)
	c := http.Client{Timeout: 5 * time.Second}
	resp, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("could not ask the running Cortex at %s to quit: %w", url, err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("a Cortex is running at %s and did not accept the request to quit (HTTP %d); quit it from its tray icon or panel and try again", url, resp.StatusCode)
	}
	for deadline := time.Now().Add(wait); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if !Running(url, time.Second) {
			return nil
		}
	}
	return fmt.Errorf("the Cortex at %s is taking too long to quit; quit it from its tray icon or panel and try again", url)
}

// Open shows a folder or URL with the OS default handler.
func Open(target string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("explorer", target)
	case "darwin":
		cmd = exec.Command("open", target)
	default:
		cmd = exec.Command("xdg-open", target)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait() // explorer exits non-zero even on success; we only reap it
	return nil
}
