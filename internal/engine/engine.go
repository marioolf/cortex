// Package engine runs sync rounds and holds everything a round needs: the
// config, the collectors, the redactor and the open vault. The control panel
// and the one-shot `cortex sync` both go through it.
package engine

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/marioolf/cortex/internal/collector"
	"github.com/marioolf/cortex/internal/config"
	"github.com/marioolf/cortex/internal/redact"
	"github.com/marioolf/cortex/internal/session"
	"github.com/marioolf/cortex/internal/summarize"
	"github.com/marioolf/cortex/internal/vault"
)

const maxErrors = 20

// Stats counts what one round did.
type Stats struct {
	Created   int `json:"created"`
	Updated   int `json:"updated"`
	Unchanged int `json:"unchanged"`
	Detached  int `json:"detached"`
	Failed    int `json:"failed"`
}

// WebSources are fed by the browser extension or by imports, not read from
// disk by a collector.
var WebSources = []string{"chatgpt", "claude-ai", "gemini"}

type SourceState struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"` // "local" for collectors, "web" for WebSources
	Detected bool   `json:"detected"`
	Enabled  bool   `json:"enabled"`
	Sessions int    `json:"sessions"`
}

type ProjectState struct {
	Name     string `json:"name"`
	Excluded bool   `json:"excluded"`
	Sessions int    `json:"sessions"`
}

// State is what the panel shows.
type State struct {
	Version    string         `json:"version"`
	Vault      string         `json:"vault"`
	Paused     bool           `json:"paused"`
	Autostart  bool           `json:"autostart"` // filled in by the caller; the engine does not know
	LastSync   string         `json:"last_sync"` // RFC3339, empty before the first round
	LastResult Stats          `json:"last_result"`
	Errors     []string       `json:"errors"`
	Sources    []SourceState  `json:"sources"`
	Projects   []ProjectState `json:"projects"`
	Chats      int            `json:"chats"` // sessions that belong to no project
	Summaries  SummaryState   `json:"summaries"`
	// Warnings are problems with the config file that did not stop Cortex
	// (an unusable summaries block); the panel shows them at the top.
	Warnings []string `json:"warnings"`
}

type seenSession struct{ source, project string }

type Engine struct {
	Version string

	// run serializes rounds and config updates; mu guards the fields below
	// and is only ever held briefly, so Snapshot never waits for a round.
	run sync.Mutex
	mu  sync.Mutex

	path      string
	cfg       *config.Config
	cols      []collector.Collector
	red       *redact.Redactor
	v         *vault.Vault // opened on first use; touched only while holding run
	seen      map[string]seenSession
	detected  map[string]bool
	lastSync  time.Time
	lastStats Stats
	errs      []string
	counts    map[string]int // notes per source in the vault, refreshed whenever it changes

	// Sessions whose note could not be written, already redacted, retried at
	// the start of every round. The collectors consider them read, so without
	// this they would be lost. Touched only while holding run.
	pending map[string]*session.Session

	// Summaries (see summaries.go).
	sum      sumState
	sumWG    sync.WaitGroup
	ctx      context.Context // cancelled by Stop: kills a running summary command
	cancel   context.CancelFunc
	now      func() time.Time // the clock of idle time, the rate limit and backoffs
	lookPath func(string) (string, error)
	runner   summarize.Runner
}

func New(configPath string, collectors []collector.Collector) (*Engine, error) {
	var cfg *config.Config
	var err error
	if configPath == "" {
		cfg, err = config.Load()
	} else {
		cfg, err = config.LoadFile(configPath)
	}
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	e := &Engine{
		path:     configPath,
		cfg:      cfg,
		cols:     collectors,
		seen:     map[string]seenSession{},
		detected: map[string]bool{},
		pending:  map[string]*session.Session{},
		now:      time.Now,
		lookPath: exec.LookPath,
	}
	e.ctx, e.cancel = context.WithCancel(context.Background())
	if e.path == "" {
		if e.path, err = config.Path(); err != nil {
			return nil, err
		}
	}
	if e.red, err = redact.New(cfg.RedactPatterns); err != nil {
		return nil, err
	}
	// The vault is not opened here: opening creates the folder, and a caller
	// that is about to OverrideVault must not leave the default one behind.
	e.detect()
	return e, nil
}

// OverrideVault points this engine at another vault without saving it, for
// `cortex sync -vault`. A later Update would persist it, so use it only in
// one-shot runs.
func (e *Engine) OverrideVault(dir string) error {
	e.run.Lock()
	defer e.run.Unlock()
	v, err := vault.Open(dir)
	if err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	cfg := clone(e.cfg)
	cfg.Vault = dir
	e.cfg, e.v = cfg, v
	e.counts = v.SourceCounts()
	return nil
}

// vault returns the open vault, opening the configured one on first use.
// Call it only while holding run.
func (e *Engine) vault(cfg *config.Config) (*vault.Vault, error) {
	if e.v == nil {
		v, err := vault.Open(cfg.Vault)
		if err != nil {
			return nil, err
		}
		e.v = v
		e.refreshCounts()
	}
	return e.v, nil
}

// refreshCounts caches the vault's per-source note counts for Snapshot. Call
// it only while holding run.
func (e *Engine) refreshCounts() {
	var c map[string]int
	if e.v != nil {
		c = e.v.SourceCounts()
	}
	e.mu.Lock()
	e.counts = c
	e.mu.Unlock()
}

func (e *Engine) detect() {
	found := map[string]bool{}
	for _, c := range e.cols {
		found[c.ID()] = len(c.Detect()) > 0
	}
	e.mu.Lock()
	e.detected = found
	e.mu.Unlock()
}

// Sync runs one round. While paused it does nothing.
func (e *Engine) Sync() Stats {
	e.run.Lock()
	defer e.run.Unlock()
	e.detect()

	e.mu.Lock()
	cfg, red := e.cfg, e.red // Update swaps these; it never edits them in place
	e.mu.Unlock()
	if cfg.Paused {
		// The panel still shows the web sources' counts: open an existing vault
		// for them, but do not create one just to look.
		if _, err := os.Stat(cfg.Vault); err == nil {
			e.vault(cfg)
		}
		return Stats{}
	}

	// Each round reports its own problems; ones that persist come back on their own.
	e.mu.Lock()
	e.errs = nil
	e.mu.Unlock()

	work := summarize.WorkDir(e.path)
	var st Stats
	v, err := e.vault(cfg)
	if err != nil {
		st.Failed++
		e.addError(err.Error())
		return st
	}
	warn := func(err error) {
		st.Failed++
		e.addError(err.Error())
	}
	// write saves s, or keeps it to retry next round.
	write := func(s *session.Session) {
		key := s.Source + "\x00" + s.ID
		res, err := v.Write(s)
		if err != nil {
			e.pending[key] = s
			warn(fmt.Errorf("%s %s: %w", s.Source, s.ID, err))
			return
		}
		delete(e.pending, key)
		if res != vault.Detached {
			e.remember(cfg, vault.KeyOf(s), session.Project(s.Cwd))
		}
		switch res {
		case vault.Created:
			st.Created++
		case vault.Updated:
			st.Updated++
		case vault.Unchanged:
			st.Unchanged++
		case vault.Detached:
			st.Detached++
		}
	}

	// Retry first, unless the user has since turned the session off.
	for key, s := range e.pending {
		if !cfg.SourceEnabled(s.Source) || cfg.ProjectExcluded(session.Project(s.Cwd)) {
			delete(e.pending, key)
			continue
		}
		write(s)
	}
	for _, c := range e.cols {
		if !cfg.SourceEnabled(c.ID()) {
			continue
		}
		c.Collect(func(s *session.Session) {
			// The summary tools save sessions of their own, started in our work
			// folder. Saving those would summarise the summaries, forever.
			if summarize.InWorkDir(s.Cwd, work) {
				return
			}
			project := session.Project(s.Cwd)
			// Record before filtering: the panel lists excluded projects too.
			e.mu.Lock()
			e.seen[s.Source+"\x00"+s.ID] = seenSession{s.Source, project}
			e.mu.Unlock()
			if cfg.ProjectExcluded(project) {
				return
			}
			scrub(s, red)
			write(s)
		}, warn)
	}

	e.mu.Lock()
	e.lastSync, e.lastStats = time.Now(), st
	e.mu.Unlock()
	e.scanSummaries(cfg, v)
	return st
}

func (e *Engine) addError(msg string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.errs = append(e.errs, msg)
	if len(e.errs) > maxErrors {
		e.errs = e.errs[len(e.errs)-maxErrors:]
	}
}

// Update applies f to a copy of the config and, if the result is usable,
// saves it, resets the collectors so the next round re-reads everything under
// the new rules, and reopens the vault if its path changed. On error nothing
// changes.
func (e *Engine) Update(f func(*config.Config)) error {
	e.run.Lock()
	defer e.run.Unlock()

	e.mu.Lock()
	old := e.cfg
	e.mu.Unlock()
	// Start from the file, so edits made by hand while we run are not lost.
	cfg := clone(e.diskConfig(old))
	f(cfg)
	if cfg.Vault == "" {
		return fmt.Errorf("vault path is empty")
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	red, err := redact.New(cfg.RedactPatterns)
	if err != nil {
		return err
	}
	v := e.v
	if cfg.Vault != old.Vault {
		if v, err = vault.Open(cfg.Vault); err != nil {
			return err
		}
	}
	if err := cfg.Save(e.path); err != nil {
		return err
	}
	for _, c := range e.cols {
		c.Reset()
	}
	e.pending = map[string]*session.Session{} // everything is collected again
	e.v = v
	e.mu.Lock()
	e.cfg, e.red = cfg, red
	e.mu.Unlock()
	e.refreshCounts()
	return nil
}

// diskConfig is the config file as it is now, or fallback when it is missing,
// does not parse or does not validate (a file that failed here would block
// every change made in the panel). LoadFile alone would turn a missing file
// into defaults.
func (e *Engine) diskConfig(fallback *config.Config) *config.Config {
	if _, err := os.Stat(e.path); err != nil {
		return fallback
	}
	c, err := config.LoadFile(e.path)
	if err != nil {
		return fallback
	}
	if _, err := redact.New(c.RedactPatterns); err != nil {
		return fallback
	}
	return c
}

func clone(c *config.Config) *config.Config {
	n := *c
	n.DisabledSources = slices.Clone(c.DisabledSources)
	n.ExcludedProjects = slices.Clone(c.ExcludedProjects)
	n.RedactPatterns = slices.Clone(c.RedactPatterns)
	n.Summaries.Command = slices.Clone(c.Summaries.Command)
	n.Summaries.Projects = slices.Clone(c.Summaries.Projects)
	n.Warnings = slices.Clone(c.Warnings)
	return &n
}

// Snapshot returns the current state for the panel.
func (e *Engine) Snapshot() State {
	e.mu.Lock()
	defer e.mu.Unlock()
	s := State{
		Version:    e.Version,
		Vault:      e.cfg.Vault,
		Paused:     e.cfg.Paused,
		LastResult: e.lastStats,
		Errors:     slices.Clone(e.errs),
		Sources:    []SourceState{},
		Projects:   []ProjectState{},
		Warnings:   append([]string{}, e.cfg.Warnings...),
	}
	// Summary failures outlive the round that clears e.errs.
	e.sum.mu.Lock()
	s.Errors = append(s.Errors, e.summaryErrors()...)
	e.sum.mu.Unlock()
	if s.Errors == nil {
		s.Errors = []string{}
	}
	s.Summaries = e.summaryState()
	if !e.lastSync.IsZero() {
		s.LastSync = e.lastSync.Format(time.RFC3339)
	}

	perSource := map[string]int{}
	perProject := map[string]int{}
	for _, x := range e.seen {
		perSource[x.source]++
		if x.project == "" {
			s.Chats++
		} else {
			perProject[x.project]++
		}
	}
	for _, c := range e.cols {
		id := c.ID()
		s.Sources = append(s.Sources, SourceState{id, "local", e.detected[id], e.cfg.SourceEnabled(id), perSource[id]})
	}
	for _, id := range WebSources {
		s.Sources = append(s.Sources, SourceState{id, "web", true, e.cfg.SourceEnabled(id), e.counts[id]})
	}
	// Excluded projects stay listed even with no sessions seen, so they can be turned back on.
	for _, name := range e.cfg.ExcludedProjects {
		if _, ok := perProject[name]; !ok && name != "" {
			perProject[name] = 0
		}
	}
	for name, n := range perProject {
		s.Projects = append(s.Projects, ProjectState{name, e.cfg.ProjectExcluded(name), n})
	}
	sort.Slice(s.Projects, func(i, j int) bool { return s.Projects[i].Name < s.Projects[j].Name })
	return s
}

// ExtensionToken is the current pairing code of the browser extension, empty
// while none exists.
func (e *Engine) ExtensionToken() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cfg.ExtensionToken
}
