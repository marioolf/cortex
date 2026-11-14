package collector

import (
	"os"
	"time"
)

type stamp struct {
	mod  time.Time
	size int64
}

// Seen remembers which files a collector has already read, so a long-running
// process does not re-parse every transcript on every round. The zero value
// is ready to use.
type Seen struct {
	m       map[string]stamp
	pending map[string]stamp // what Changed observed, for Mark to commit
}

// Changed reports whether path is new or has a different modification time or
// size than when it was last marked. It remembers the state it saw, but does
// not record it as read: call Mark once the file has been read successfully,
// so that failures are retried.
func (s *Seen) Changed(path string) (bool, error) {
	st, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	cur := stamp{st.ModTime(), st.Size()}
	if s.pending == nil {
		s.pending = map[string]stamp{}
	}
	s.pending[path] = cur
	prev, ok := s.m[path]
	return !ok || prev != cur, nil
}

// Mark records path as read in the state the last Changed call observed, not
// the state now: bytes appended while the file was being read were not parsed,
// so they must still count as a change next round. Without a prior Changed
// there is nothing to record.
func (s *Seen) Mark(path string) {
	cur, ok := s.pending[path]
	if !ok {
		return
	}
	if s.m == nil {
		s.m = map[string]stamp{}
	}
	s.m[path] = cur
	delete(s.pending, path)
}

// Reset forgets everything.
func (s *Seen) Reset() { s.m, s.pending = nil, nil }
