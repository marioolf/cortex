// Package claudecode reads Claude Code transcripts (CLI and desktop app),
// stored as one JSONL file per session under ~/.claude/projects/<project>/.
package claudecode

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/marioolf/cortex/internal/session"
	"github.com/marioolf/cortex/internal/wsl"
)

const ID = "claude-code"

type stamp struct {
	mod  time.Time
	size int64
}

type Collector struct {
	Roots []string
	// extraRoots is evaluated on every call because the set of running WSL
	// distros changes while Cortex runs.
	extraRoots func() []string
	seen       map[string]stamp // transcript path -> what we last parsed
}

func New() *Collector {
	var roots []string
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		roots = append(roots, filepath.Join(dir, "projects"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		roots = append(roots, filepath.Join(home, ".claude", "projects"))
	}
	return &Collector{Roots: roots, extraRoots: func() []string {
		var r []string
		for _, h := range wsl.Homes() {
			r = append(r, filepath.Join(h, ".claude", "projects"))
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

// Reset makes the next Collect parse every transcript again.
func (c *Collector) Reset() { c.seen = nil }

// Collect skips transcripts whose modification time and size are the same as
// when they were last parsed; a long-running process would otherwise re-read
// every transcript every round.
func (c *Collector) Collect(fn func(*session.Session), warn func(error)) {
	if c.seen == nil {
		c.seen = map[string]stamp{}
	}
	for _, root := range c.Detect() {
		// Subagent transcripts live one level deeper and are deliberately not matched.
		files, _ := filepath.Glob(filepath.Join(root, "*", "*.jsonl"))
		for _, f := range files {
			st, err := os.Stat(f)
			if err != nil {
				warn(fmt.Errorf("%s: %w", f, err))
				continue
			}
			cur := stamp{st.ModTime(), st.Size()}
			if prev, ok := c.seen[f]; ok && prev == cur {
				continue
			}
			s, err := ParseFile(f)
			if err != nil {
				warn(fmt.Errorf("%s: %w", f, err))
				continue
			}
			c.seen[f] = cur
			if len(s.Messages) > 0 {
				fn(s)
			}
		}
	}
}

type entry struct {
	Type              string          `json:"type"`
	Cwd               string          `json:"cwd"`
	Timestamp         string          `json:"timestamp"`
	IsSidechain       bool            `json:"isSidechain"`
	IsMeta            bool            `json:"isMeta"`
	IsCompactSummary  bool            `json:"isCompactSummary"`
	IsAPIErrorMessage bool            `json:"isApiErrorMessage"`
	AITitle           string          `json:"aiTitle"`
	CustomTitle       string          `json:"customTitle"`
	Message           json.RawMessage `json:"message"`
}

type block struct {
	Type  string         `json:"type"`
	Text  string         `json:"text"`
	Name  string         `json:"name"`
	Input map[string]any `json:"input"`
}

func ParseFile(path string) (*session.Session, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	s, err := Parse(f)
	if err != nil {
		return nil, err
	}
	s.ID = strings.TrimSuffix(filepath.Base(path), ".jsonl")
	return s, nil
}

// Parse reads a transcript. Lines it does not understand are skipped: the
// format is undocumented and gains new entry types with every release.
func Parse(r io.Reader) (*session.Session, error) {
	s := &session.Session{Source: ID}
	var aiTitle, customTitle string
	br := bufio.NewReaderSize(r, 1<<20)
	for {
		line, err := br.ReadBytes('\n') // lines can be many MB; a Scanner would choke
		if len(line) > 0 {
			var e entry
			if json.Unmarshal(line, &e) == nil {
				switch e.Type {
				case "ai-title":
					aiTitle = e.AITitle
				case "custom-title":
					customTitle = e.CustomTitle
				case "user", "assistant":
					addMessage(s, &e)
				}
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	switch {
	case customTitle != "":
		s.Title = customTitle
	case aiTitle != "":
		s.Title = aiTitle
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

func addMessage(s *session.Session, e *entry) {
	if e.IsSidechain || e.IsMeta || e.IsCompactSummary || e.IsAPIErrorMessage {
		return
	}
	if s.Cwd == "" {
		s.Cwd = e.Cwd
	}
	var msg struct {
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(e.Message, &msg) != nil {
		return
	}
	text := contentText(msg.Content)
	if e.Type == "user" {
		text = cleanUser(text)
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	ts, _ := time.Parse(time.RFC3339, e.Timestamp)
	// One assistant turn is spread over several lines (one per content block).
	if n := len(s.Messages); n > 0 && s.Messages[n-1].Role == e.Type {
		s.Messages[n-1].Text += "\n\n" + text
		return
	}
	s.Messages = append(s.Messages, session.Message{Role: e.Type, Text: text, Time: ts})
}

// contentText keeps text and a one-line summary of each tool call. Tool
// results and thinking are dropped: they are most of the volume and rarely
// what anyone looks for later.
func contentText(raw json.RawMessage) string {
	var str string
	if json.Unmarshal(raw, &str) == nil {
		return str
	}
	var blocks []block
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if t := strings.TrimSpace(b.Text); t != "" {
				parts = append(parts, t)
			}
		case "tool_use":
			parts = append(parts, toolLine(b))
		}
	}
	return strings.Join(parts, "\n\n")
}

func toolLine(b block) string {
	line := "> `" + b.Name + "`"
	for _, k := range []string{"description", "command", "file_path", "path", "pattern", "query", "url", "skill", "prompt"} {
		if v, ok := b.Input[k].(string); ok && strings.TrimSpace(v) != "" {
			v, _, _ = strings.Cut(strings.TrimSpace(v), "\n")
			if r := []rune(v); len(r) > 120 {
				v = string(r[:120]) + "…"
			}
			return line + " " + v
		}
	}
	return line
}

var (
	// Harness-injected wrappers that the user never typed.
	injected = regexp.MustCompile(`(?s)<(system-reminder|task-notification|command-message|local-command-[a-z]+)>.*?</(system-reminder|task-notification|command-message|local-command-[a-z]+)>`)
	cmdName  = regexp.MustCompile(`(?s)<command-name>(.*?)</command-name>`)
	cmdArgs  = regexp.MustCompile(`(?s)<command-args>(.*?)</command-args>`)
)

func cleanUser(t string) string {
	t = injected.ReplaceAllString(t, "")
	t = cmdName.ReplaceAllString(t, "$1")
	t = cmdArgs.ReplaceAllString(t, " $1")
	return t
}
