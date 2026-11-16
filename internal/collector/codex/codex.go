// Package codex reads OpenAI Codex CLI sessions ("rollouts"), stored as one
// JSONL file per session under $CODEX_HOME/sessions/YYYY/MM/DD/ (CODEX_HOME
// defaults to ~/.codex) and, once archived, under $CODEX_HOME/archived_sessions/.
//
// Three generations of the format exist on disk, all handled here:
//  1. first line is flat metadata {"id","timestamp","instructions"}, then bare
//     response items {"type":"message",...} (2025-05 to about 2025-08);
//  2. every line is {"timestamp","type","payload"} with type session_meta,
//     response_item, event_msg or turn_context;
//  3. like 2, but newer "paginated" rollouts log typed messages as
//     event_msg/item_completed instead of user_message/agent_message events.
//     Codex CLI 0.169.0 writes both for every message ("history_mode":
//     "paginated", a per-line "ordinal"), which Parse deduplicates.
package codex

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/marioolf/cortex/internal/collector"
	"github.com/marioolf/cortex/internal/session"
	"github.com/marioolf/cortex/internal/wsl"
)

const ID = "codex"

const compressedSuffix = ".zst"

type Collector struct {
	Roots []string
	// extraRoots is evaluated on every call because the set of running WSL
	// distros changes while Cortex runs.
	extraRoots func() []string
	seen       collector.Seen
}

func New() *Collector {
	return &Collector{
		Roots: homeRoots(os.Getenv("CODEX_HOME"), userHome()),
		extraRoots: func() []string {
			var r []string
			for _, h := range wsl.Homes() {
				r = append(r, homeRoots("", h)...)
			}
			return r
		},
	}
}

func userHome() string {
	h, _ := os.UserHomeDir()
	return h
}

// homeRoots lists the session directories of a Codex home. codexHome wins
// over home/.codex when set, but both are read: people switch between them.
func homeRoots(codexHome, home string) []string {
	var homes []string
	if codexHome != "" {
		homes = append(homes, codexHome)
	}
	if home != "" {
		homes = append(homes, filepath.Join(home, ".codex"))
	}
	var r []string
	for _, h := range homes {
		r = append(r, filepath.Join(h, "sessions"), filepath.Join(h, "archived_sessions"))
	}
	return r
}

func (c *Collector) ID() string { return ID }

func (c *Collector) Detect() []string {
	roots := c.Roots
	if c.extraRoots != nil {
		roots = append(roots[:len(roots):len(roots)], c.extraRoots()...)
	}
	var found []string
	dup := map[string]bool{} // CODEX_HOME often is ~/.codex
	for _, r := range roots {
		r = filepath.Clean(r)
		if dup[r] {
			continue
		}
		dup[r] = true
		if st, err := os.Stat(r); err == nil && st.IsDir() {
			found = append(found, r)
		}
	}
	return found
}

// Reset makes the next Collect parse every rollout again.
func (c *Collector) Reset() { c.seen.Reset() }

// Collect skips rollouts whose modification time and size are the same as
// when they were last parsed.
func (c *Collector) Collect(fn func(*session.Session), warn func(error)) {
	for _, root := range c.Detect() {
		// Rollouts are nested by date (sessions/YYYY/MM/DD), archived ones are flat.
		filepath.WalkDir(root, func(f string, d fs.DirEntry, err error) error {
			if err != nil {
				warn(fmt.Errorf("%s: %w", f, err))
				return nil
			}
			if d.IsDir() || !isRollout(d.Name()) || hiddenByPlain(f) {
				return nil
			}
			c.collectFile(f, fn, warn)
			return nil
		})
	}
}

// isRollout matches plain and compressed rollout names. Codex compresses cold
// rollouts to rollout-*.jsonl.zst; temp files of that job have other names.
func isRollout(name string) bool {
	return strings.HasPrefix(name, "rollout-") &&
		(strings.HasSuffix(name, ".jsonl") || strings.HasSuffix(name, compressedSuffix))
}

// hiddenByPlain tells whether f is a .jsonl.zst whose plain .jsonl sibling
// exists. Codex is compressing it (it removes the plain file only after
// publishing the .zst) or is re-expanding it to append, and its own readers
// prefer the plain file, so the session is emitted once, from that one.
func hiddenByPlain(f string) bool {
	if !strings.HasSuffix(f, compressedSuffix) {
		return false
	}
	_, err := os.Stat(strings.TrimSuffix(f, compressedSuffix))
	return err == nil
}

func (c *Collector) collectFile(f string, fn func(*session.Session), warn func(error)) {
	changed, err := c.seen.Changed(f)
	if err != nil {
		warn(fmt.Errorf("%s: %w", f, err))
		return
	}
	if !changed {
		return
	}
	s, err := ParseFile(f)
	if err != nil {
		warn(fmt.Errorf("%s: %w", f, err))
		return
	}
	c.seen.Mark(f)
	if len(s.Messages) > 0 {
		fn(s)
	}
}

// rec is both a whole line and the payload inside it; which fields are filled
// depends on the generation. Fields whose type varies between item kinds are
// RawMessage so that one odd item never fails the whole line.
type rec struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
	ID        json.RawMessage `json:"id"`
	Cwd       string          `json:"cwd"`
	Role      string          `json:"role"`
	Content   json.RawMessage `json:"content"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
	Input     json.RawMessage `json:"input"`
	Message   json.RawMessage `json:"message"`
	Action    struct {
		Command json.RawMessage `json:"command"`
	} `json:"action"`
	Item *rec `json:"item"`
}

func ParseFile(path string) (*session.Session, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var r io.Reader = f
	if strings.HasSuffix(path, compressedSuffix) {
		dec, err := newDecoder(f)
		if err != nil {
			return nil, err
		}
		defer dec.Close()
		r = &cappedReader{r: dec, left: maxDecompressed}
	}
	s, err := Parse(r)
	if err != nil {
		return nil, err
	}
	if s.ID == "" {
		s.ID = idFromName(filepath.Base(path))
	}
	return s, nil
}

// idFromName extracts the uuid from rollout-YYYY-MM-DDThh-mm-ss-<uuid>[_<rollout id>].jsonl[.zst].
func idFromName(name string) string {
	name = strings.TrimSuffix(strings.TrimSuffix(name, compressedSuffix), ".jsonl")
	if len(name) >= 28+36 && strings.HasPrefix(name, "rollout-") {
		return name[28 : 28+36]
	}
	return strings.TrimPrefix(name, "rollout-")
}

// Parse reads a rollout. Lines it does not understand are skipped: the format
// is internal to Codex and changes between releases.
func Parse(r io.Reader) (*session.Session, error) {
	s := &session.Session{Source: ID}
	// Typed messages are logged twice in newer rollouts (raw response item and
	// UI event), so each source is collected apart and only one is used.
	var raw, events session.Merger
	var metaTime time.Time
	br := bufio.NewReaderSize(r, 1<<20)
	for {
		line, err := br.ReadBytes('\n') // lines can be many MB; a Scanner would choke
		if len(line) > 0 {
			var l rec
			if json.Unmarshal(line, &l) == nil {
				ts, _ := time.Parse(time.RFC3339, l.Timestamp)
				switch l.Type {
				case "":
					// Generation 1 metadata line has no type; "record_type" state lines are ignored.
					if s.ID == "" && len(l.ID) > 0 {
						s.ID = str(l.ID)
						metaTime = ts
					}
				case "session_meta":
					var m rec
					if json.Unmarshal(l.Payload, &m) == nil && s.ID == "" { // the first one is the thread's own
						s.ID = str(m.ID)
						s.Cwd = m.Cwd
						metaTime = ts
					}
				case "turn_context":
					var m rec
					if s.Cwd == "" && json.Unmarshal(l.Payload, &m) == nil {
						s.Cwd = m.Cwd
					}
				case "response_item":
					var it rec
					if json.Unmarshal(l.Payload, &it) == nil {
						addItem(s, &raw, &it, ts)
					}
				case "event_msg":
					var ev rec
					if json.Unmarshal(l.Payload, &ev) == nil {
						addEvent(&events, &ev, ts)
					}
				default:
					// Generation 1: the line is the response item itself.
					addItem(s, &raw, &l, time.Time{})
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
	s.Messages = raw.Messages()
	if !hasUser(s.Messages) && hasUser(events.Messages()) {
		s.Messages = events.Messages()
	}
	if len(s.Messages) > 0 && s.Messages[0].Time.IsZero() {
		s.Messages[0].Time = metaTime
	}
	for _, m := range s.Messages {
		if m.Role == "user" {
			s.Title, _, _ = strings.Cut(m.Text, "\n")
			break
		}
	}
	return s, nil
}

func hasUser(ms []session.Message) bool {
	for _, m := range ms {
		if m.Role == "user" {
			return true
		}
	}
	return false
}

// str decodes a JSON string, "" for anything else.
func str(raw json.RawMessage) string {
	var s string
	json.Unmarshal(raw, &s)
	return s
}

func addItem(s *session.Session, dst *session.Merger, it *rec, ts time.Time) {
	switch it.Type {
	case "message":
		role := it.Role
		if role != "user" && role != "assistant" {
			return // developer/system prompts are not conversation
		}
		text := contentText(it.Content)
		if role == "user" {
			text = cleanUser(s, text)
		}
		add(dst, role, text, ts)
	case "function_call":
		add(dst, "assistant", toolLine(it.Name, it.Arguments), ts)
	case "local_shell_call":
		add(dst, "assistant", "> `shell` "+oneLine(shellCmd(it.Action.Command)), ts)
	case "custom_tool_call":
		add(dst, "assistant", toolLine(it.Name, it.Input), ts)
	}
	// reasoning, *_output, web search and the rest are volume, not conversation.
}

// addEvent handles the UI-facing copies of messages, used only when a rollout
// has no raw user messages.
func addEvent(dst *session.Merger, ev *rec, ts time.Time) {
	switch ev.Type {
	case "user_message":
		add(dst, "user", cleanUser(nil, str(ev.Message)), ts)
	case "agent_message":
		add(dst, "assistant", str(ev.Message), ts)
	case "item_completed":
		if ev.Item == nil {
			return
		}
		switch ev.Item.Type {
		case "UserMessage":
			add(dst, "user", cleanUser(nil, contentText(ev.Item.Content)), ts)
		case "AgentMessage":
			add(dst, "assistant", contentText(ev.Item.Content), ts)
		}
	}
}

func add(dst *session.Merger, role, text string, ts time.Time) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	// A turn is spread over several items (text, tool calls, more text).
	dst.Add(role, text, ts)
}

// contentText joins the text parts: input_text/output_text in response items,
// text in typed items.
func contentText(raw json.RawMessage) string {
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return str(raw)
	}
	var out []string
	for _, p := range parts {
		if strings.EqualFold(strings.TrimSuffix(p.Type, "_text"), "input") ||
			strings.EqualFold(strings.TrimSuffix(p.Type, "_text"), "output") ||
			strings.EqualFold(p.Type, "text") {
			out = append(out, p.Text)
		}
	}
	return strings.Join(out, "\n\n")
}

// shellCmd turns an argv such as ["bash","-lc","ls -la"] into what was typed.
func shellCmd(raw json.RawMessage) string {
	var argv []string
	if json.Unmarshal(raw, &argv) != nil {
		return str(raw)
	}
	if len(argv) >= 3 && (argv[1] == "-c" || argv[1] == "-lc") {
		return argv[2]
	}
	return strings.Join(argv, " ")
}

// oneLine keeps only the first line. It does not shorten it: that is done
// after redaction, so a secret cut in half is not left half-visible.
func oneLine(v string) string {
	v, _, _ = strings.Cut(strings.TrimSpace(v), "\n")
	return v
}

// toolLine summarizes a function_call (JSON-string arguments) or a
// custom_tool_call (free-form input, e.g. an apply_patch body).
func toolLine(name string, args json.RawMessage) string {
	line := "> `" + name + "`"
	if name == "shell" || name == "container.exec" || name == "local_shell" {
		line = "> `shell`"
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal([]byte(str(args)), &obj) == nil {
		for _, k := range []string{"command", "cmd", "path", "file_path", "pattern", "query", "url", "prompt", "description"} {
			if v := strings.TrimSpace(shellCmd(obj[k])); v != "" {
				return line + " " + oneLine(v)
			}
		}
		return line
	}
	// Not JSON: patch text names the file on its first "*** Update File:"-style line.
	for _, l := range strings.Split(str(args), "\n") {
		if l = strings.TrimSpace(l); strings.HasPrefix(l, "*** ") && !strings.HasPrefix(l, "*** Begin") {
			return line + " " + oneLine(l)
		}
	}
	return line
}

var (
	// Context Codex injects as user-role messages: the first thing in every
	// session, and again after compaction or a directory change.
	injected = regexp.MustCompile(`(?s)^\s*(<(environment_context|user_instructions|environments_instructions|apps_instructions|skills_instructions|plugins_instructions|collaboration_mode|turn_aborted|user_shell_command|subagent_notification|permissions instructions)[^>]*>.*?</(environment_context|user_instructions|environments_instructions|apps_instructions|skills_instructions|plugins_instructions|collaboration_mode|turn_aborted|user_shell_command|subagent_notification|permissions instructions)>|# AGENTS\.md instructions.*?</INSTRUCTIONS>)\s*`)
	cwdTag   = regexp.MustCompile(`<cwd>(.*?)</cwd>`)
)

const requestMarker = "## My request for Codex:"

// cleanUser removes the injected blocks. Generation 1 rollouts carry no cwd in
// their metadata, so it is taken from the environment block when s is given.
func cleanUser(s *session.Session, t string) string {
	if s != nil && s.Cwd == "" {
		if m := cwdTag.FindStringSubmatch(t); m != nil && strings.Contains(t, "<environment_context>") {
			s.Cwd = strings.TrimSpace(m[1])
		}
	}
	for {
		u := injected.ReplaceAllString(t, "")
		if u == t {
			break
		}
		t = u
	}
	// Old versions prefixed the typed text with context and this marker.
	if _, after, ok := strings.Cut(t, requestMarker); ok {
		t = after
	}
	return t
}
