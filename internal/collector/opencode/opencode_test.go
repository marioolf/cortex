package opencode

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marioolf/cortex/internal/session"
	_ "modernc.org/sqlite"
)

func write(t *testing.T, path string, v any) {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

type run struct {
	got   []*session.Session
	warns []error
}

func collect(c *Collector) run {
	var r run
	c.Collect(func(s *session.Session) { r.got = append(r.got, s) }, func(e error) { r.warns = append(r.warns, e) })
	return r
}

func newC(root string) *Collector { return &Collector{Roots: []string{root}} }

type m = map[string]any

// ---- JSON layout ----

func jsonSession(t *testing.T, root, sid, parent string) {
	st := filepath.Join(root, "storage")
	info := m{"id": sid, "projectID": "p1", "directory": "/work/app", "title": "New session - 2025-08-01T10:00:00.000Z", "time": m{"created": 1, "updated": 2}}
	if parent != "" {
		info["parentID"] = parent
	}
	write(t, filepath.Join(st, "session", "p1", sid+".json"), info)
}

func jsonMsg(t *testing.T, root, sid, mid, role string, created int64, parts ...map[string]any) {
	st := filepath.Join(root, "storage")
	write(t, filepath.Join(st, "message", sid, mid+".json"), m{"id": mid, "sessionID": sid, "role": role, "time": m{"created": created}})
	for i, p := range parts {
		write(t, filepath.Join(st, "part", mid, fmt.Sprintf("prt_%02d.json", i)), p)
	}
}

func TestJSONLayout(t *testing.T) {
	root := t.TempDir()
	jsonSession(t, root, "ses_a", "")
	jsonSession(t, root, "ses_child", "ses_a")
	jsonSession(t, root, "ses_empty", "")
	// Written out of order on purpose: ordering follows time.created.
	jsonMsg(t, root, "ses_a", "msg_2", "assistant", 2000,
		m{"type": "reasoning", "text": "secret thoughts"},
		m{"type": "text", "text": "Let me look."},
		m{"type": "tool", "tool": "bash", "state": m{"status": "completed", "input": m{"command": "ls -la\nsecond line", "description": ""}, "output": "TOOL OUTPUT"}},
	)
	jsonMsg(t, root, "ses_a", "msg_1", "user", 1000,
		m{"type": "text", "text": "Fix the build\nplease"},
		m{"type": "text", "text": "<injected>", "synthetic": true},
		m{"type": "text", "text": "ignored one", "ignored": true},
	)
	jsonMsg(t, root, "ses_a", "msg_3", "assistant", 3000, m{"type": "tool", "tool": "read", "state": m{"input": m{"filePath": "/work/app/main.go"}}})
	jsonMsg(t, root, "ses_child", "msg_c", "user", 1, m{"type": "text", "text": "child"})
	jsonMsg(t, root, "ses_empty", "msg_e", "assistant", 1, m{"type": "reasoning", "text": "only thinking"})

	c := newC(root)
	r := collect(c)
	if len(r.warns) != 0 {
		t.Fatal(r.warns)
	}
	if len(r.got) != 1 {
		t.Fatalf("got %d sessions, want 1", len(r.got))
	}
	s := r.got[0]
	if s.Source != ID || s.ID != "ses_a" || s.Cwd != "/work/app" || s.Title != "Fix the build" {
		t.Errorf("session = %+v", s)
	}
	if len(s.Messages) != 2 {
		t.Fatalf("messages = %+v", s.Messages)
	}
	if s.Messages[0].Role != "user" || s.Messages[0].Text != "Fix the build\nplease" {
		t.Errorf("user = %q", s.Messages[0].Text)
	}
	want := "Let me look.\n\n> `bash` ls -la\n\n> `read` /work/app/main.go"
	if s.Messages[1].Text != want {
		t.Errorf("assistant = %q, want %q", s.Messages[1].Text, want)
	}
	if !s.Messages[0].Time.Equal(time.UnixMilli(1000)) {
		t.Errorf("time = %v", s.Messages[0].Time)
	}
	for _, bad := range []string{"secret", "TOOL OUTPUT", "injected", "ignored one"} {
		if strings.Contains(s.Messages[0].Text+s.Messages[1].Text, bad) {
			t.Errorf("%q leaked", bad)
		}
	}

	// Unchanged: nothing reported.
	if r := collect(c); len(r.got) != 0 {
		t.Errorf("unchanged round reported %d sessions", len(r.got))
	}
	// A new message file (session file untouched) is noticed.
	jsonMsg(t, root, "ses_a", "msg_4", "user", 4000, m{"type": "text", "text": "thanks"})
	if r := collect(c); len(r.got) != 1 || len(r.got[0].Messages) != 3 {
		t.Errorf("after new message: %+v", r.got)
	}
	// A late part for the newest message (message file untouched) is noticed.
	time.Sleep(20 * time.Millisecond) // directory mtime granularity
	write(t, filepath.Join(root, "storage", "part", "msg_4", "prt_09.json"), m{"type": "text", "text": "and more"})
	if r := collect(c); len(r.got) != 1 || !strings.Contains(r.got[0].Messages[2].Text, "and more") {
		t.Errorf("after new part: %+v", r.got)
	}
	// Reset reports everything again.
	c.Reset()
	if r := collect(c); len(r.got) != 1 {
		t.Errorf("after Reset: %d sessions", len(r.got))
	}
}

func TestJSONCorrupt(t *testing.T) {
	root := t.TempDir()
	jsonSession(t, root, "ses_ok", "")
	jsonMsg(t, root, "ses_ok", "msg_1", "user", 1, m{"type": "text", "text": "hello"})
	bad := filepath.Join(root, "storage", "session", "p1", "ses_bad.json")
	os.WriteFile(bad, []byte("{not json"), 0o644)
	// A broken message file is skipped silently; the rest of the session survives.
	os.WriteFile(filepath.Join(root, "storage", "message", "ses_ok", "msg_x.json"), []byte("\x00\x01"), 0o644)

	r := collect(newC(root))
	if len(r.got) != 1 || r.got[0].ID != "ses_ok" || len(r.got[0].Messages) != 1 {
		t.Errorf("got %+v", r.got)
	}
	if len(r.warns) != 1 {
		t.Errorf("warns = %v", r.warns)
	}
}

// ---- SQLite ----

type dbw struct{ db *sql.DB }

func openW(t *testing.T, path string) dbw {
	t.Helper()
	// wal_autocheckpoint(0): keep new rows in the -wal, as a busy app does.
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(wal)&_pragma=wal_autocheckpoint(0)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	return dbw{db}
}

func (w dbw) exec(t *testing.T, q string, args ...any) {
	t.Helper()
	if _, err := w.db.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func (w dbw) v1(t *testing.T) {
	w.exec(t, `CREATE TABLE session (id text PRIMARY KEY, project_id text, parent_id text, directory text NOT NULL, title text NOT NULL, time_created integer, time_updated integer)`)
	w.exec(t, `CREATE TABLE message (id text PRIMARY KEY, session_id text NOT NULL, time_created integer, time_updated integer, data text NOT NULL)`)
	w.exec(t, `CREATE TABLE part (id text PRIMARY KEY, message_id text NOT NULL, session_id text NOT NULL, time_created integer, time_updated integer, data text NOT NULL)`)
}

func (w dbw) v1Msg(t *testing.T, sid, mid, role string, created int64, parts ...map[string]any) {
	b, _ := json.Marshal(m{"role": role, "time": m{"created": created}})
	w.exec(t, `INSERT INTO message VALUES (?,?,?,?,?)`, mid, sid, created, created, string(b))
	for i, p := range parts {
		pb, _ := json.Marshal(p)
		w.exec(t, `INSERT INTO part VALUES (?,?,?,?,?,?)`, fmt.Sprintf("%s_prt_%02d", mid, i), mid, sid, created, created, string(pb))
	}
}

func TestSQLiteV1AndWAL(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "opencode.db")
	w := openW(t, path)
	w.v1(t)
	w.exec(t, `INSERT INTO session VALUES ('ses_1','p',NULL,'/home/u/proj','Real title',1,1)`)
	w.exec(t, `INSERT INTO session VALUES ('ses_sub','p','ses_1','/home/u/proj','sub',1,1)`)
	w.exec(t, `INSERT INTO session VALUES ('ses_empty','p',NULL,'/x','New session - 2025-01-01',1,1)`)
	w.v1Msg(t, "ses_1", "msg_a", "user", 1000, m{"type": "text", "text": "hi"})
	w.v1Msg(t, "ses_1", "msg_b", "assistant", 2000,
		m{"type": "reasoning", "text": "hmm"},
		m{"type": "tool", "tool": "grep", "state": m{"input": m{"pattern": "TODO"}}},
		m{"type": "text", "text": "found"})
	w.v1Msg(t, "ses_sub", "msg_s", "user", 1, m{"type": "text", "text": "subagent"})

	// The fixture really lives in the -wal, not the main file.
	if st, err := os.Stat(path + "-wal"); err != nil || st.Size() == 0 {
		t.Fatalf("expected a populated -wal: %v", err)
	}
	before := snapshot(t, path)

	c := newC(root)
	r := collect(c)
	if len(r.warns) != 0 {
		t.Fatal(r.warns)
	}
	if len(r.got) != 1 {
		t.Fatalf("got %d sessions", len(r.got))
	}
	s := r.got[0]
	if s.ID != "ses_1" || s.Title != "Real title" || s.Cwd != "/home/u/proj" || len(s.Messages) != 2 {
		t.Fatalf("session = %+v", s)
	}
	if s.Messages[1].Text != "> `grep` TODO\n\nfound" {
		t.Errorf("assistant = %q", s.Messages[1].Text)
	}

	if got := snapshot(t, path); got != before {
		t.Error("collecting changed the database files")
	}
	if r := collect(c); len(r.got) != 0 {
		t.Errorf("unchanged round reported %d sessions", len(r.got))
	}
	w.v1Msg(t, "ses_1", "msg_c", "user", 3000, m{"type": "text", "text": "more"})
	if r := collect(c); len(r.got) != 1 || len(r.got[0].Messages) != 3 {
		t.Errorf("after write: %+v", r.got)
	}
	if got := snapshot(t, path); got == before {
		t.Log("(writer's own changes expected to differ)")
	}
}

// snapshot is the content of the database and its -wal, to prove Collect
// never writes to them.
func snapshot(t *testing.T, path string) string {
	t.Helper()
	var b bytes.Buffer
	for _, p := range []string{path, path + "-wal"} {
		data, err := os.ReadFile(p)
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		fmt.Fprintf(&b, "%d:", len(data))
		b.Write(data)
	}
	return b.String()
}

func TestSQLiteV2(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "opencode.db")
	w := openW(t, path)
	w.exec(t, `CREATE TABLE session_v2 (id text PRIMARY KEY, parent_id text, directory text NOT NULL, title text, time_created integer, time_updated integer)`)
	w.exec(t, `CREATE TABLE session_message (id text PRIMARY KEY, session_id text NOT NULL, type text NOT NULL, seq integer, time_created integer, time_updated integer, data text NOT NULL)`)
	w.exec(t, `CREATE TABLE session_part (message_id text NOT NULL, session_id text NOT NULL, ordinal integer NOT NULL, type text, time_created integer, time_updated integer, data text NOT NULL)`)
	// Legacy tables, as left by a migration from v1.
	w.exec(t, `CREATE TABLE message (id text PRIMARY KEY, session_id text NOT NULL, time_created integer, time_updated integer, data text NOT NULL)`)
	w.exec(t, `CREATE TABLE part (id text PRIMARY KEY, message_id text NOT NULL, session_id text NOT NULL, time_created integer, time_updated integer, data text NOT NULL)`)

	w.exec(t, `INSERT INTO session_v2 VALUES ('ses_n','','/w/new',NULL,1,1)`)
	w.exec(t, `UPDATE session_v2 SET parent_id = NULL`)
	w.exec(t, `INSERT INTO session_v2 VALUES ('ses_old',NULL,'/w/old','Old one',1,1)`)
	ins := func(id, sid, typ string, seq, created int, data any) {
		b, _ := json.Marshal(data)
		w.exec(t, `INSERT INTO session_message VALUES (?,?,?,?,?,?,?)`, id, sid, typ, seq, created, created, string(b))
	}
	ins("msg_1", "ses_n", "user", 1, 1000, m{"text": "build a thing"})
	ins("msg_0", "ses_n", "agent-switched", 0, 900, m{"agent": "build"})
	ins("msg_2", "ses_n", "assistant", 2, 2000, m{"content": []any{
		m{"type": "reasoning", "text": "think"},
		m{"type": "text", "text": "inline answer"},
		m{"type": "tool", "name": "bash", "state": m{"status": "completed", "input": m{"command": "make"}}},
	}})
	ins("msg_3", "ses_n", "assistant", 3, 3000, m{"content": []any{m{"type": "text", "text": "replaced by parts"}}})
	pb, _ := json.Marshal(m{"type": "text", "text": "from parts"})
	w.exec(t, `INSERT INTO session_part VALUES ('msg_3','ses_n',0,'text',3000,3000,?)`, string(pb))

	// ses_old exists only in the legacy tables.
	w.v1Msg(t, "ses_old", "m_old", "user", 500, m{"type": "text", "text": "legacy question"})

	r := collect(newC(root))
	if len(r.warns) != 0 {
		t.Fatal(r.warns)
	}
	by := map[string]*session.Session{}
	for _, s := range r.got {
		by[s.ID] = s
	}
	n := by["ses_n"]
	if n == nil || n.Cwd != "/w/new" || n.Title != "build a thing" || len(n.Messages) != 2 {
		t.Fatalf("ses_n = %+v", n)
	}
	if want := "inline answer\n\n> `bash` make\n\nfrom parts"; n.Messages[1].Text != want {
		t.Errorf("assistant = %q, want %q", n.Messages[1].Text, want)
	}
	if o := by["ses_old"]; o == nil || o.Title != "Old one" || o.Messages[0].Text != "legacy question" {
		t.Errorf("ses_old = %+v", o)
	}
}

func TestJSONIgnoredWhenDatabaseExists(t *testing.T) {
	root := t.TempDir()
	jsonSession(t, root, "ses_a", "")
	jsonMsg(t, root, "ses_a", "msg_1", "user", 1, m{"type": "text", "text": "stale"})
	w := openW(t, filepath.Join(root, "opencode.db"))
	w.v1(t)
	if r := collect(newC(root)); len(r.got) != 0 || len(r.warns) != 0 {
		t.Errorf("got %+v %v", r.got, r.warns)
	}
}

func TestCorruptDatabase(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "opencode.db"), bytes.Repeat([]byte("not sqlite "), 500), 0o644)
	r := collect(newC(root))
	if len(r.got) != 0 || len(r.warns) != 1 {
		t.Errorf("got %d sessions, warns %v", len(r.got), r.warns)
	}
}

func TestDetect(t *testing.T) {
	root := t.TempDir()
	c := &Collector{Roots: []string{root, filepath.Join(root, "missing")}}
	if d := c.Detect(); len(d) != 1 || d[0] != root {
		t.Errorf("Detect = %v", d)
	}
}
