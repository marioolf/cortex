// Package copilot reads GitHub Copilot Chat sessions saved by VS Code (and its
// Insiders/VSCodium variants), one file per session under
// <User dir>/workspaceStorage/<hash>/chatSessions/ or
// <User dir>/globalStorage/emptyWindowChatSessions/.
//
// Older VS Code versions write a single JSON snapshot (.json); newer ones write
// an append-only JSONL mutation log (.jsonl) that must be replayed. Recent
// builds also keep a turn-level copy in globalStorage/github.copilot-chat/
// session-store.db (see sessionstore.go).
package copilot

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/marioolf/cortex/internal/collector"
	"github.com/marioolf/cortex/internal/session"
	"github.com/marioolf/cortex/internal/wsl"
)

const ID = "copilot-vscode"

type Collector struct {
	Roots []string // VS Code "User" directories
	// extraRoots is evaluated on every call because the set of running WSL
	// distros changes while Cortex runs.
	extraRoots func() []string
	seen       collector.Seen
	// fileIDs remembers the session id of every chatSessions file parsed so
	// far (path -> id), so ids of files skipped as unchanged still count.
	fileIDs map[string]string
}

var products = []string{"Code", "Code - Insiders", "VSCodium"}

func New() *Collector {
	var roots []string
	if base, err := os.UserConfigDir(); err == nil {
		for _, p := range products {
			roots = append(roots, filepath.Join(base, p, "User"))
		}
	}
	return &Collector{Roots: roots, extraRoots: func() []string {
		var homes []string
		if runtime.GOOS == "linux" {
			if h, err := os.UserHomeDir(); err == nil {
				homes = append(homes, h)
			}
		}
		homes = append(homes, wsl.Homes()...)
		var r []string
		for _, h := range homes {
			r = append(r,
				filepath.Join(h, ".vscode-server", "data", "User"),
				filepath.Join(h, ".vscode-server-insiders", "data", "User"))
		}
		return r
	}}
}

func (c *Collector) ID() string { return ID }

func (c *Collector) Detect() []string {
	roots := c.Roots
	if c.extraRoots != nil {
		roots = append(roots[:len(roots):len(roots)], c.extraRoots()...)
	}
	var found []string
	for _, r := range roots {
		if st, err := os.Stat(r); err == nil && st.IsDir() {
			found = append(found, r)
		}
	}
	return found
}

func (c *Collector) Reset() { c.seen.Reset(); c.fileIDs = nil }

// Collect skips files whose modification time and size are unchanged since
// they were last parsed.
func (c *Collector) Collect(fn func(*session.Session), warn func(error)) {
	for _, root := range c.Detect() {
		for _, f := range sessionFiles(root) {
			changed, err := c.seen.Changed(f.path)
			if err != nil {
				warn(fmt.Errorf("%s: %w", f.path, err))
				continue
			}
			if !changed {
				continue
			}
			s, err := ParseFile(f.path)
			if err != nil {
				warn(fmt.Errorf("%s: %w", f.path, err))
				continue
			}
			c.seen.Mark(f.path)
			if len(s.Messages) > 0 {
				if c.fileIDs == nil {
					c.fileIDs = map[string]string{}
				}
				c.fileIDs[f.path] = s.ID
			} else {
				delete(c.fileIDs, f.path)
			}
			if f.workspaceDir != "" {
				s.Cwd = workspaceCwd(f.workspaceDir)
			}
			if len(s.Messages) > 0 {
				fn(s)
			}
		}
		c.collectStore(root, fn, warn)
	}
}

type file struct {
	path         string
	workspaceDir string // <hash> directory; empty for empty-window sessions
}

func sessionFiles(root string) []file {
	var out []file
	for _, ext := range []string{"*.json", "*.jsonl"} {
		ws, _ := filepath.Glob(filepath.Join(root, "workspaceStorage", "*", "chatSessions", ext))
		for _, p := range ws {
			out = append(out, file{p, filepath.Dir(filepath.Dir(p))})
		}
		ew, _ := filepath.Glob(filepath.Join(root, "globalStorage", "emptyWindowChatSessions", ext))
		for _, p := range ew {
			out = append(out, file{path: p})
		}
	}
	return out
}

// workspaceCwd reads <hash>/workspace.json and turns its folder (or
// multi-root workspace file) URI into a plain path. Unknown shapes give "".
func workspaceCwd(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, "workspace.json"))
	if err != nil {
		return ""
	}
	var w struct {
		Folder    string `json:"folder"`
		Workspace string `json:"workspace"`
	}
	if json.Unmarshal(b, &w) != nil {
		return ""
	}
	if w.Folder != "" {
		return uriToPath(w.Folder)
	}
	if p := uriToPath(w.Workspace); p != "" {
		// A multi-root workspace is a .code-workspace file; its directory is the closest thing to a project.
		if strings.Contains(p, `\`) && !strings.HasPrefix(p, "/") {
			return p[:max(strings.LastIndex(p, `\`), 0)]
		}
		return path.Dir(p)
	}
	return ""
}

var driveRe = regexp.MustCompile(`^/[A-Za-z]:(/|$)`)

func uriToPath(raw string) string {
	// net/url rejects the escaped "+" in remote authorities (wsl%2Bubuntu), so split those by hand.
	if rest, ok := strings.CutPrefix(raw, "vscode-remote://"); ok {
		_, p, found := strings.Cut(rest, "/")
		if !found {
			return ""
		}
		if dec, err := url.PathUnescape("/" + p); err == nil {
			return dec
		}
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	switch u.Scheme {
	case "file":
		p := u.Path
		if driveRe.MatchString(p) {
			p = strings.ToUpper(p[1:2]) + p[2:]
			return strings.ReplaceAll(p, "/", `\`)
		}
		if u.Host != "" && u.Host != "localhost" { // UNC share, e.g. wsl.localhost
			return `\\` + u.Host + strings.ReplaceAll(p, "/", `\`)
		}
		return p
	}
	return ""
}

type request struct {
	Message   json.RawMessage   `json:"message"`
	Response  []json.RawMessage `json:"response"`
	Timestamp int64             `json:"timestamp"`
}

type part struct {
	Kind              string          `json:"kind"`
	Value             json.RawMessage `json:"value"`
	Content           json.RawMessage `json:"content"`
	ToolID            string          `json:"toolId"`
	InvocationMessage json.RawMessage `json:"invocationMessage"`
	PastTenseMessage  json.RawMessage `json:"pastTenseMessage"`
}

func ParseFile(p string) (*session.Session, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var s *session.Session
	if strings.HasSuffix(p, ".jsonl") {
		s, err = parseLog(b)
	} else {
		s, err = parseSnapshot(b)
	}
	if err != nil {
		return nil, err
	}
	if s.ID == "" {
		s.ID = strings.TrimSuffix(filepath.Base(p), filepath.Ext(p))
	}
	return s, nil
}

func parseSnapshot(b []byte) (*session.Session, error) {
	var state any
	if err := json.Unmarshal(b, &state); err != nil {
		return nil, err
	}
	return fromState(state)
}

// parseLog replays the mutation log: entry kinds are 0 initial snapshot,
// 1 set, 2 push (optionally truncating to length i first), 3 delete. A bad
// line is skipped; a log without a snapshot is an error.
func parseLog(b []byte) (*session.Session, error) {
	var state any
	have := false
	for _, line := range bytes.Split(b, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var e struct {
			Kind int             `json:"kind"`
			K    []any           `json:"k"`
			V    json.RawMessage `json:"v"`
			I    *int            `json:"i"`
		}
		if json.Unmarshal(line, &e) != nil {
			continue
		}
		var v any
		if len(e.V) > 0 {
			_ = json.Unmarshal(e.V, &v)
		}
		if e.Kind == 0 {
			state, have = v, true
			continue
		}
		if !have || len(e.K) == 0 {
			continue
		}
		state = apply(state, e.K, func(old any) any {
			switch e.Kind {
			case 1:
				return v
			case 2:
				arr, _ := old.([]any)
				if e.I != nil && *e.I >= 0 && *e.I < len(arr) {
					arr = arr[:*e.I]
				}
				items, _ := v.([]any)
				return append(arr[:len(arr):len(arr)], items...)
			case 3:
				return deleted{}
			}
			return old
		})
	}
	if !have {
		return nil, fmt.Errorf("log has no initial snapshot")
	}
	return fromState(state)
}

type deleted struct{}

// apply walks path inside node and replaces the target with fn(old). Containers
// are rebuilt on the way back because Go slices cannot grow in place.
func apply(node any, path []any, fn func(any) any) any {
	if len(path) == 0 {
		return fn(node)
	}
	switch key := path[0].(type) {
	case string:
		m, ok := node.(map[string]any)
		if !ok {
			if node != nil {
				return node
			}
			m = map[string]any{}
		}
		nv := apply(m[key], path[1:], fn)
		if _, del := nv.(deleted); del {
			delete(m, key)
		} else {
			m[key] = nv
		}
		return m
	case float64:
		a, ok := node.([]any)
		i := int(key)
		if !ok || i < 0 || i >= len(a) {
			return node
		}
		nv := apply(a[i], path[1:], fn)
		if _, del := nv.(deleted); del {
			nv = nil
		}
		a[i] = nv
		return a
	}
	return node
}

func fromState(state any) (*session.Session, error) {
	b, err := json.Marshal(state)
	if err != nil {
		return nil, err
	}
	var d struct {
		SessionID     string    `json:"sessionId"`
		CustomTitle   string    `json:"customTitle"`
		ComputedTitle string    `json:"computedTitle"`
		Requests      []request `json:"requests"`
	}
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, err
	}
	s := &session.Session{Source: ID, ID: d.SessionID}
	for _, r := range d.Requests {
		var ts time.Time
		if r.Timestamp > 0 {
			ts = time.UnixMilli(r.Timestamp)
		}
		if t := strings.TrimSpace(promptText(r.Message)); t != "" {
			s.Messages = append(s.Messages, session.Message{Role: "user", Text: t, Time: ts})
		}
		// The response has no timestamp of its own; the request's is the nearest.
		if t := strings.TrimSpace(responseText(r.Response)); t != "" {
			s.Messages = append(s.Messages, session.Message{Role: "assistant", Text: t, Time: ts})
		}
	}
	switch {
	case strings.TrimSpace(d.CustomTitle) != "":
		s.Title = strings.TrimSpace(d.CustomTitle)
	case strings.TrimSpace(d.ComputedTitle) != "":
		s.Title = strings.TrimSpace(d.ComputedTitle)
	default:
		for _, m := range s.Messages {
			if m.Role == "user" {
				s.Title, _, _ = strings.Cut(m.Text, "\n")
				break
			}
		}
	}
	return s, nil
}

// promptText accepts the old format (a plain string) and the new one (an
// object whose "text" is what the user typed).
func promptText(raw json.RawMessage) string {
	var str string
	if json.Unmarshal(raw, &str) == nil {
		return str
	}
	var m struct {
		Text string `json:"text"`
	}
	_ = json.Unmarshal(raw, &m)
	return m.Text
}

// textOf reads a field that is either a string or a markdown object with "value".
func textOf(raw json.RawMessage) string {
	var str string
	if json.Unmarshal(raw, &str) == nil {
		return str
	}
	var m struct {
		Value string `json:"value"`
	}
	_ = json.Unmarshal(raw, &m)
	return m.Value
}

// responseText keeps markdown text and a line per tool call. Thinking, tool
// output, references and progress messages are dropped.
func responseText(parts []json.RawMessage) string {
	var blocks []string
	prevMD := false
	for _, raw := range parts {
		var p part
		if json.Unmarshal(raw, &p) != nil {
			continue
		}
		switch p.Kind {
		case "", "markdownContent":
			// Streaming splits one paragraph over several parts, so adjacent ones are concatenated.
			t := textOf(p.Content)
			if t == "" {
				t = textOf(p.Value)
			}
			if t == "" {
				continue
			}
			if prevMD {
				blocks[len(blocks)-1] += t
			} else {
				blocks = append(blocks, t)
			}
			prevMD = true
		case "toolInvocationSerialized":
			blocks = append(blocks, toolLine(p))
			prevMD = false
		}
	}
	var out []string
	for _, b := range blocks {
		if b = strings.TrimSpace(b); b != "" {
			out = append(out, b)
		}
	}
	return strings.Join(out, "\n\n")
}

func toolLine(p part) string {
	name := p.ToolID
	if name == "" {
		name = "tool"
	}
	line := "> `" + name + "`"
	desc := strings.TrimSpace(textOf(p.PastTenseMessage))
	if desc == "" {
		desc = strings.TrimSpace(textOf(p.InvocationMessage))
	}
	if desc == "" {
		return line
	}
	desc, _, _ = strings.Cut(desc, "\n")
	return line + " " + desc
}
