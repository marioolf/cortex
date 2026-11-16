package copilot

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/marioolf/cortex/internal/session"
	_ "modernc.org/sqlite"
)

const storeSchema = `
CREATE TABLE sessions (id TEXT PRIMARY KEY, cwd TEXT, repository TEXT, host_type TEXT, branch TEXT, summary TEXT, agent_name TEXT, agent_description TEXT, created_at TEXT, updated_at TEXT);
CREATE TABLE turns (id INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT NOT NULL, turn_index INTEGER NOT NULL, user_message TEXT, assistant_response TEXT, timestamp TEXT);
INSERT INTO sessions (id, cwd, summary) VALUES ('s1', '/work/proj', 'Fix parser'), ('s2', NULL, NULL), ('empty', '/x', 'nothing'), ('dup', '/y', 'dup');
INSERT INTO turns (session_id, turn_index, user_message, assistant_response, timestamp) VALUES
 ('s1', 1, 'second q', 'second a', '2026-05-01T10:00:05.000Z'),
 ('s1', 0, 'first q', 'first a', '2026-05-01T10:00:00.000Z'),
 ('s2', 0, 'untitled question' || char(10) || 'more', NULL, NULL),
 ('dup', 0, 'from db', 'x', NULL);`

func makeStore(t *testing.T, root string) string {
	t.Helper()
	p := filepath.Join(root, "globalStorage", "github.copilot-chat", "session-store.db")
	write(t, p, "")
	db, err := sql.Open("sqlite", "file:"+p)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(storeSchema); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSessionStore(t *testing.T) {
	root := t.TempDir()
	makeStore(t, root)
	// "dup" also exists as a chatSessions file and must not be delivered twice.
	write(t, filepath.Join(root, "workspaceStorage", "h", "chatSessions", "dup.json"),
		`{"sessionId":"dup","requests":[{"message":"from file","response":[]}]}`)
	var got []*session.Session
	c := &Collector{Roots: []string{root}}
	c.Collect(func(s *session.Session) { got = append(got, s) }, func(err error) { t.Fatal(err) })
	by := map[string]*session.Session{}
	for _, s := range got {
		if by[s.ID] != nil {
			t.Fatalf("duplicate %s", s.ID)
		}
		by[s.ID] = s
	}
	if len(got) != 3 || by["empty"] != nil {
		t.Fatalf("got %d sessions", len(got))
	}
	s1 := by["s1"]
	if s1.Title != "Fix parser" || s1.Cwd != "/work/proj" || len(s1.Messages) != 4 ||
		s1.Messages[0].Text != "first q" || s1.Messages[1].Role != "assistant" || s1.Messages[0].Time.IsZero() {
		t.Errorf("s1 = %+v", s1)
	}
	if s2 := by["s2"]; s2.Title != "untitled question" || len(s2.Messages) != 1 {
		t.Errorf("s2 = %+v", s2)
	}
	if by["dup"].Messages[0].Text != "from file" {
		t.Errorf("dup = %+v", by["dup"])
	}
	// Unchanged database is skipped on the next pass.
	n := 0
	c.Collect(func(*session.Session) { n++ }, func(error) {})
	if n != 0 {
		t.Errorf("second pass delivered %d", n)
	}
}

func TestSessionStoreSkipsFileIDsAcrossPasses(t *testing.T) {
	root := t.TempDir()
	p := makeStore(t, root)
	write(t, filepath.Join(root, "workspaceStorage", "h", "chatSessions", "dup.json"),
		`{"sessionId":"dup","requests":[{"message":"from file","response":[]}]}`)
	c := &Collector{Roots: []string{root}}
	c.Collect(func(*session.Session) {}, func(err error) { t.Fatal(err) })
	// Second pass: the chatSessions file is unchanged, the store changes.
	db, err := sql.Open("sqlite", "file:"+p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO turns (session_id, turn_index, user_message, assistant_response) VALUES ('dup', 1, 'more', 'more'), ('s1', 2, 'q3', 'a3')`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	got := map[string]bool{}
	c.Collect(func(s *session.Session) { got[s.ID] = true }, func(err error) { t.Fatal(err) })
	if got["dup"] || !got["s1"] {
		t.Errorf("second pass delivered %v", got)
	}
	c.Reset()
	if len(c.fileIDs) != 0 {
		t.Error("Reset kept ids")
	}
}

func TestSessionStoreNotBlockedByEmptyFile(t *testing.T) {
	root := t.TempDir()
	makeStore(t, root)
	f := filepath.Join(root, "workspaceStorage", "h", "chatSessions", "dup.json")
	write(t, f, `{"sessionId":"dup","requests":[]}`)
	c := &Collector{Roots: []string{root}}
	got := map[string]bool{}
	c.Collect(func(s *session.Session) { got[s.ID] = true }, func(err error) { t.Fatal(err) })
	if !got["dup"] {
		t.Fatalf("empty placeholder blocked the store: %v", got)
	}
	// The file gains messages, then becomes empty again.
	write(t, f, `{"sessionId":"dup","requests":[{"message":"from file","response":[]}]}`)
	c.Collect(func(*session.Session) {}, func(err error) { t.Fatal(err) })
	if len(c.fileIDs) != 1 {
		t.Fatalf("fileIDs = %v", c.fileIDs)
	}
	write(t, f, `{"sessionId":"dup","requests":[]}  `)
	c.Collect(func(*session.Session) {}, func(err error) { t.Fatal(err) })
	if len(c.fileIDs) != 0 {
		t.Errorf("empty file kept its id: %v", c.fileIDs)
	}
}
