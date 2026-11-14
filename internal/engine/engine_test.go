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
		if d != nil && !d.IsDir() && strings.HasSuffix(p, ".md") && d.Name() != "README.md" {
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
	if s.LastSync == "" || s.Chats != 1 || len(s.Sources) != 1+len(WebSources) || s.Sources[0].Sessions != 2 || !s.Sources[0].Detected || s.Sources[0].Kind != "local" {
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

func TestNewDoesNotCreateTheVault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cfgPath := filepath.Join(t.TempDir(), "config.json") // no file: the default vault applies
	e, err := New(cfgPath, []collector.Collector{&fake{sessions: []session.Session{mk("a1", "/work/alpha")}}})
	if err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(t.TempDir(), "other")
	if err := e.OverrideVault(other); err != nil {
		t.Fatal(err)
	}
	if st := e.Sync(); st.Created != 1 {
		t.Fatalf("stats = %+v", st)
	}
	if _, err := os.Stat(filepath.Join(home, "Cortex")); !os.IsNotExist(err) {
		t.Errorf("the default vault was created (err = %v)", err)
	}
}

func TestPausedSyncDoesNotCreateTheVault(t *testing.T) {
	f := &fake{}
	e, _, root := setup(t, f)
	e.Update(func(c *config.Config) { c.Paused = true })
	e.Sync()
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Errorf("vault exists while paused (err = %v)", err)
	}
}

func TestFailedWriteIsRetriedAndReportedEveryRound(t *testing.T) {
	// The fake yields its session once, like a collector that has marked it read.
	f := &onceFake{fake: fake{sessions: []session.Session{mk("a1", "/work/alpha")}}}
	e, _, root := setup(t, &f.fake)
	e.cols = []collector.Collector{f}
	// A file where the Projects folder should go makes the write fail.
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	obstacle := filepath.Join(root, "Projects")
	if err := os.WriteFile(obstacle, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	for round := 1; round <= 3; round++ {
		st := e.Sync()
		if st.Failed != 1 || st.Created != 0 {
			t.Fatalf("round %d: stats = %+v", round, st)
		}
		if errs := e.Snapshot().Errors; len(errs) != 1 || !strings.Contains(errs[0], "a1") {
			t.Fatalf("round %d: errors = %v", round, errs)
		}
	}

	os.Remove(obstacle)
	if st := e.Sync(); st.Created != 1 || st.Failed != 0 {
		t.Fatalf("after the obstacle is gone: %+v", st)
	}
	if n := len(notes(t, root)); n != 1 {
		t.Errorf("notes = %d", n)
	}
	if f.resets != 0 {
		t.Errorf("collectors were reset %d times", f.resets)
	}
	if st := e.Sync(); st.Unchanged != 0 || st.Failed != 0 || len(e.pending) != 0 {
		t.Errorf("a written session stayed pending: %+v", st)
	}
}

func TestPendingDroppedWhenTurnedOff(t *testing.T) {
	for name, off := range map[string]func(*config.Config){
		"project": func(c *config.Config) { c.ExcludedProjects = []string{"alpha"} },
		"source":  func(c *config.Config) { c.DisabledSources = []string{"fake"} },
	} {
		f := &onceFake{fake: fake{sessions: []session.Session{mk("a1", "/work/alpha")}}}
		e, _, root := setup(t, &f.fake)
		e.cols = []collector.Collector{f}
		os.MkdirAll(root, 0o755)
		obstacle := filepath.Join(root, "Projects")
		os.WriteFile(obstacle, nil, 0o644)
		e.Sync()
		if len(e.pending) != 1 {
			t.Fatalf("%s: pending = %d", name, len(e.pending))
		}
		// Update clears pending itself; put it back to test the per-round check.
		e.cfg.ExcludedProjects, e.cfg.DisabledSources = nil, nil
		cfg := clone(e.cfg)
		off(cfg)
		e.cfg = cfg
		os.Remove(obstacle)
		if st := e.Sync(); st.Created != 0 || len(e.pending) != 0 {
			t.Errorf("%s: stats = %+v, pending = %d", name, st, len(e.pending))
		}
		if n := len(notes(t, root)); n != 0 {
			t.Errorf("%s: wrote %d notes for a session turned off", name, n)
		}
	}
}

func TestUpdateClearsPending(t *testing.T) {
	f := &fake{sessions: []session.Session{mk("a1", "/work/alpha")}}
	e, _, root := setup(t, f)
	os.MkdirAll(root, 0o755)
	os.WriteFile(filepath.Join(root, "Projects"), nil, 0o644)
	e.Sync()
	if len(e.pending) != 1 {
		t.Fatalf("pending = %d", len(e.pending))
	}
	if err := e.Update(func(c *config.Config) { c.Paused = true }); err != nil {
		t.Fatal(err)
	}
	if len(e.pending) != 0 {
		t.Errorf("pending = %d after Update", len(e.pending))
	}
}

// onceFake yields its sessions only on the first Collect after a Reset.
type onceFake struct {
	fake
	done bool
}

func (o *onceFake) Reset() { o.fake.Reset(); o.done = false }
func (o *onceFake) Collect(fn func(*session.Session), warn func(error)) {
	if o.done {
		return
	}
	o.done = true
	o.fake.Collect(fn, warn)
}

func TestUpdateKeepsHandEdits(t *testing.T) {
	e, cfgPath, _ := setup(t, &fake{})
	// The user edits the file while Cortex runs.
	edited := &config.Config{Vault: filepath.Join(t.TempDir(), "v"), ExcludedProjects: []string{"hand"}, RedactPatterns: []string{"ACME-[0-9]+"}}
	if err := edited.Save(cfgPath); err != nil {
		t.Fatal(err)
	}
	if err := e.Update(func(c *config.Config) { c.Paused = true }); err != nil {
		t.Fatal(err)
	}
	got, _ := config.LoadFile(cfgPath)
	if !got.Paused || len(got.ExcludedProjects) != 1 || got.ExcludedProjects[0] != "hand" || got.Vault != edited.Vault || len(got.RedactPatterns) != 1 {
		t.Errorf("hand edits lost: %+v", got)
	}

	// A file that parses but holds an invalid pattern does not block the panel.
	os.WriteFile(cfgPath, []byte(`{"vault": "`+edited.Vault+`", "redact_patterns": ["("]}`), 0o600)
	if err := e.Update(func(c *config.Config) { c.Paused = false }); err != nil {
		t.Fatalf("invalid pattern on disk blocks Update: %v", err)
	}
	if got, _ := config.LoadFile(cfgPath); got.Paused || len(got.RedactPatterns) != 1 || got.RedactPatterns[0] != "ACME-[0-9]+" {
		t.Errorf("after invalid-pattern fallback: %+v", got)
	}
	e.Update(func(c *config.Config) { c.Paused = true })

	// A broken file falls back to what is in memory.
	os.WriteFile(cfgPath, []byte("{not json"), 0o600)
	if err := e.Update(func(c *config.Config) { c.Paused = false }); err != nil {
		t.Fatal(err)
	}
	if got, _ := config.LoadFile(cfgPath); got.Paused || got.ExcludedProjects[0] != "hand" {
		t.Errorf("fallback = %+v", got)
	}
}

func TestToolLinesShortenedAfterRedaction(t *testing.T) {
	// The secret straddles the 120-character mark: cutting first would leave
	// a prefix that no rule matches.
	line := "> `Bash` " + strings.Repeat("x", 120-len("> `Bash` ")-8) + " AKIAABCDEFGHIJKLMNOP"
	s := mk("a1", "/work/alpha")
	s.Messages[0].Text = line
	e, _, root := setup(t, &fake{sessions: []session.Session{s}})
	e.Sync()
	b, _ := os.ReadFile(notes(t, root)[0])
	if strings.Contains(string(b), "AKIA") {
		t.Errorf("part of the key survived:\n%s", b)
	}
	if !strings.Contains(string(b), "[REDACT") { // the mask itself may be cut short
		t.Errorf("no redaction mark:\n%s", b)
	}
}
