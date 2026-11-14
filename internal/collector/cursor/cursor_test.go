package cursor

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/marioolf/cortex/internal/session"
	_ "modernc.org/sqlite"
)

type m = map[string]any

type store struct {
	t  *testing.T
	db *sql.DB
}

// newStore creates a WAL-mode database that keeps new rows in its -wal, like
// a running app, with the table layout Cursor uses.
func newStore(t *testing.T, path string) *store {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(wal)&_pragma=wal_autocheckpoint(0)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	s := &store{t, db}
	s.exec(`CREATE TABLE IF NOT EXISTS cursorDiskKV (key TEXT UNIQUE ON CONFLICT REPLACE, value BLOB)`)
	s.exec(`CREATE TABLE IF NOT EXISTS ItemTable (key TEXT UNIQUE ON CONFLICT REPLACE, value BLOB)`)
	return s
}

func (s *store) exec(q string, args ...any) {
	s.t.Helper()
	if _, err := s.db.Exec(q, args...); err != nil {
		s.t.Fatalf("%s: %v", q, err)
	}
}

func (s *store) kv(table, key string, v any) {
	s.t.Helper()
	b, _ := json.Marshal(v)
	s.exec(`INSERT INTO `+table+` (key, value) VALUES (?, ?)`, key, string(b))
}

func (s *store) bubble(cid, bid string, v m) { s.kv("cursorDiskKV", "bubbleId:"+cid+":"+bid, v) }

type run struct {
	got   []*session.Session
	warns []error
}

func (r run) byID() map[string]*session.Session {
	out := map[string]*session.Session{}
	for _, s := range r.got {
		out[s.ID] = s
	}
	return out
}

func collect(c *Collector) run {
	var r run
	c.Collect(func(s *session.Session) { r.got = append(r.got, s) }, func(e error) { r.warns = append(r.warns, e) })
	return r
}

func ids(r run) string {
	var out []string
	for _, s := range r.got {
		out = append(out, s.ID)
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

// fixture builds a User dir with every format generation we support.
func fixture(t *testing.T) (root string, g *store) {
	root = t.TempDir()
	g = newStore(t, filepath.Join(root, "globalStorage", "state.vscdb"))

	// Current: composerData lists bubble ids; bubbles are separate rows.
	g.kv("cursorDiskKV", "composerData:c-new", m{"_v": 10, "composerId": "c-new", "name": "Refactor parser", "createdAt": 1700000000000,
		"fullConversationHeadersOnly": []m{{"bubbleId": "b1", "type": 1}, {"bubbleId": "b2", "type": 2}, {"bubbleId": "b3", "type": 2}, {"bubbleId": "b4", "type": 2}, {"bubbleId": "b5", "type": 2}, {"bubbleId": "bX", "type": 2}},
		"workspaceIdentifier":         m{"id": "w", "uri": m{"fsPath": "/home/u/proj", "scheme": "file"}}})
	g.bubble("c-new", "b1", m{"_v": 2, "type": 1, "text": "Please refactor the parser", "createdAt": "2025-03-01T10:00:00.000Z"})
	g.bubble("c-new", "b2", m{"type": 2, "text": "", "thinking": m{"text": "deep thoughts"}, "createdAt": "2025-03-01T10:00:01.000Z"})
	g.bubble("c-new", "b3", m{"type": 2, "text": "On it.", "createdAt": "2025-03-01T10:00:02.000Z"})
	g.bubble("c-new", "b4", m{"type": 2, "text": "", "createdAt": "2025-03-01T10:00:03.000Z",
		"toolFormerData": m{"name": "run_terminal_command_v2", "status": "completed", "params": `{"command":"go test ./...\nmore","cwd":"/x"}`, "result": "SECRET RESULT"}})
	g.bubble("c-new", "b5", m{"type": 2, "text": "Done.", "createdAt": "2025-03-01T10:00:04.000Z",
		"toolFormerData": m{"name": "read_file_v2", "params": m{"targetFile": "/home/u/proj/a.go"}}})

	// Newer still: headers chunked into separate rows; workspace only known via the workspace db.
	g.kv("cursorDiskKV", "composerData:c-chunk", m{"composerId": "c-chunk", "headerLog": m{"count": 2, "chunkCount": 2}})
	g.kv("cursorDiskKV", "composerConvHeaders:c-chunk:000000", []m{{"bubbleId": "k1", "type": 1}})
	g.kv("cursorDiskKV", "composerConvHeaders:c-chunk:000001", []m{{"bubbleId": "k2", "type": 2}})
	g.bubble("c-chunk", "k2", m{"type": 2, "text": "chunked answer"})
	g.bubble("c-chunk", "k1", m{"type": 1, "text": "chunked question\nsecond line"})

	// Older: the conversation is inline in composerData.
	g.kv("cursorDiskKV", "composerData:c-old", m{"composerId": "c-old", "richText": "", "text": "", "createdAt": 1690000000000,
		"conversation": []m{{"type": 1, "bubbleId": "o1", "text": "old question", "workspaceUris": []string{"file:///c%3A/Users/me/old%20proj"}}, {"type": 2, "bubbleId": "o2", "text": "old answer"}}})

	// Nothing to show.
	g.kv("cursorDiskKV", "composerData:c-empty", m{"composerId": "c-empty", "fullConversationHeadersOnly": []m{{"bubbleId": "e1", "type": 2}}})
	g.bubble("c-empty", "e1", m{"type": 2, "text": "  "})
	// Garbage values are skipped.
	g.exec(`INSERT INTO cursorDiskKV (key, value) VALUES ('composerData:c-junk', 'not json')`)
	g.bubble("c-new", "bad", m{"type": 2, "text": "unreferenced"})

	// Workspace storage maps composers to folders and holds a legacy chat panel.
	ws := newStore(t, filepath.Join(root, "workspaceStorage", "abc", "state.vscdb"))
	os.WriteFile(filepath.Join(root, "workspaceStorage", "abc", "workspace.json"), []byte(`{"folder":"file:///home/u/chunky"}`), 0o644)
	ws.kv("ItemTable", "composer.composerData", m{"allComposers": []m{{"composerId": "c-chunk", "name": "x", "createdAt": 1}}, "selectedComposerIds": []string{}})
	ws.kv("ItemTable", "workbench.panel.aichat.view.aichat.chatdata", m{"tabs": []m{{"tabId": "tab-1", "chatTitle": "Legacy chat", "bubbles": []m{
		{"type": "user", "text": "legacy q"}, {"type": "ai", "text": "legacy a"}}}}})
	return root, g
}

func TestCollect(t *testing.T) {
	root, g := fixture(t)
	c := &Collector{Roots: []string{root}}
	r := collect(c)
	for _, w := range r.warns {
		if !strings.Contains(w.Error(), "c-junk") {
			t.Errorf("unexpected warning: %v", w)
		}
	}
	by := r.byID()
	if got := ids(r); got != "c-chunk,c-new,c-old,tab-1" {
		t.Fatalf("sessions = %s", got)
	}

	n := by["c-new"]
	if n.Source != ID || n.Title != "Refactor parser" || n.Cwd != "/home/u/proj" || len(n.Messages) != 2 {
		t.Fatalf("c-new = %+v", n)
	}
	if n.Messages[0].Role != "user" || n.Messages[0].Text != "Please refactor the parser" {
		t.Errorf("user = %+v", n.Messages[0])
	}
	if !n.Messages[0].Time.Equal(time.Date(2025, 3, 1, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("time = %v", n.Messages[0].Time)
	}
	want := "On it.\n\n> `run_terminal_command` go test ./...\n\nDone.\n\n> `read_file` /home/u/proj/a.go"
	if n.Messages[1].Text != want {
		t.Errorf("assistant = %q, want %q", n.Messages[1].Text, want)
	}
	for _, bad := range []string{"deep thoughts", "SECRET RESULT", "unreferenced"} {
		if strings.Contains(n.Messages[1].Text, bad) {
			t.Errorf("%q leaked", bad)
		}
	}

	ch := by["c-chunk"]
	if ch.Cwd != "/home/u/chunky" || ch.Title != "chunked question" || len(ch.Messages) != 2 || ch.Messages[1].Text != "chunked answer" {
		t.Errorf("c-chunk = %+v", ch)
	}
	o := by["c-old"]
	if o.Cwd != `C:\Users\me\old proj` || o.Title != "old question" || len(o.Messages) != 2 {
		t.Errorf("c-old = %+v", o)
	}
	if !o.Messages[0].Time.Equal(time.UnixMilli(1690000000000)) {
		t.Errorf("c-old time = %v", o.Messages[0].Time)
	}
	l := by["tab-1"]
	if l.Title != "Legacy chat" || l.Cwd != "/home/u/chunky" || len(l.Messages) != 2 || l.Messages[1].Role != "assistant" {
		t.Errorf("tab-1 = %+v", l)
	}

	// Nothing changed: nothing reported.
	if r := collect(c); len(r.got) != 0 {
		t.Errorf("unchanged round: %s", ids(r))
	}

	// The database changes, but only one composer: only it is reported again.
	g.bubble("c-new", "b6", m{"type": 2, "text": "One more thing."})
	g.kv("cursorDiskKV", "composerData:c-new", m{"composerId": "c-new", "name": "Refactor parser",
		"fullConversationHeadersOnly": []m{{"bubbleId": "b1"}, {"bubbleId": "b3"}, {"bubbleId": "b6"}}, "workspaceIdentifier": m{"uri": m{"fsPath": "/home/u/proj"}}})
	r = collect(c)
	if ids(r) != "c-new" {
		t.Errorf("after change: %s", ids(r))
	}
	// b1 has type 1 and b3/b6 type 2 in the fixture rows.
	if len(r.got) == 1 && !strings.HasSuffix(r.got[0].Messages[len(r.got[0].Messages)-1].Text, "One more thing.") {
		t.Errorf("new bubble missing: %+v", r.got[0].Messages)
	}

	// Reset forgets both the file stamps and the fingerprints.
	c.Reset()
	if got := ids(collect(c)); got != "c-chunk,c-new,c-old,tab-1" {
		t.Errorf("after Reset: %s", got)
	}
}

func TestReadOnly(t *testing.T) {
	root, _ := fixture(t)
	var files []string
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			files = append(files, p)
		}
		return nil
	})
	snap := func() map[string][]byte {
		out := map[string][]byte{}
		for _, f := range files {
			out[f], _ = os.ReadFile(f)
		}
		return out
	}
	before := snap()
	if len(before) < 4 {
		t.Fatalf("fixture too small: %d files", len(before))
	}
	c := &Collector{Roots: []string{root}}
	if r := collect(c); len(r.got) == 0 {
		t.Fatal("collected nothing")
	}
	after := snap()
	for f, b := range before {
		// -shm is the WAL's shared-memory index; every WAL reader updates its
		// read marks there. It holds no data and is rebuilt from the -wal.
		if strings.HasSuffix(f, "-shm") {
			continue
		}
		if !bytes.Equal(b, after[f]) {
			t.Errorf("%s changed", f)
		}
	}
	var now []string
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			now = append(now, p)
		}
		return nil
	})
	if len(now) != len(files) {
		t.Errorf("files before %d, after %d: Collect created or removed files", len(files), len(now))
	}
}

func TestCorruptStore(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "globalStorage"), 0o755)
	os.WriteFile(filepath.Join(root, "globalStorage", "state.vscdb"), bytes.Repeat([]byte("garbage!"), 1000), 0o644)
	// A good workspace next to a broken one still works.
	os.MkdirAll(filepath.Join(root, "workspaceStorage", "bad"), 0o755)
	os.WriteFile(filepath.Join(root, "workspaceStorage", "bad", "state.vscdb"), []byte("short"), 0o644)
	r := collect(&Collector{Roots: []string{root}})
	if len(r.got) != 0 || len(r.warns) != 2 {
		t.Errorf("sessions %d, warns %v", len(r.got), r.warns)
	}
}

func TestMissingTable(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "globalStorage"), 0o755)
	s := newStore(t, filepath.Join(root, "globalStorage", "state.vscdb"))
	s.exec(`CREATE TABLE Other (a)`)
	r := collect(&Collector{Roots: []string{root}})
	if len(r.got) != 0 || len(r.warns) != 0 {
		t.Errorf("got %v %v", r.got, r.warns)
	}
}

func TestUriToPath(t *testing.T) {
	for in, want := range map[string]string{
		"file:///c%3A/Users/me/proj":                  `C:\Users\me\proj`,
		"file:///C:/Users/me/proj":                    `C:\Users\me\proj`,
		"file:///home/me/my%20proj":                   "/home/me/my proj",
		"file://server/share/dir":                     `\\server\share\dir`,
		"vscode-remote://wsl%2Bubuntu/home/me/proj":   "/home/me/proj",
		"vscode-remote://ssh-remote+box/srv/app":      "/srv/app",
		"/already/a/path":                             "/already/a/path",
		"":                                            "",
		"vscode-remote://wsl%2Bubuntu":                "",
		"file:///home/me/p?x=1#frag":                  "/home/me/p",
		"file:///c%3A":                                `C:`,
		"untitled:Untitled-1":                         "untitled:Untitled-1",
		"vscode-remote://dev-container+abc/workspace": "/workspace",
	} {
		if got := uriToPath(in); got != want {
			t.Errorf("uriToPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestToolLineIsNotTruncated(t *testing.T) {
	b := bubble{}
	b.Tool.Name = "grep_search"
	b.Tool.RawArgs = json.RawMessage(fmt.Sprintf(`{"query": %q}`, strings.Repeat("é", 200)))
	got := b.toolLine()
	// Shortening happens after redaction, in the engine (session.ShortenToolLines).
	if r := []rune(strings.TrimPrefix(got, "> `grep_search` ")); len(r) != 200 {
		t.Errorf("len = %d", len(r))
	}
	b.Tool.RawArgs = nil
	if got := b.toolLine(); got != "> `grep_search`" {
		t.Errorf("bare = %q", got)
	}
}
