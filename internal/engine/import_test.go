package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marioolf/cortex/internal/importer"
	"github.com/marioolf/cortex/internal/session"
)

func exportFile(t *testing.T, convs ...map[string]any) string {
	t.Helper()
	b, err := json.Marshal(convs)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "conversations.json")
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func claudeExp(id, text string) map[string]any {
	return map[string]any{"uuid": id, "name": "Chat " + id, "chat_messages": []any{
		map[string]any{"sender": "human", "text": text, "created_at": "2025-03-01T10:00:00Z"}}}
}

// An import of the same file twice changes nothing; repeated and missing ids
// are reported, never written under another name or under an empty one.
func TestImportSkipsMissingAndRepeatedIDs(t *testing.T) {
	e, _, root := setup(t, &fake{})
	p := exportFile(t, claudeExp("c1", "first"), claudeExp("c1", "second loses"), claudeExp("", "no id"), claudeExp("c2", "other"))
	sum, err := e.Import(p)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Created != 2 || sum.Unreadable != 2 || sum.Conversations != 4 {
		t.Fatalf("%+v", sum)
	}
	joined := strings.Join(sum.Errors, "\n")
	if !strings.Contains(joined, "no id") || !strings.Contains(joined, "already appeared") {
		t.Errorf("errors: %q", sum.Errors)
	}
	if len(notes(t, root)) != 2 {
		t.Fatalf("notes: %v", notes(t, root))
	}
	again, err := e.Import(p)
	if err != nil || again.Created != 0 || again.Updated != 0 || again.Unchanged != 2 || again.Unreadable != 2 {
		t.Fatalf("second run %+v %v", again, err)
	}
	for _, n := range notes(t, root) {
		b, _ := os.ReadFile(n)
		if strings.Contains(string(b), "second loses") {
			t.Error("the repeated id overwrote the first conversation")
		}
	}
}

// An oversized conversation is counted and the import goes on.
func TestImportSkipsOversizedConversation(t *testing.T) {
	e, _, root := setup(t, &fake{})
	p := exportFile(t, claudeExp("a", "small"), claudeExp("big", strings.Repeat("x", 3<<20)), claudeExp("b", "small too"))
	sum, err := e.importWith(p, importer.Options{MaxElement: 1 << 20})
	if err != nil || sum.Created != 2 || sum.Unreadable != 1 || len(notes(t, root)) != 2 {
		t.Fatalf("%+v %v", sum, err)
	}
	if !strings.Contains(strings.Join(sum.Errors, " "), "larger than") {
		t.Errorf("%q", sum.Errors)
	}
}

// Every source is capped, before redaction; a secret before the cut is still
// redacted, and what is saved is far smaller than what came in.
func TestMessagesAreCappedForEverySource(t *testing.T) {
	old := maxMessageChars
	maxMessageChars = 1000
	defer func() { maxMessageChars = old }()
	huge := "key password=hunter2hunter2 " + strings.Repeat("é", 400_000) // ~800 KB stands for 400 MB

	check := func(t *testing.T, root string) {
		t.Helper()
		files := notes(t, root)
		if len(files) != 1 {
			t.Fatalf("notes %v", files)
		}
		b, _ := os.ReadFile(files[0])
		note := string(b)
		if len(note) > 4000 {
			t.Errorf("note is %d bytes", len(note))
		}
		if !strings.Contains(note, "[… 399") || !strings.Contains(note, "more characters not saved by Cortex]") {
			t.Errorf("no cut notice:\n%s", note[:min(len(note), 600)])
		}
		if strings.Contains(note, "hunter2hunter2") {
			t.Error("secret saved")
		}
	}
	t.Run("ingest", func(t *testing.T) {
		e, _, root := setup(t, &fake{})
		ingest(t, e, &session.Session{Source: "chatgpt", ID: "big", Title: "T", Messages: []session.Message{{Role: "user", Text: huge}}}, false)
		check(t, root)
	})
	t.Run("collector", func(t *testing.T) {
		s := mk("big", "/work/alpha")
		s.Messages = []session.Message{{Role: "user", Text: huge}}
		e, _, root := setup(t, &fake{sessions: []session.Session{s}})
		if st := e.Sync(); st.Created != 1 {
			t.Fatalf("%+v", st)
		}
		check(t, root)
	})
	t.Run("import", func(t *testing.T) {
		e, _, root := setup(t, &fake{})
		sum, err := e.Import(exportFile(t, claudeExp("big", huge)))
		if err != nil || sum.Created != 1 {
			t.Fatalf("%+v %v", sum, err)
		}
		check(t, root)
	})
}

// The title of a skipped conversation is redacted and cut before it is shown.
func TestImportSkipMessageRedactsAndCutsTheTitle(t *testing.T) {
	e, _, _ := setup(t, &fake{})
	c := claudeExp("", "text")
	c["name"] = "Token ghp_abcdefghijklmnopqrstuvwxyz0123 " + strings.Repeat("long ", 100)
	sum, err := e.Import(exportFile(t, c))
	if err != nil || sum.Unreadable != 1 || len(sum.Errors) != 1 {
		t.Fatalf("%+v %v", sum, err)
	}
	msg := sum.Errors[0]
	if strings.Contains(msg, "ghp_abcdefghijklmnopqrstuvwxyz0123") || !strings.Contains(msg, "no id") {
		t.Errorf("secret shown or reason lost: %q", msg)
	}
	if len([]rune(msg)) > 150 {
		t.Errorf("title not cut: %d characters", len([]rune(msg)))
	}
}
