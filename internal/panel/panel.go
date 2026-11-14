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
	"fmt"
	"mime"
	"net/http"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/marioolf/cortex/internal/autostart"
	"github.com/marioolf/cortex/internal/config"
	"github.com/marioolf/cortex/internal/engine"
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
}

type panel struct {
	e     *engine.Engine
	token string
	opt   Options
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
	p := &panel{e, token, opt}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", p.page)
	mux.HandleFunc("GET /api/state", p.state)
	mux.HandleFunc("POST /api/config", p.config)
	mux.HandleFunc("POST /api/open-vault", p.openVault)
	return p.guard(mux)
}

// guard runs before every handler, so no route can forget a check.
func (p *panel) guard(next http.Handler) http.Handler {
	hosts := []string{fmt.Sprintf("127.0.0.1:%d", p.opt.Port), fmt.Sprintf("localhost:%d", p.opt.Port)}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Cache-Control", "no-store")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")

		// A DNS-rebinding page reaches us under its own name, not ours.
		if !slices.Contains(hosts, r.Host) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			got := r.Header.Get("X-Cortex-Token")
			if subtle.ConstantTimeCompare([]byte(got), []byte(p.token)) != 1 {
				jsonError(w, "forbidden", http.StatusForbidden)
				return
			}
		}
		if r.Method == http.MethodPost {
			if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
				jsonError(w, "Content-Type must be application/json", http.StatusUnsupportedMediaType)
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, maxBody)
		}
		next.ServeHTTP(w, r)
	})
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

func (p *panel) snapshot() engine.State {
	s := p.e.Snapshot()
	s.Autostart = p.opt.AutostartEnabled()
	return s
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
}

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

	changed := req.Paused != nil || req.Vault != nil || req.Source != nil || req.Project != nil
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

func (p *panel) openVault(w http.ResponseWriter, r *http.Request) {
	if err := p.opt.Open(p.e.Snapshot().Vault); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	writeJSON(w, code, map[string]string{"error": msg})
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
