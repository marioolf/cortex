package vault

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marioolf/cortex/internal/session"
)

func open(t *testing.T, root string) *Vault {
	t.Helper()
	v, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	v.Loc = time.UTC
	return v
}

func sample() *session.Session {
	at := time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC)
	return &session.Session{
		Source: "claude-code",
		ID:     "a1b2c3d4-0000-0000-0000-000000000000",
		Cwd:    "/work/demo",
		Title:  `Fix: the "parser" [v2]?`,
		Messages: []session.Message{
			{Role: "user", Text: "hello", Time: at},
			{Role: "assistant", Text: "hi <!-- cortex:end --> there", Time: at.Add(time.Minute)},
		},
	}
}

func write(t *testing.T, v *Vault, s *session.Session, want Result) {
	t.Helper()
	got, err := v.Write(s)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("result = %d, want %d", got, want)
	}
}

func TestWriteLifecycle(t *testing.T) {
	root := t.TempDir()
	v := open(t, root)
	s := sample()
	write(t, v, s, Created)

	path := filepath.Join(root, "Projects", "demo", "2026-01-02 Fix the parser v2 (a1b2c3d4).md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	note := string(data)
	for _, want := range []string{
		`session: "a1b2c3d4-0000-0000-0000-000000000000"`,
		`project: "demo"`,
		"started: 2026-01-02T10:00:00Z",
		"## User · 2026-01-02 10:00\n\nhello",
		"## Assistant · 2026-01-02 10:01",
	} {
		if !strings.Contains(note, want) {
			t.Errorf("note lacks %q:\n%s", want, note)
		}
	}
	if strings.Count(note, endMarker) != 1 {
		t.Errorf("end marker from the conversation was not neutralised:\n%s", note)
	}

	write(t, v, s, Unchanged)

	// The user adds their own text, renames the note, and the session grows.
	edited := strings.Replace(note, "# Fix", "my notes above\n\n# Fix", 1) + "\nmy notes below\n"
	moved := filepath.Join(root, "renamed.md")
	if err := os.WriteFile(moved, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	os.Remove(path)

	v = open(t, root) // a later run
	s.Messages = append(s.Messages, session.Message{Role: "user", Text: "one more thing"})
	write(t, v, s, Updated)

	data, _ = os.ReadFile(moved)
	note = string(data)
	for _, want := range []string{"my notes above", "my notes below", "one more thing"} {
		if !strings.Contains(note, want) {
			t.Errorf("after update, note lacks %q:\n%s", want, note)
		}
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("a second note was created instead of updating the renamed one")
	}

	// Removing the markers hands the note over to the user.
	os.WriteFile(moved, []byte(strings.ReplaceAll(note, startMarker, "")), 0o644)
	write(t, open(t, root), s, Detached)
}

func TestChatsWithoutProject(t *testing.T) {
	root := t.TempDir()
	v := open(t, root)
	s := sample()
	s.Cwd = "/home/someone"
	s.Title = ""
	write(t, v, s, Created)
	if _, err := os.Stat(filepath.Join(root, "Chats", "claude-code", "2026-01-02 Untitled (a1b2c3d4).md")); err != nil {
		t.Error(err)
	}
}

func TestShortIDClash(t *testing.T) {
	root := t.TempDir()
	v := open(t, root)
	a, b := sample(), sample()
	b.ID = "a1b2c3d4-1111-1111-1111-111111111111"
	write(t, v, a, Created)
	write(t, v, b, Created)
	files, _ := filepath.Glob(filepath.Join(root, "Projects", "demo", "*.md"))
	if len(files) != 2 {
		t.Errorf("want 2 notes, got %v", files)
	}
}

func TestRenameBetweenWrites(t *testing.T) {
	root := t.TempDir()
	v := open(t, root)
	s := sample()
	write(t, v, s, Created)
	old, _ := filepath.Glob(filepath.Join(root, "Projects", "demo", "*.md"))
	moved := filepath.Join(root, "mine.md")
	if len(old) != 1 || os.Rename(old[0], moved) != nil {
		t.Fatalf("setup failed: %v", old)
	}

	s.Messages = append(s.Messages, session.Message{Role: "user", Text: "later"})
	write(t, v, s, Updated) // same *Vault, no reopen
	var notes []string
	filepath.WalkDir(root, func(p string, d os.DirEntry, _ error) error {
		if !d.IsDir() && strings.HasSuffix(p, ".md") && d.Name() != "README.md" {
			notes = append(notes, p)
		}
		return nil
	})
	if len(notes) != 1 || notes[0] != moved {
		t.Errorf("want only %s, got %v", moved, notes)
	}
}

func TestLongNamesStayWithinLimits(t *testing.T) {
	root := t.TempDir()
	v := open(t, root)
	long := strings.Repeat("😀", 70)
	a, b := sample(), sample()
	a.Title, a.Cwd = long, "/work/"+long
	// Same short ID and a long full ID force the fallback name.
	a.ID = "a1b2c3d4-" + strings.Repeat("x", 120)
	b.Title, b.Cwd = long, a.Cwd
	b.ID = "a1b2c3d4-" + strings.Repeat("y", 120)
	write(t, v, a, Created)
	write(t, v, b, Created)

	var n int
	filepath.WalkDir(filepath.Join(root, "Projects"), func(p string, d os.DirEntry, _ error) error {
		if d.IsDir() && p != filepath.Join(root, "Projects") && len(d.Name()) > 100 {
			t.Errorf("folder name is %d bytes", len(d.Name()))
		}
		if !d.IsDir() {
			n++
			if len(d.Name()) > 200 {
				t.Errorf("file name is %d bytes", len(d.Name()))
			}
		}
		return nil
	})
	if n != 2 {
		t.Errorf("want 2 notes, got %d", n)
	}
}

func TestSafeName(t *testing.T) {
	cases := []struct {
		in   string
		max  int
		want string
	}{
		{"a\x7fb", 50, "a b"},
		{"re‮v‏e⁦al", 50, "re v e al"},
		{"😀😀😀", 9, "😀😀"}, // never splits a character
		{"abc def", 5, "abc d"},
		{"abc def", 4, "abc"},
		{"..x..", 50, "x"},
	}
	for _, c := range cases {
		if got := safeName(c.in, c.max); got != c.want {
			t.Errorf("safeName(%q, %d) = %q, want %q", c.in, c.max, got, c.want)
		}
	}
}

func TestNotesInHiddenFoldersAreFound(t *testing.T) {
	for _, dir := range []string{".archive", ".trash"} {
		root := t.TempDir()
		v := open(t, root)
		s := sample()
		write(t, v, s, Created)
		old, _ := filepath.Glob(filepath.Join(root, "Projects", "demo", "*.md"))
		if len(old) != 1 {
			t.Fatalf("setup failed: %v", old)
		}
		os.MkdirAll(filepath.Join(root, dir), 0o755)
		moved := filepath.Join(root, dir, "note.md")
		os.Rename(old[0], moved)

		s.Messages = append(s.Messages, session.Message{Role: "user", Text: "later"})
		write(t, open(t, root), s, Updated) // a later run: the note is updated where it lies
		if left, _ := filepath.Glob(filepath.Join(root, "Projects", "demo", "*.md")); len(left) != 0 {
			t.Errorf("%s: a duplicate was created: %v", dir, left)
		}
		data, _ := os.ReadFile(moved)
		if !strings.Contains(string(data), "later") {
			t.Errorf("%s: moved note was not updated", dir)
		}

		// Once it is gone for good (trash emptied) the next change writes it again.
		os.Remove(moved)
		s.Messages = append(s.Messages, session.Message{Role: "user", Text: "again"})
		write(t, open(t, root), s, Created)
	}
}

func TestSessionWithoutTimestampsIsDatedWhenFirstSeen(t *testing.T) {
	root := t.TempDir()
	v := open(t, root)
	now := time.Date(2026, 3, 4, 22, 30, 0, 0, time.UTC)
	v.Now = func() time.Time { return now }
	s := &session.Session{
		Source: "chatgpt", ID: "web-1", Title: "Plants", URL: "https://chatgpt.com/c/web-1",
		Messages: []session.Message{{Role: "user", Text: "hello"}, {Role: "assistant", Text: "hi"}},
	}
	write(t, v, s, Created)

	path := filepath.Join(root, "Chats", "chatgpt", "2026-03-04 Plants (web-1).md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	note := string(data)
	for _, want := range []string{"started: 2026-03-04T22:30:00Z\n", `url: "https://chatgpt.com/c/web-1"` + "\n", "## User\n\nhello", "## Assistant\n\nhi"} {
		if !strings.Contains(note, want) {
			t.Errorf("note lacks %q:\n%s", want, note)
		}
	}

	// A later round, on another day, changes nothing; a new message only touches the block.
	v.Now = func() time.Time { return now.Add(48 * time.Hour) }
	write(t, v, s, Unchanged)
	s.Messages = append(s.Messages, session.Message{Role: "user", Text: "more"})
	write(t, v, s, Updated)
	after, _ := os.ReadFile(path)
	if !strings.Contains(string(after), "started: 2026-03-04T22:30:00Z\n") || !strings.Contains(string(after), "more") {
		t.Errorf("after update:\n%s", after)
	}
}

func TestSourceCountsComeFromTheNotes(t *testing.T) {
	root := t.TempDir()
	v := open(t, root)
	for _, s := range []*session.Session{
		{Source: "gemini", ID: "g1", Title: "a", Messages: []session.Message{{Role: "user", Text: "x"}}},
		{Source: "gemini", ID: "g2", Title: "b", Messages: []session.Message{{Role: "user", Text: "x"}}},
		{Source: "chatgpt", ID: "c1", Title: "c", Messages: []session.Message{{Role: "user", Text: "x"}}},
	} {
		write(t, v, s, Created)
	}
	// A fresh Vault on the same folder, as after a restart.
	got := open(t, root).SourceCounts()
	if got["gemini"] != 2 || got["chatgpt"] != 1 || len(got) != 2 {
		t.Errorf("counts = %v", got)
	}
}

// The same id under two sources is two sessions: a push for "chatgpt" must not
// land in the note of a "claude-ai" conversation that happens to share it.
func TestSameIDUnderTwoSourcesMakesTwoNotes(t *testing.T) {
	root := t.TempDir()
	v := open(t, root)
	mk := func(source, text string) *session.Session {
		return &session.Session{Source: source, ID: "shared-id-1", Title: "T",
			Messages: []session.Message{{Role: "user", Text: text}}}
	}
	write(t, v, mk("claude-ai", "from claude"), Created)
	write(t, v, mk("chatgpt", "from chatgpt"), Created)
	pa, pb := v.NotePath(Key{"claude-ai", "shared-id-1"}), v.NotePath(Key{"chatgpt", "shared-id-1"})
	if pa == "" || pb == "" || pa == pb {
		t.Fatalf("paths %q %q", pa, pb)
	}
	a, _ := os.ReadFile(pa)
	b, _ := os.ReadFile(pb)
	if !strings.Contains(string(a), "from claude") || strings.Contains(string(a), "from chatgpt") ||
		!strings.Contains(string(b), "from chatgpt") || strings.Contains(string(b), "from claude") {
		t.Fatalf("notes mixed:\n%s\n%s", a, b)
	}
	// The same source and id still merge into one note, also after a restart.
	write(t, v, mk("chatgpt", "from chatgpt, edited"), Updated)
	v2 := open(t, root)
	write(t, v2, mk("claude-ai", "from claude, edited"), Updated)
	if v2.NotePath(Key{"chatgpt", "shared-id-1"}) != pb || v2.NotePath(Key{"claude-ai", "shared-id-1"}) != pa {
		t.Fatal("index differs after reopening")
	}
	if got := v2.SourceCounts(); got["chatgpt"] != 1 || got["claude-ai"] != 1 {
		t.Fatalf("counts %v", got)
	}
	if k, err := v2.SessionOf(pb); err != nil || k != (Key{"chatgpt", "shared-id-1"}) {
		t.Fatalf("SessionOf = %v %v", k, err)
	}
	// Summaries are per note as well.
	_, h, _ := v2.ConversationText(Key{"chatgpt", "shared-id-1"})
	if err := v2.WriteSummary(Key{"chatgpt", "shared-id-1"}, h, "T", "only for chatgpt"); err != nil {
		t.Fatal(err)
	}
	if _, ok := v2.Summary(Key{"claude-ai", "shared-id-1"}); ok {
		t.Fatal("summary leaked to the other source")
	}
}
