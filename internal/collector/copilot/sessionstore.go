package copilot

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/marioolf/cortex/internal/session"
	"github.com/marioolf/cortex/internal/sqliteutil"
)

// Newer Copilot Chat builds (extension src/platform/chronicle/node/sessionStore.ts,
// schema version 3) also mirror every session into a SQLite database at
// <User dir>/globalStorage/github.copilot-chat/session-store.db with a
// sessions row (cwd, summary) and one turns row per exchange
// (user_message, assistant_response). Tool calls are not stored there.

// collectStore reads session-store.db under root. Sessions whose id was ever seen
// in a chatSessions file (parsed or skipped as unchanged) are skipped so nothing is counted twice.
func (c *Collector) collectStore(root string, fn func(*session.Session), warn func(error)) {
	p := filepath.Join(root, "globalStorage", "github.copilot-chat", "session-store.db")
	if _, err := os.Stat(p); err != nil {
		return
	}
	changed, err := sqliteutil.Changed(&c.seen, p)
	if err != nil {
		warn(fmt.Errorf("%s: %w", p, err))
		return
	}
	if !changed {
		return
	}
	sessions, err := readStore(p)
	if err != nil {
		warn(fmt.Errorf("%s: %w", p, err))
		return
	}
	sqliteutil.MarkSeen(&c.seen, p)
	done := map[string]bool{}
	for _, id := range c.fileIDs {
		done[id] = true
	}
	for _, s := range sessions {
		if !done[s.ID] {
			fn(s)
		}
	}
}

func readStore(path string) ([]*session.Session, error) {
	db, closeDB, err := sqliteutil.Open(path)
	if err != nil {
		return nil, err
	}
	defer closeDB()
	rows, err := db.Query(`SELECT id, COALESCE(cwd,''), COALESCE(summary,'') FROM sessions`)
	if err != nil {
		return nil, err
	}
	var out []*session.Session
	for rows.Next() {
		s := &session.Session{Source: ID}
		if err := rows.Scan(&s.ID, &s.Cwd, &s.Title); err != nil {
			rows.Close()
			return nil, err
		}
		s.Title = strings.TrimSpace(s.Title)
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	kept := out[:0]
	for _, s := range out {
		if err := loadTurns(db, s); err != nil {
			return nil, err
		}
		if len(s.Messages) == 0 {
			continue
		}
		if s.Title == "" {
			s.Title, _, _ = strings.Cut(s.Messages[0].Text, "\n")
		}
		kept = append(kept, s)
	}
	return kept, nil
}

func loadTurns(db *sql.DB, s *session.Session) error {
	rows, err := db.Query(`SELECT COALESCE(user_message,''), COALESCE(assistant_response,''), COALESCE(timestamp,'')
		FROM turns WHERE session_id = ? ORDER BY turn_index`, s.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var u, a, ts string
		if err := rows.Scan(&u, &a, &ts); err != nil {
			return err
		}
		t, _ := time.Parse(time.RFC3339, ts)
		if u = strings.TrimSpace(u); u != "" {
			s.Messages = append(s.Messages, session.Message{Role: "user", Text: u, Time: t})
		}
		if a = strings.TrimSpace(a); a != "" {
			s.Messages = append(s.Messages, session.Message{Role: "assistant", Text: a, Time: t})
		}
	}
	return rows.Err()
}
