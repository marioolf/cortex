package sqliteutil

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/marioolf/cortex/internal/collector"
)

func liveWAL(t *testing.T) string {
	path := filepath.Join(t.TempDir(), "app.db")
	w, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(wal)&_pragma=wal_autocheckpoint(0)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	w.SetMaxOpenConns(1)
	for _, q := range []string{`CREATE TABLE t (a)`, `INSERT INTO t VALUES ('in the wal')`} {
		if _, err := w.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func read(t *testing.T, db *sql.DB) string {
	var a string
	if err := db.QueryRow(`SELECT a FROM t`).Scan(&a); err != nil {
		t.Fatal(err)
	}
	return a
}

func TestOpenSeesWALAndIsReadOnly(t *testing.T) {
	path := liveWAL(t)
	db, closeDB, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer closeDB()
	if got := read(t, db); got != "in the wal" {
		t.Errorf("got %q", got)
	}
	if _, err := db.Exec(`INSERT INTO t VALUES ('x')`); err == nil {
		t.Error("write succeeded on a read-only handle")
	}
}

func TestOpenCopyFallback(t *testing.T) {
	path := liveWAL(t)
	before, _ := os.ReadFile(path + "-wal")
	db, closeDB, err := OpenCopy(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := read(t, db); got != "in the wal" {
		t.Errorf("got %q", got)
	}
	closeDB()
	after, _ := os.ReadFile(path + "-wal")
	if string(before) != string(after) {
		t.Error("original -wal changed")
	}
}

func TestOpenMissingAndNotADatabase(t *testing.T) {
	if _, _, err := Open(filepath.Join(t.TempDir(), "nope.db")); err == nil {
		t.Error("expected error for missing file")
	}
	p := filepath.Join(t.TempDir(), "x.db")
	os.WriteFile(p, []byte("this is plain text, definitely not a database, padding padding padding padding"), 0o644)
	if _, _, err := Open(p); err == nil {
		t.Error("expected error for non-database")
	}
}

func TestChangedWatchesWAL(t *testing.T) {
	path := liveWAL(t)
	var seen collector.Seen
	if ch, err := Changed(&seen, path); err != nil || !ch {
		t.Fatalf("first: %v %v", ch, err)
	}
	MarkSeen(&seen, path)
	if ch, _ := Changed(&seen, path); ch {
		t.Error("unchanged reported as changed")
	}
	w, _ := sql.Open("sqlite", "file:"+path+"?_pragma=wal_autocheckpoint(0)")
	defer w.Close()
	w.Exec(`INSERT INTO t VALUES ('more')`)
	if ch, _ := Changed(&seen, path); !ch {
		t.Error("write to the -wal not noticed")
	}
}

// cleanWAL returns a WAL-mode database that was closed cleanly: SQLite
// checkpoints and removes the -wal and -shm files on the last close.
func cleanWAL(t *testing.T) string {
	path := filepath.Join(t.TempDir(), "app.db")
	w, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(wal)")
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`CREATE TABLE t (a)`, `INSERT INTO t VALUES ('checkpointed')`} {
		if _, err := w.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	w.Close()
	return path
}

func listing(t *testing.T, dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestOpenCleanWALCreatesNoFiles(t *testing.T) {
	path := cleanWAL(t)
	dir := filepath.Dir(path)
	files, bytes := listing(t, dir), mustRead(t, path)
	if len(files) != 1 {
		t.Fatalf("fixture is not clean: %v", files)
	}

	db, closeDB, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := read(t, db); got != "checkpointed" {
		t.Errorf("got %q", got)
	}
	closeDB()

	if after := listing(t, dir); !reflect.DeepEqual(after, files) {
		t.Errorf("directory changed: %v -> %v", files, after)
	}
	if string(mustRead(t, path)) != string(bytes) {
		t.Error("database file changed")
	}
}

// A writer that keeps the database open, and has truncated the -wal to zero
// bytes: an empty -wal is no sign that nobody is using it.
func TestOpenEmptyWALHeldOpenUsesROAndSeesData(t *testing.T) {
	path := liveWAL(t)
	w, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	w.SetMaxOpenConns(1)
	if _, err := w.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(path + "-wal"); err != nil || st.Size() != 0 {
		t.Fatalf("fixture: -wal should exist and be empty: %v", err)
	}
	if cleanlyClosedWAL(path) {
		t.Fatal("a database held open was taken for a cleanly closed one")
	}
	db, closeDB, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer closeDB()
	if got := read(t, db); got != "in the wal" {
		t.Errorf("got %q", got)
	}
}

func TestOpenRollbackJournalWithConcurrentWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	w, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(delete)&_pragma=busy_timeout(3000)")
	if err != nil {
		t.Fatal(err)
	}
	w.SetMaxOpenConns(1)
	// The initial value has the same shape as the updates: readers may run
	// before the writer goroutine has done its first one (slow to start on Windows).
	pad := strings.Repeat("x", 500)
	for _, q := range []string{`CREATE TABLE t (a)`, `INSERT INTO t VALUES ('v-` + pad + `')`} {
		if _, err := w.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if cleanlyClosedWAL(path) {
		t.Fatal("a rollback-journal database must not use immutable")
	}

	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			// Each value is self-describing, so a torn read cannot pass for a good one.
			w.Exec(`UPDATE t SET a = ?`, fmt.Sprintf("v%d-%s", i, pad))
		}
	}()

	end := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(end) {
		db, closeDB, err := Open(path)
		if err != nil {
			if !strings.Contains(err.Error(), "locked") && !strings.Contains(err.Error(), "busy") {
				t.Fatalf("open: %v", err)
			}
			continue
		}
		var a string
		if err := db.QueryRow(`SELECT a FROM t`).Scan(&a); err != nil {
			if !strings.Contains(err.Error(), "locked") && !strings.Contains(err.Error(), "busy") {
				t.Fatalf("read: %v", err)
			}
		} else if !strings.HasPrefix(a, "v") || !strings.HasSuffix(a, strings.Repeat("x", 500)) {
			t.Fatalf("torn value %q", a)
		}
		closeDB()
	}
	close(stop)
	<-done
	w.Close()
	if files := listing(t, filepath.Dir(path)); len(files) != 1 {
		t.Errorf("side files left behind: %v", files)
	}
}

func mustRead(t *testing.T, path string) []byte {
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
