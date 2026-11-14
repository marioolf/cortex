package copilot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marioolf/cortex/internal/session"
)

const snapshot = `{"version":3,"sessionId":"sess-json","customTitle":"Fix the parser","creationDate":1700000000000,
"requests":[{"requestId":"r1","timestamp":1700000001000,"message":{"text":"how do I parse dates?","parts":[]},
"response":[{"value":"Use ","supportThemeIcons":false},{"kind":"markdownContent","content":{"value":"time.Parse."}},
{"kind":"thinking","value":"secret reasoning"},
{"kind":"toolInvocationSerialized","toolId":"copilot_readFile","invocationMessage":{"value":"Reading x.go"},"pastTenseMessage":{"value":"Read x.go\nsecond line"}},
{"kind":"inlineReference","inlineReference":{"path":"/x"}}]},
{"requestId":"r2","message":"old style string prompt","response":[]}]}`

const logFile = `{"kind":0,"v":{"version":3,"sessionId":"sess-log","requests":[]}}
{"kind":1,"k":["responderUsername"],"v":"GitHub Copilot"}
{"kind":2,"k":["requests"],"v":[{"requestId":"a","timestamp":1700000002000,"message":{"text":"first\nline two"},"response":[]}]}
{"kind":2,"k":["requests",0,"response"],"v":[{"kind":"markdownContent","content":{"value":"Hello"}}]}
{"kind":2,"k":["requests",0,"response"],"v":[{"kind":"markdownContent","content":{"value":" there"}}]}
{"kind":2,"k":["requests"],"v":[{"requestId":"b","message":{"text":"dropped later"},"response":[]}]}
{"kind":2,"k":["requests"],"i":1}
{"kind":2,"k":["requests"],"v":[{"requestId":"c","message":{"text":"second"},"response":[]}]}
{"kind":1,"k":["customTitle"],"v":"Log title"}
{"kind":3,"k":["customTitle"]}
not json at all
`

func write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshot(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.json")
	write(t, p, snapshot)
	s, err := ParseFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if s.ID != "sess-json" || s.Title != "Fix the parser" || len(s.Messages) != 3 {
		t.Fatalf("got %+v", s)
	}
	want := "Use time.Parse.\n\n> `copilot_readFile` Read x.go"
	if s.Messages[1].Text != want {
		t.Errorf("assistant = %q", s.Messages[1].Text)
	}
	if s.Messages[0].Time.IsZero() || s.Messages[2].Role != "user" || s.Messages[2].Text != "old style string prompt" {
		t.Errorf("messages = %+v", s.Messages)
	}
}

func TestLogReplay(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sess-file.jsonl")
	write(t, p, logFile)
	s, err := ParseFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if s.ID != "sess-log" || len(s.Messages) != 3 {
		t.Fatalf("got %+v", s)
	}
	if s.Messages[1].Text != "Hello there" || s.Messages[2].Text != "second" {
		t.Errorf("messages = %+v", s.Messages)
	}
	if s.Title != "first" { // custom title was deleted
		t.Errorf("title = %q", s.Title)
	}
}

func TestLogWithoutSnapshot(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.jsonl")
	write(t, p, `{"kind":1,"k":["x"],"v":1}`+"\n")
	if _, err := ParseFile(p); err == nil {
		t.Error("want error")
	}
}

func TestUriToPath(t *testing.T) {
	for in, want := range map[string]string{
		"file:///c%3A/Users/x/proj":                `C:\Users\x\proj`,
		"file:///home/x/my%20proj":                 "/home/x/my proj",
		"vscode-remote://wsl%2Bubuntu/home/x/proj": "/home/x/proj",
		"file://wsl.localhost/Ubuntu/home/x":       `\\wsl.localhost\Ubuntu\home\x`,
		"vscode-vfs://github/owner/repo":           "",
		"":                                         "",
	} {
		if got := uriToPath(in); got != want {
			t.Errorf("uriToPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWorkspaceCwd(t *testing.T) {
	d := t.TempDir()
	write(t, filepath.Join(d, "multi", "workspace.json"), `{"workspace":"file:///home/x/code/a.code-workspace"}`)
	if got := workspaceCwd(filepath.Join(d, "multi")); got != "/home/x/code" {
		t.Errorf("multi-root = %q", got)
	}
	if got := workspaceCwd(filepath.Join(d, "missing")); got != "" {
		t.Errorf("missing = %q", got)
	}
}

func TestCollect(t *testing.T) {
	root := t.TempDir()
	ws := filepath.Join(root, "workspaceStorage", "h1")
	write(t, filepath.Join(ws, "workspace.json"), `{"folder":"vscode-remote://wsl%2Bubuntu/home/x/proj"}`)
	write(t, filepath.Join(ws, "chatSessions", "one.json"), snapshot)
	write(t, filepath.Join(ws, "chatSessions", "two.jsonl"), logFile)
	write(t, filepath.Join(ws, "chatSessions", "bad.json"), `{broken`)
	write(t, filepath.Join(ws, "chatSessions", "empty.json"), `{"sessionId":"e","requests":[]}`)
	write(t, filepath.Join(root, "globalStorage", "emptyWindowChatSessions", "win.jsonl"),
		`{"kind":0,"v":{"sessionId":"empty-window","requests":[{"message":{"text":"hi"},"response":[]}]}}`+"\n")

	c := &Collector{Roots: []string{root, filepath.Join(root, "nope")}}
	got := map[string]*session.Session{}
	var warns int
	collect := func() {
		c.Collect(func(s *session.Session) { got[s.ID] = s }, func(error) { warns++ })
	}
	collect()
	if len(got) != 3 || warns != 1 {
		t.Fatalf("sessions=%d warns=%d", len(got), warns)
	}
	if got["sess-json"].Cwd != "/home/x/proj" || got["empty-window"].Cwd != "" {
		t.Errorf("cwd: %q / %q", got["sess-json"].Cwd, got["empty-window"].Cwd)
	}

	// Unchanged files are skipped; the broken one is retried and warns again.
	n := 0
	c.Collect(func(*session.Session) { n++ }, func(error) { warns++ })
	if n != 0 || warns != 2 {
		t.Errorf("second round: n=%d warns=%d", n, warns)
	}
	c.Reset()
	c.Collect(func(*session.Session) { n++ }, func(error) {})
	if n != 3 {
		t.Errorf("after Reset n=%d", n)
	}
	if d := c.Detect(); len(d) != 1 || !strings.HasPrefix(d[0], root) {
		t.Errorf("Detect = %v", d)
	}
}
