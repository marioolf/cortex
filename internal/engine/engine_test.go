package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marioolf/cortex/internal/collector"
	"github.com/marioolf/cortex/internal/config"
	"github.com/marioolf/cortex/internal/session"
)

// fake yields its sessions on every Collect, like a collector with no cache.
type fake struct {
	sessions []session.Session
	resets   int
	disabled bool
}

func (f *fake) ID() string { return "fake" }
func (f *fake) Detect() []string {
	if f.disabled {
		return nil
	}
	return []string{"/somewhere"}
}
func (f *fake) Reset() { f.resets++ }
func (f *fake) Collect(fn func(*session.Session), warn func(error)) {
	for _, s := range f.sessions {
		s := s
		fn(&s)
	}
}

func mk(id, cwd string) session.Session {
	return session.Session{
		Source: "fake", ID: id, Cwd: cwd, Title: "t " + id,
		Messages: []session.Message{{Role: "user", Text: "hello token=abcdef123456", Time: time.Now()}},
	}
}

func setup(t *testing.T, f *fake) (*Engine, string, string) {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "cfg", "config.json")
	vaultDir := filepath.Join(dir, "vault")
	cfg := &config.Config{Vault: vaultDir}
	if err := cfg.Save(cfgPath); err != nil {
		t.Fatal(err)
	}
	e, err := New(cfgPath, []collector.Collector{f})
	if err != nil {
		t.Fatal(err)
	}
	return e, cfgPath, vaultDir
}

func notes(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	filepath.WalkDir(root, func(p string, d os.DirEntry, _ error) error {
		if !d.IsDir() && strings.HasSuffix(p, ".md") && d.Name() != "README.md" {
			out = append(out, p)
		}
		return nil
	})
	return out
}

func TestSyncWritesAndRedacts(t *testing.T) {
	f := &fake{sessions: []session.Session{mk("a1", "/work/alpha"), mk("c1", "/home/someone")}}
	e, _, root := setup(t, f)
	st := e.Sync()
	if st.Created != 2 || st.Failed != 0 {
		t.Fatalf("stats = %+v", st)
	}
	files := notes(t, root)
	if len(files) != 2 {
		t.Fatalf("notes = %v", files)
	}
	for _, p := range files {
		b, _ := os.ReadFile(p)
		if strings.Contains(string(b), "abcdef123456") {
			t.Errorf("%s was not redacted", p)
		}
	}
	if st := e.Sync(); st.Unchanged != 2 {
		t.Errorf("second round = %+v", st)
	}
	s := e.Snapshot()
	if s.LastSync == "" || s.Chats != 1 || len(s.Sources) != 1 || s.Sources[0].Sessions != 2 || !s.Sources[0].Detected {
		t.Errorf("snapshot = %+v", s)
	}
}

func TestExcludedProjectStillListed(t *testing.T) {
	f := &fake{sessions: []session.Session{mk("a1", "/work/alpha")}}
	e, _, root := setup(t, f)
	if err := e.Update(func(c *config.Config) { c.ExcludedProjects = []string{"alpha"} }); err != nil {
		t.Fatal(err)
	}
	e.Sync()
	if n := len(notes(t, root)); n != 0 {
		t.Errorf("excluded project wrote %d notes", n)
	}
	s := e.Snapshot()
	if len(s.Projects) != 1 || s.Projects[0].Name != "alpha" || !s.Projects[0].Excluded || s.Projects[0].Sessions != 1 {
		t.Errorf("projects = %+v", s.Projects)
	}

	// Turning it back on writes the notes it skipped.
	e.Update(func(c *config.Config) { c.ExcludedProjects = nil })
	if st := e.Sync(); st.Created != 1 {
		t.Errorf("after re-enable = %+v", st)
	}
}

func TestDisabledSourceKeepsCounts(t *testing.T) {
	f := &fake{sessions: []session.Session{mk("a1", "/work/alpha")}}
	e, _, root := setup(t, f)
	e.Sync()
	e.Update(func(c *config.Config) { c.DisabledSources = []string{"fake"} })
	f.sessions = append(f.sessions, mk("a2", "/work/alpha"))
	e.Sync()
	s := e.Snapshot()
	if s.Sources[0].Enabled || s.Sources[0].Sessions != 1 {
		t.Errorf("source = %+v", s.Sources[0])
	}
	if n := len(notes(t, root)); n != 1 {
		t.Errorf("notes = %d", n)
	}
}

func TestPausedDoesNothing(t *testing.T) {
	f := &fake{sessions: []session.Session{mk("a1", "/work/alpha")}}
	e, _, root := setup(t, f)
	e.Update(func(c *config.Config) { c.Paused = true })
	if st := e.Sync(); st != (Stats{}) {
		t.Errorf("stats = %+v", st)
	}
	if n := len(notes(t, root)); n != 0 {
		t.Errorf("paused wrote %d notes", n)
	}
	if s := e.Snapshot(); !s.Paused || s.LastSync != "" {
		t.Errorf("snapshot = %+v", s)
	}
}

func TestUpdatePersistsAndResets(t *testing.T) {
	f := &fake{}
	e, cfgPath, _ := setup(t, f)
	newVault := filepath.Join(t.TempDir(), "other")
	if err := e.Update(func(c *config.Config) { c.Vault = newVault; c.Paused = true }); err != nil {
		t.Fatal(err)
	}
	got, err := config.LoadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if got.Vault != newVault || !got.Paused {
		t.Errorf("saved = %+v", got)
	}
	if f.resets != 1 {
		t.Errorf("resets = %d", f.resets)
	}
	if _, err := os.Stat(filepath.Join(newVault, "README.md")); err != nil {
		t.Errorf("new vault not opened: %v", err)
	}
	// A bad pattern is rejected and nothing is saved.
	if err := e.Update(func(c *config.Config) { c.RedactPatterns = []string{"("}; c.Paused = false }); err == nil {
		t.Error("invalid redact pattern accepted")
	}
	if got, _ := config.LoadFile(cfgPath); !got.Paused || len(got.RedactPatterns) != 0 {
		t.Errorf("failed update leaked into the file: %+v", got)
	}
}

func TestErrorsAreCapped(t *testing.T) {
	e, _, _ := setup(t, &fake{})
	for i := 0; i < 30; i++ {
		e.addError("e")
	}
	if n := len(e.Snapshot().Errors); n != maxErrors {
		t.Errorf("errors = %d", n)
	}
}
