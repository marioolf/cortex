package collector

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSeen(t *testing.T) {
	f := filepath.Join(t.TempDir(), "a.jsonl")
	os.WriteFile(f, []byte("one\n"), 0o600)
	var s Seen

	changed := func() bool {
		t.Helper()
		c, err := s.Changed(f)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	if !changed() {
		t.Fatal("a new file is changed")
	}
	if !changed() {
		t.Fatal("not marked yet: still changed")
	}
	s.Mark(f)
	if changed() {
		t.Fatal("marked and untouched: not changed")
	}

	// Bytes appended while the file is being read must not be marked as read.
	changed()                                    // observe the state the reader is about to parse
	os.WriteFile(f, []byte("one\ntwo\n"), 0o600) // the append lands during the read
	s.Mark(f)
	if !changed() {
		t.Error("an append during the read was recorded as seen")
	}
}

func TestMarkWithoutChangedDoesNothing(t *testing.T) {
	f := filepath.Join(t.TempDir(), "a")
	os.WriteFile(f, []byte("x"), 0o600)
	var s Seen
	s.Mark(f)
	if c, _ := s.Changed(f); !c {
		t.Error("Mark without Changed recorded something")
	}
}
