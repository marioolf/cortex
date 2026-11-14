package panel

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/marioolf/cortex/internal/config"
	"github.com/marioolf/cortex/internal/session"
)

// The browser extension's API. It is authenticated by the pairing code alone,
// not by the panel token: the extension cannot know that token, which is
// regenerated at every start and only handed to the page we serve.
const (
	maxIngest       = 5 << 20
	maxMessages     = 5000
	maxTitleChars   = 500
	pairingHeader   = "X-Cortex-Extension"
	routePing       = "/api/ext/ping"
	routeIngest     = "/api/ext/ingest"
	routeRegenerate = "/api/ext/regenerate" // an ordinary panel route, despite the prefix
	routeQuit       = "/api/ext/quit"       // for `cortex install`, which must stop a running Cortex and has no panel token
)

// hostsOf lists the sites each source may link to.
var hostsOf = map[string][]string{
	"chatgpt":   {"chatgpt.com", "chat.openai.com"},
	"claude-ai": {"claude.ai"},
	"gemini":    {"gemini.google.com"},
}

var idPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

func isExtRoute(path string) bool {
	return path == routePing || path == routeIngest || path == routeQuit
}

// extAllowed applies the extra rules of the extension routes. A web page must
// never reach them, even if it somehow learned the code, so a request that
// carries an Origin is only accepted from an extension's own origin. (The
// extension's worker, with host permission, sends its own origin.) No CORS
// headers are ever sent, so browsers also refuse to show a page the answer.
func (p *panel) extAllowed(r *http.Request) bool {
	for _, o := range r.Header.Values("Origin") {
		if !strings.HasPrefix(o, "chrome-extension://") && !strings.HasPrefix(o, "moz-extension://") {
			return false
		}
	}
	want := p.e.ExtensionToken()
	if want == "" { // no code yet: closed
		return false
	}
	return subtle.ConstantTimeCompare([]byte(r.Header.Get(pairingHeader)), []byte(want)) == 1
}

func (p *panel) extPing(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "version": p.e.Version})
}

// extQuit stops Cortex for a caller that holds the pairing code: the installer
// reads it from the config file. Whoever has the code can already add notes, and
// anything that can read the file can end the process anyway.
func (p *panel) extQuit(w http.ResponseWriter, r *http.Request) {
	if p.opt.Quit == nil {
		jsonError(w, "not available", http.StatusNotImplemented)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	go p.opt.Quit()
}

type ingestMessage struct {
	Role string `json:"role"`
	Text string `json:"text"`
	Time string `json:"time"`
}

type ingestRequest struct {
	Source   string          `json:"source"`
	ID       string          `json:"id"`
	Title    string          `json:"title"`
	URL      string          `json:"url"`
	Messages []ingestMessage `json:"messages"`
}

func (p *panel) extIngest(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body) // r.Body is capped by the guard
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			jsonError(w, "body too large", http.StatusRequestEntityTooLarge)
			return
		}
		jsonError(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	var req ingestRequest
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		jsonError(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	if dec.More() {
		jsonError(w, "bad request: trailing data", http.StatusBadRequest)
		return
	}
	s, err := req.session()
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	res, err := p.e.Ingest(s, false)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"result": res})
}

// session validates the request and turns it into a session.
func (req *ingestRequest) session() (*session.Session, error) {
	hosts, ok := hostsOf[req.Source]
	if !ok {
		return nil, errors.New("unknown source")
	}
	if !idPattern.MatchString(req.ID) {
		return nil, errors.New("invalid id")
	}
	if n := len(req.Messages); n < 1 || n > maxMessages {
		return nil, errors.New("messages: expected between 1 and 5000")
	}
	s := &session.Session{Source: req.Source, ID: req.ID, Title: cutChars(strings.TrimSpace(req.Title), maxTitleChars)}
	if req.URL != "" {
		u, err := url.Parse(req.URL)
		if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || !hostIn(u.Hostname(), hosts) {
			return nil, errors.New("url: must be an https address of the source's site")
		}
		s.URL = u.String()
	}
	for _, m := range req.Messages {
		if m.Role != "user" && m.Role != "assistant" {
			return nil, errors.New("messages: role must be user or assistant")
		}
		var t time.Time
		if m.Time != "" {
			var err error
			if t, err = time.Parse(time.RFC3339, m.Time); err != nil {
				return nil, errors.New("messages: time must be RFC 3339")
			}
		}
		if strings.TrimSpace(m.Text) == "" {
			continue // an empty bubble adds only a bare heading
		}
		s.Messages = append(s.Messages, session.Message{Role: m.Role, Text: m.Text, Time: t})
	}
	if len(s.Messages) == 0 {
		return nil, errors.New("messages: no text")
	}
	if s.Title == "" {
		first, _, _ := strings.Cut(strings.TrimSpace(s.Messages[0].Text), "\n")
		s.Title = cutChars(strings.TrimSpace(first), maxTitleChars)
	}
	return s, nil
}

func hostIn(host string, hosts []string) bool {
	for _, h := range hosts {
		if strings.EqualFold(host, h) {
			return true
		}
	}
	return false
}

// cutChars shortens s to at most n characters.
func cutChars(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// extRegenerate replaces the pairing code; the old one stops working at once,
// so the extension has to be given the new one.
func (p *panel) extRegenerate(w http.ResponseWriter, r *http.Request) {
	code, err := config.NewToken()
	if err == nil {
		err = p.e.Update(func(c *config.Config) { c.ExtensionToken = code })
	}
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, p.snapshot())
}
