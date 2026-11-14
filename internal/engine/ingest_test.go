package engine

import (
	"os"
	"strings"
	"testing"

	"github.com/marioolf/cortex/internal/collector"
	"github.com/marioolf/cortex/internal/config"
	"github.com/marioolf/cortex/internal/session"
)

func web(id, text string) *session.Session {
	return &session.Session{
		Source: "chatgpt", ID: id, Title: "Plan password=hunter2hunter2", URL: "https://chatgpt.com/c/" + id,
		Messages: []session.Message{{Role: "user", Text: text}, {Role: "assistant", Text: "> `web` " + strings.Repeat("x", 200)}},
	}
}

func ingest(t *testing.T, e *Engine, s *session.Session, explicit bool) string {
	t.Helper()
	res, err := e.Ingest(s, explicit)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestIngestPipeline(t *testing.T) {
	e, _, root := setup(t, &fake{})
	s := web("conv1", "my token=abcdef123456 here")
	if got := ingest(t, e, s, false); got != "created" {
		t.Fatalf("first = %s", got)
	}
	if got := ingest(t, e, s, false); got != "unchanged" {
		t.Errorf("same again = %s", got)
	}
	s.Messages = append(s.Messages, session.Message{Role: "user", Text: "one more"})
	if got := ingest(t, e, s, false); got != "updated" {
		t.Errorf("changed = %s", got)
	}

	files := notes(t, root)
	if len(files) != 1 {
		t.Fatalf("notes = %v", files)
	}
	b, _ := os.ReadFile(files[0])
	note := string(b)
	if strings.Contains(note, "abcdef123456") || strings.Contains(note, "hunter2hunter2") {
		t.Errorf("secrets in the note:\n%s", note)
	}
	if strings.Contains(note, strings.Repeat("x", 150)) {
		t.Error("tool line not shortened")
	}
	if !strings.Contains(s.Title, "hunter2hunter2") || !strings.Contains(s.Messages[0].Text, "abcdef123456") {
		t.Error("the caller's session was modified")
	}
	if !strings.Contains(note, "one more") || !strings.Contains(note, "source: \"chatgpt\"") {
		t.Errorf("note:\n%s", note)
	}
}

func TestIngestRespectsPauseAndSourceUnlessExplicit(t *testing.T) {
	for name, set := range map[string]func(*config.Config){
		"paused":     func(c *config.Config) { c.Paused = true },
		"source off": func(c *config.Config) { c.DisabledSources = []string{"chatgpt"} },
	} {
		t.Run(name, func(t *testing.T) {
			e, _, root := setup(t, &fake{})
			if err := e.Update(set); err != nil {
				t.Fatal(err)
			}
			if got := ingest(t, e, web("c1", "hi"), false); got != "skipped" {
				t.Errorf("pushed = %s", got)
			}
			if n := len(notes(t, root)); n != 0 {
				t.Errorf("skipped but wrote %d notes", n)
			}
			// An import is what the user asked for.
			if got := ingest(t, e, web("c1", "hi"), true); got != "created" {
				t.Errorf("explicit = %s", got)
			}
		})
	}
}

func TestIngestWriteErrorIsReturned(t *testing.T) {
	e, _, root := setup(t, &fake{})
	ingest(t, e, web("c1", "hi"), false)
	// Make the folder for new chatgpt notes impossible to create.
	if err := os.RemoveAll(root + "/Chats"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(root+"/Chats", []byte("not a folder"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Ingest(web("c2", "hi"), false); err == nil {
		t.Error("write error swallowed")
	}
}

func TestWebSourcesInStateAndCountsSurviveRestart(t *testing.T) {
	e, cfgPath, _ := setup(t, &fake{})
	ingest(t, e, web("a", "x"), false)
	ingest(t, e, web("b", "y"), false)
	g := web("g", "z")
	g.Source = "gemini"
	ingest(t, e, g, false)

	check := func(e *Engine) {
		t.Helper()
		got := map[string]SourceState{}
		for _, s := range e.Snapshot().Sources {
			got[s.ID] = s
		}
		for id, n := range map[string]int{"chatgpt": 2, "claude-ai": 0, "gemini": 1} {
			s := got[id]
			if s.Kind != "web" || !s.Detected || !s.Enabled || s.Sessions != n {
				t.Errorf("%s = %+v, want %d sessions", id, s, n)
			}
		}
	}
	check(e)

	// A restart: a new engine over the same config. Nothing collects web chats again.
	e2, err := New(cfgPath, []collector.Collector{&fake{}})
	if err != nil {
		t.Fatal(err)
	}
	e2.Sync()
	check(e2)

	// Also while paused.
	e3, _ := New(cfgPath, []collector.Collector{&fake{}})
	e3.Update(func(c *config.Config) { c.Paused = true })
	e3.Sync()
	check(e3)
}
