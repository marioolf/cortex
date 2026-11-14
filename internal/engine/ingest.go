package engine

import (
	"fmt"
	"slices"

	"github.com/marioolf/cortex/internal/redact"
	"github.com/marioolf/cortex/internal/session"
	"github.com/marioolf/cortex/internal/summarize"
	"github.com/marioolf/cortex/internal/vault"
)

// maxTitleChars is the longest title redaction and the vault ever see.
const maxTitleChars = 500

// maxMessageChars is the per-message cap; tests lower it.
var maxMessageChars = session.MaxMessageChars

// scrub prepares a session for the vault, in place: it caps the size of the
// title and of every message first (so redaction has a bounded amount of
// work), then redacts, then shortens tool lines (after redacting: see
// session.ShortenToolLines). Every source goes through it: collectors, the
// extension and import.
func scrub(s *session.Session, red *redact.Redactor) {
	s.Title = red.Apply(session.CapText(s.Title, maxTitleChars))
	for i := range s.Messages {
		m := &s.Messages[i]
		m.Text = session.ShortenToolLines(red.Apply(session.CapText(m.Text, maxMessageChars)))
	}
}

// Results of Ingest.
const (
	IngestCreated   = "created"
	IngestUpdated   = "updated"
	IngestUnchanged = "unchanged"
	IngestSkipped   = "skipped"
)

// Ingest saves a session that was pushed to us instead of collected: by the
// browser extension (explicit false) or from a file the user imported
// (explicit true). It goes through the same steps as a collected session, and
// is serialised with sync rounds.
//
// An extension push must respect the pause and the source switches, so it is
// skipped without writing. An import is something the user asked for by hand,
// so neither applies. A write error is returned and nothing is kept to retry:
// the caller still has the conversation and tries again.
func (e *Engine) Ingest(s *session.Session, explicit bool) (string, error) {
	e.run.Lock()
	defer e.run.Unlock()

	e.mu.Lock()
	cfg, red := e.cfg, e.red
	e.mu.Unlock()
	if !explicit && (cfg.Paused || !cfg.SourceEnabled(s.Source)) {
		return IngestSkipped, nil
	}
	if summarize.InWorkDir(s.Cwd, summarize.WorkDir(e.path)) {
		return IngestSkipped, nil
	}
	v, err := e.vault(cfg)
	if err != nil {
		return "", err
	}

	// Work on a copy: the caller keeps its own, unredacted, session.
	c := *s
	c.Messages = slices.Clone(s.Messages)
	scrub(&c, red)
	res, err := v.Write(&c)
	if err != nil {
		return "", fmt.Errorf("%s %s: %w", s.Source, s.ID, err)
	}
	e.mu.Lock()
	e.seen[s.Source+"\x00"+s.ID] = seenSession{s.Source, session.Project(s.Cwd)}
	e.mu.Unlock()
	e.refreshCounts()
	switch res {
	case vault.Created:
		return IngestCreated, nil
	case vault.Updated:
		return IngestUpdated, nil
	}
	return IngestUnchanged, nil // including a note whose markers the user removed: it is theirs now
}
