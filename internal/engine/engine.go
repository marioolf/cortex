// Package engine runs sync rounds and holds everything a round needs: the
// config, the collectors, the redactor and the open vault. The control panel
// and the one-shot `cortex sync` both go through it.
package engine

import (
	"fmt"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/marioolf/cortex/internal/collector"
	"github.com/marioolf/cortex/internal/config"
	"github.com/marioolf/cortex/internal/redact"
	"github.com/marioolf/cortex/internal/session"
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

type SourceState struct {
	ID       string `json:"id"`
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
	v         *vault.Vault // touched only while holding run
	seen      map[string]seenSession
	detected  map[string]bool
	lastSync  time.Time
	lastStats Stats
	errs      []string
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
	}
	if e.path == "" {
		if e.path, err = config.Path(); err != nil {
			return nil, err
		}
	}
	if e.red, err = redact.New(cfg.RedactPatterns); err != nil {
		return nil, err
	}
	if e.v, err = vault.Open(cfg.Vault); err != nil {
		return nil, err
	}
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
	return nil
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
		return Stats{}
	}

	// Each round reports its own problems; ones that persist come back on their own.
	e.mu.Lock()
	e.errs = nil
	e.mu.Unlock()

	var st Stats
	writeFailed := false
	warn := func(err error) {
		st.Failed++
		e.addError(err.Error())
	}
	for _, c := range e.cols {
		if !cfg.SourceEnabled(c.ID()) {
			continue
		}
		c.Collect(func(s *session.Session) {
			project := session.Project(s.Cwd)
			// Record before filtering: the panel lists excluded projects too.
			e.mu.Lock()
			e.seen[s.Source+"\x00"+s.ID] = seenSession{s.Source, project}
			e.mu.Unlock()
			if cfg.ProjectExcluded(project) {
				return
			}
			s.Title = red.Apply(s.Title)
			for i := range s.Messages {
				s.Messages[i].Text = red.Apply(s.Messages[i].Text)
			}
			res, err := e.v.Write(s)
			if err != nil {
				writeFailed = true
				warn(fmt.Errorf("%s %s: %w", s.Source, s.ID, err))
				return
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
		}, warn)
	}

	// The collectors already consider those sessions read. A note locked by an
	// editor or a sync tool is usually free again by the next round, so retry.
	if writeFailed {
		for _, c := range e.cols {
			c.Reset()
		}
	}

	e.mu.Lock()
	e.lastSync, e.lastStats = time.Now(), st
	e.mu.Unlock()
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
	cfg := clone(old)
	f(cfg)
	if cfg.Vault == "" {
		return fmt.Errorf("vault path is empty")
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
	e.v = v
	e.mu.Lock()
	e.cfg, e.red = cfg, red
	e.mu.Unlock()
	return nil
}

func clone(c *config.Config) *config.Config {
	n := *c
	n.DisabledSources = slices.Clone(c.DisabledSources)
	n.ExcludedProjects = slices.Clone(c.ExcludedProjects)
	n.RedactPatterns = slices.Clone(c.RedactPatterns)
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
	}
	if s.Errors == nil {
		s.Errors = []string{}
	}
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
		s.Sources = append(s.Sources, SourceState{id, e.detected[id], e.cfg.SourceEnabled(id), perSource[id]})
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
