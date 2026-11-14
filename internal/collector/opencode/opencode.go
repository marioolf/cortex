// Package opencode reads OpenCode (sst/opencode) sessions. OpenCode keeps its
// data in <data dir>/opencode*.db (SQLite, since v1.1/1.2) and, before that,
// as JSON files under <data dir>/storage/. The data dir is
// $XDG_DATA_HOME/opencode, or ~/.local/share/opencode on every OS (including
// Windows and macOS: OpenCode uses the xdg-basedir convention everywhere).
package opencode

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/marioolf/cortex/internal/collector"
	"github.com/marioolf/cortex/internal/session"
	"github.com/marioolf/cortex/internal/sqliteutil"
	"github.com/marioolf/cortex/internal/wsl"
)

const ID = "opencode"

type Collector struct {
	// Roots are OpenCode data directories.
	Roots []string
	// extraRoots is evaluated on every call because the set of running WSL
	// distros changes while Cortex runs.
	extraRoots func() []string
	seen       collector.Seen    // database files (+ -wal)
	fp         map[string]string // per-session fingerprint: JSON layout and database
}

func New() *Collector {
	var roots []string
	home, _ := os.UserHomeDir()
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		roots = append(roots, filepath.Join(x, "opencode"))
	}
	if home != "" {
		roots = append(roots, filepath.Join(home, ".local", "share", "opencode"))
	}
	return &Collector{Roots: roots, extraRoots: func() []string {
		var r []string
		for _, h := range wsl.Homes() {
			r = append(r, filepath.Join(h, ".local", "share", "opencode"))
		}
		return r
	}}
}

func (c *Collector) ID() string { return ID }

func (c *Collector) allRoots() []string {
	roots := c.Roots
	if c.extraRoots != nil {
		roots = append(roots[:len(roots):len(roots)], c.extraRoots()...)
	}
	return roots
}

func (c *Collector) Detect() []string {
	var found []string
	seen := map[string]bool{}
	for _, r := range c.allRoots() {
		if st, err := os.Stat(r); err == nil && st.IsDir() && !seen[r] {
			seen[r] = true
			found = append(found, r)
		}
	}
	return found
}

// Reset makes the next Collect report every session again.
func (c *Collector) Reset() { c.seen.Reset(); c.fp = nil }

func (c *Collector) Collect(fn func(*session.Session), warn func(error)) {
	if c.fp == nil {
		c.fp = map[string]string{}
	}
	for _, root := range c.Detect() {
		// Channel builds use opencode-<channel>.db. Once a database exists, the
		// storage/ JSON files are what OpenCode migrated from and are no longer
		// written, so reading both would report every old session twice.
		dbs, _ := filepath.Glob(filepath.Join(root, "opencode*.db"))
		if len(dbs) == 0 {
			c.collectJSON(filepath.Join(root, "storage"), fn, warn)
			continue
		}
		for _, db := range dbs {
			c.collectDB(db, fn, warn)
		}
	}
}

// ---- message and part shapes (shared by the JSON files and the database) ----

type sessionInfo struct {
	ID        string `json:"id"`
	ParentID  string `json:"parentID"`
	Directory string `json:"directory"`
	Title     string `json:"title"`
}

type msgInfo struct {
	Role string `json:"role"`
	Time struct {
		Created int64 `json:"created"`
	} `json:"time"`
}

// partData covers the v1 part (tool name in "tool") and the newer session_part
// (tool name in "name").
type partData struct {
	Type      string `json:"type"`
	Text      string `json:"text"`
	Synthetic bool   `json:"synthetic"`
	Ignored   bool   `json:"ignored"`
	Tool      string `json:"tool"`
	Name      string `json:"name"`
	State     struct {
		Input map[string]any `json:"input"`
	} `json:"state"`
}

// message is a message with its parts still raw, so both layouts feed one builder.
type message struct {
	id      string
	role    string
	created int64
	parts   []json.RawMessage
}

var autoTitle = regexp.MustCompile(`^(New session|Child session) - \d{4}-\d\d-\d\d`)

// build turns messages into a Session. Reasoning, tool output and synthetic or
// ignored text (injected by OpenCode, not typed) are dropped.
func build(info sessionInfo, msgs []message) *session.Session {
	s := &session.Session{Source: ID, ID: info.ID, Cwd: info.Directory}
	if !autoTitle.MatchString(info.Title) {
		s.Title = info.Title
	}
	sort.SliceStable(msgs, func(i, j int) bool {
		if msgs[i].created != msgs[j].created {
			return msgs[i].created < msgs[j].created
		}
		return msgs[i].id < msgs[j].id
	})
	for _, m := range msgs {
		if m.role != "user" && m.role != "assistant" {
			continue
		}
		var parts []string
		for _, raw := range m.parts {
			var p partData
			if json.Unmarshal(raw, &p) != nil {
				continue
			}
			switch p.Type {
			case "text":
				if t := strings.TrimSpace(p.Text); t != "" && !p.Synthetic && !p.Ignored {
					parts = append(parts, t)
				}
			case "tool":
				if m.role == "assistant" {
					parts = append(parts, toolLine(p))
				}
			}
		}
		text := strings.Join(parts, "\n\n")
		if text == "" {
			continue
		}
		var ts time.Time
		if m.created > 0 {
			ts = time.UnixMilli(m.created)
		}
		// An assistant turn is several messages (one per model step).
		if n := len(s.Messages); n > 0 && s.Messages[n-1].Role == m.role {
			s.Messages[n-1].Text += "\n\n" + text
			continue
		}
		s.Messages = append(s.Messages, session.Message{Role: m.role, Text: text, Time: ts})
	}
	if s.Title == "" {
		for _, m := range s.Messages {
			if m.Role == "user" {
				s.Title, _, _ = strings.Cut(m.Text, "\n")
				break
			}
		}
	}
	return s
}

func toolLine(p partData) string {
	name := p.Tool
	if name == "" {
		name = p.Name
	}
	line := "> `" + name + "`"
	for _, k := range []string{"description", "command", "filePath", "file_path", "path", "pattern", "query", "url", "prompt"} {
		if v, ok := p.State.Input[k].(string); ok && strings.TrimSpace(v) != "" {
			v, _, _ = strings.Cut(strings.TrimSpace(v), "\n")
			return line + " " + v
		}
	}
	return line
}

// ---- JSON files: storage/{session/<project>,message/<session>,part/<message>} ----

// collectJSON handles the layout used before the move to SQLite.
//
// "Unchanged" cannot be read from the session file alone: OpenCode rewrites it
// (time.updated) only when a user prompt is sent or the title/summary changes,
// not for assistant messages or parts. So the fingerprint combines the session
// file, the name/mtime/size of every file in message/<session>/ (a new message
// adds a file, and OpenCode rewrites an assistant's message file when it
// completes, after its parts are written) and the mtime of the newest
// message's part directory (parts of a message that is still streaming).
func (c *Collector) collectJSON(storage string, fn func(*session.Session), warn func(error)) {
	files, _ := filepath.Glob(filepath.Join(storage, "session", "*", "*.json"))
	for _, f := range files {
		id := strings.TrimSuffix(filepath.Base(f), ".json")
		msgDir := filepath.Join(storage, "message", id)
		fp, err := jsonFingerprint(f, msgDir, filepath.Join(storage, "part"))
		if err != nil {
			warn(fmt.Errorf("%s: %w", f, err))
			continue
		}
		if c.fp[f] == fp {
			continue
		}
		var info sessionInfo
		if err := readJSON(f, &info); err != nil {
			warn(fmt.Errorf("%s: %w", f, err))
			continue
		}
		c.fp[f] = fp // even for child sessions: no need to look at them again
		if info.ID == "" {
			info.ID = id
		}
		if info.ParentID != "" {
			continue
		}
		s := build(info, readJSONMessages(storage, msgDir))
		if len(s.Messages) > 0 {
			fn(s)
		}
	}
}

func jsonFingerprint(sessionFile, msgDir, partRoot string) (string, error) {
	st, err := os.Stat(sessionFile)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d:%d", st.ModTime().UnixNano(), st.Size())
	entries, _ := os.ReadDir(msgDir) // no messages yet is fine
	var last string
	for _, e := range entries {
		if info, err := e.Info(); err == nil && !e.IsDir() {
			fmt.Fprintf(&b, "|%s:%d:%d", e.Name(), info.ModTime().UnixNano(), info.Size())
			if e.Name() > last {
				last = e.Name()
			}
		}
	}
	if last != "" {
		if st, err := os.Stat(filepath.Join(partRoot, strings.TrimSuffix(last, ".json"))); err == nil {
			fmt.Fprintf(&b, "|p%d", st.ModTime().UnixNano())
		}
	}
	return b.String(), nil
}

func readJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

// readJSONMessages skips files it cannot read or parse: a half-written file
// shows up complete on the next round, when the fingerprint changes.
func readJSONMessages(storage, msgDir string) []message {
	var msgs []message
	files, _ := filepath.Glob(filepath.Join(msgDir, "*.json"))
	for _, f := range files {
		var mi msgInfo
		if readJSON(f, &mi) != nil {
			continue
		}
		m := message{id: strings.TrimSuffix(filepath.Base(f), ".json"), role: mi.Role, created: mi.Time.Created}
		pfiles, _ := filepath.Glob(filepath.Join(storage, "part", m.id, "*.json"))
		sort.Strings(pfiles) // part ids ascend in creation order
		for _, pf := range pfiles {
			if data, err := os.ReadFile(pf); err == nil {
				m.parts = append(m.parts, data)
			}
		}
		msgs = append(msgs, m)
	}
	return msgs
}

// ---- SQLite ----

// layout names the tables of one schema generation.
type layout struct {
	session, message, part string
	order                  string // part order within a message
	// legacy tables to try when a session has no rows in message: databases
	// migrated from v1 keep old sessions' history only there.
	legacyMessage, legacyPart string
}

var (
	// v1 (OpenCode 1.2+): session / message / part, each row's JSON in "data".
	layoutV1 = layout{session: "session", message: "message", part: "part", order: "id"}
	// Newer builds renamed session to session_v2 and moved messages to
	// session_message / session_part.
	layoutV2 = layout{session: "session_v2", message: "session_message", part: "session_part", order: "ordinal"}
)

func (c *Collector) collectDB(path string, fn func(*session.Session), warn func(error)) {
	changed, err := sqliteutil.Changed(&c.seen, path)
	if err != nil {
		warn(fmt.Errorf("%s: %w", path, err))
		return
	}
	if !changed {
		return
	}
	db, closeDB, err := sqliteutil.Open(path)
	if err != nil {
		warn(fmt.Errorf("%s: %w", path, err))
		return
	}
	defer closeDB()
	tables, err := sqliteutil.Tables(db)
	if err != nil {
		warn(fmt.Errorf("%s: %w", path, err))
		return
	}
	var ls []layout
	if tables["session_v2"] && tables["session_message"] {
		l := layoutV2
		if tables["message"] && tables["part"] {
			l.legacyMessage, l.legacyPart = "message", "part"
		}
		ls = append(ls, l)
	} else if tables["session"] && tables["message"] && tables["part"] {
		ls = append(ls, layoutV1)
	}
	ok := true
	for _, l := range ls {
		if err := c.readLayout(db, path, l, fn); err != nil {
			warn(fmt.Errorf("%s: %w", path, err))
			ok = false
		}
	}
	if ok {
		sqliteutil.MarkSeen(&c.seen, path)
	}
}

func (c *Collector) readLayout(db *sql.DB, path string, l layout, fn func(*session.Session)) error {
	type item struct{ id, fp string }
	// Top-level sessions only; parent_id marks subagent sessions.
	q := fmt.Sprintf(`SELECT id, time_updated,
		(SELECT count(*) || ':' || coalesce(max(time_updated), 0) FROM %[2]s WHERE session_id = s.id),
		(SELECT count(*) || ':' || coalesce(max(time_updated), 0) FROM %[3]s WHERE session_id = s.id)
		FROM %[1]s s WHERE parent_id IS NULL`, l.session, l.message, l.part)
	rows, err := db.Query(q)
	if err != nil {
		return err
	}
	var todo []item
	for rows.Next() {
		var id string
		var upd, m, p sql.NullString
		if rows.Scan(&id, &upd, &m, &p) != nil {
			continue
		}
		key := path + "\x00" + l.session + "\x00" + id
		fp := upd.String + "|" + m.String + "|" + p.String
		if c.fp[key] != fp {
			todo = append(todo, item{id, fp})
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	var firstErr error
	for _, it := range todo {
		s, err := readDBSession(db, l, it.id)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("session %s: %w", it.id, err)
			}
			continue
		}
		c.fp[path+"\x00"+l.session+"\x00"+it.id] = it.fp
		if len(s.Messages) > 0 {
			fn(s)
		}
	}
	return firstErr
}

func readDBSession(db *sql.DB, l layout, id string) (*session.Session, error) {
	var info sessionInfo
	var dir, title sql.NullString
	if err := db.QueryRow(fmt.Sprintf("SELECT directory, title FROM %s WHERE id = ?", l.session), id).Scan(&dir, &title); err != nil {
		return nil, err
	}
	info = sessionInfo{ID: id, Directory: dir.String, Title: title.String}

	msgs, err := readMessages(db, l, id)
	if err != nil {
		return nil, err
	}
	if len(msgs) == 0 && l.legacyMessage != "" {
		legacy := layout{session: l.session, message: l.legacyMessage, part: l.legacyPart, order: "id"}
		if msgs, err = readMessages(db, legacy, id); err != nil {
			return nil, err
		}
	}
	return build(info, msgs), nil
}

func readMessages(db *sql.DB, l layout, id string) ([]message, error) {
	// session_message keeps the kind ("user", "assistant", ...) in a column, not in data.
	typeCol := "''"
	if l.order == "ordinal" {
		typeCol = "type"
	}
	rows, err := db.Query(fmt.Sprintf("SELECT id, time_created, data, %s FROM %s WHERE session_id = ? ORDER BY time_created, id", typeCol, l.message), id)
	if err != nil {
		return nil, err
	}
	var msgs []message
	index := map[string]int{}
	for rows.Next() {
		var mid string
		var created int64
		var data []byte
		var kind string
		if rows.Scan(&mid, &created, &data, &kind) != nil {
			continue
		}
		var mi struct {
			msgInfo
			Text    string            `json:"text"` // session_message user
			Content []json.RawMessage `json:"content"`
		}
		if json.Unmarshal(data, &mi) != nil {
			continue
		}
		role := mi.Role
		if role == "" && (kind == "user" || kind == "assistant") {
			role = kind
		}
		m := message{id: mid, role: role, created: created, parts: mi.Content}
		if l.order == "ordinal" && role == "user" && mi.Text != "" {
			t, _ := json.Marshal(map[string]any{"type": "text", "text": mi.Text})
			m.parts = append(m.parts, t)
		}
		index[mid] = len(msgs)
		msgs = append(msgs, m)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}

	// Stored parts replace inline content (assistant content moves to parts).
	prows, err := db.Query(fmt.Sprintf("SELECT message_id, data FROM %s WHERE session_id = ? ORDER BY message_id, %s", l.part, l.order), id)
	if err != nil {
		return nil, err
	}
	defer prows.Close()
	cleared := map[string]bool{}
	for prows.Next() {
		var mid string
		var data []byte
		if prows.Scan(&mid, &data) != nil {
			continue
		}
		i, ok := index[mid]
		if !ok {
			continue
		}
		if !cleared[mid] {
			cleared[mid] = true
			if l.order == "ordinal" {
				msgs[i].parts = nil
			}
		}
		msgs[i].parts = append(msgs[i].parts, data)
	}
	if err := prows.Err(); err != nil {
		return nil, err
	}
	return msgs, nil
}
