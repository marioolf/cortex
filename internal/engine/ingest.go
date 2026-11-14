package engine

import (
	"fmt"

	"github.com/marioolf/cortex/internal/session"
	"github.com/marioolf/cortex/internal/vault"
)

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
	v, err := e.vault(cfg)
	if err != nil {
		return "", err
	}

	// Work on a copy: the caller keeps its own, unredacted, session.
	c := *s
	c.Title = red.Apply(s.Title)
	c.Messages = make([]session.Message, len(s.Messages))
	for i, m := range s.Messages {
		m.Text = session.ShortenToolLines(red.Apply(m.Text)) // after redacting: see ShortenToolLines
		c.Messages[i] = m
	}
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
