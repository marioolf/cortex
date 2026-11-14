// Package sqliteutil opens another application's SQLite database for reading
// only. The owning app may be running and writing to it, so nothing here may
// write to, lock exclusively, or checkpoint the original files.
package sqliteutil

import (
	"database/sql"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/marioolf/cortex/internal/collector"
	"modernc.org/sqlite" // pure Go: keeps CGO_ENABLED=0 builds working; also registers the "sqlite" driver
)

// Open returns a read-only handle on the database at path and a function that
// releases it. A missing file is an error.
//
// The URI uses mode=ro, and not immutable=1, whenever anything else may be
// using the database. immutable=1 disables locking, so a concurrent writer
// (a rollback-journal database being written, or a WAL database whose -wal was
// just truncated by a checkpoint) yields torn pages. Apps like OpenCode and
// Cursor run in WAL mode, where recent writes live in the -wal file until a
// checkpoint; mode=ro reads it through the shared -shm index and only takes the
// short-lived shared read locks that readers always take, which never block the
// writer. A read-only connection never checkpoints or deletes the WAL.
//
// The one case where mode=ro does harm is a cleanly closed WAL database: it
// creates -wal and -shm next to another tool's file. When the header says WAL
// and neither side file exists, nobody has the database open and there is
// nothing to miss, so immutable=1 is used there; it creates nothing.
//
// Where that fails (a \\wsl.localhost share cannot map -shm, a read-only
// directory without -shm) Open falls back to reading a private copy of the
// database and its -wal in a temporary directory.
func Open(path string) (*sql.DB, func(), error) {
	if _, err := os.Stat(path); err != nil {
		return nil, nil, err
	}
	if !strings.HasPrefix(path, `\\`) {
		db, err := openRO(path)
		if err == nil {
			return db, func() { db.Close() }, nil
		}
		// A file that is not a database will not become one when copied.
		var se *sqlite.Error
		if errors.As(err, &se) && (se.Code()&0xff == 26 || se.Code()&0xff == 11) {
			return nil, nil, err
		}
	}
	return openCopy(path)
}

func openRO(path string) (*sql.DB, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	p := filepath.ToSlash(abs)
	if runtime.GOOS == "windows" {
		p = "/" + p // file:///C:/dir/file
	}
	mode := "mode=ro"
	if cleanlyClosedWAL(abs) {
		mode = "immutable=1"
	}
	u := url.URL{Scheme: "file", Path: p, RawQuery: mode + "&_pragma=query_only(1)&_pragma=busy_timeout(3000)"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	// One connection: callers drain a result set before starting the next query.
	db.SetMaxOpenConns(1)
	// Opening is lazy; force the first read so failures surface here.
	var n int
	if err := db.QueryRow("SELECT count(*) FROM sqlite_master").Scan(&n); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// cleanlyClosedWAL reports whether path is a WAL-mode database with no -wal
// and no -shm beside it. An unreadable or unknown header counts as "no".
func cleanlyClosedWAL(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	var h [100]byte
	if _, err := io.ReadFull(f, h[:]); err != nil || string(h[:16]) != "SQLite format 3\x00" {
		return false
	}
	// Bytes 18 and 19 are the file format write and read versions: 2 is WAL, 1 rollback journal.
	if h[18] != 2 || h[19] != 2 {
		return false
	}
	for _, side := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(path + side); err == nil {
			return false
		}
	}
	return true
}

func openCopy(path string) (*sql.DB, func(), error) {
	dir, err := os.MkdirTemp("", "cortex-sqlite-")
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { os.RemoveAll(dir) }
	dst := filepath.Join(dir, "copy.db")
	if err := copyFile(path, dst); err != nil {
		cleanup()
		return nil, nil, err
	}
	// The WAL holds the newest commits; a missing one just means a checkpointed db.
	if err := copyFile(path+"-wal", dst+"-wal"); err != nil && !os.IsNotExist(err) {
		cleanup()
		return nil, nil, err
	}
	db, err := openRO(dst)
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	return db, func() { db.Close(); cleanup() }, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// OpenCopy is Open's fallback, exposed so tests can exercise it.
func OpenCopy(path string) (*sql.DB, func(), error) { return openCopy(path) }

// Tables returns the names of the tables in db.
func Tables(db *sql.DB) (map[string]bool, error) {
	rows, err := db.Query("SELECT name FROM sqlite_master WHERE type = 'table'")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	t := map[string]bool{}
	for rows.Next() {
		var n string
		if rows.Scan(&n) == nil {
			t[n] = true
		}
	}
	return t, rows.Err()
}

// Changed reports whether the database or its -wal file is new or different
// since MarkSeen. In WAL mode most writes touch only the -wal file, so the
// main file alone would miss them. A missing -wal is normal.
func Changed(seen *collector.Seen, db string) (bool, error) {
	changed, err := seen.Changed(db)
	if err != nil {
		return false, err
	}
	if w, err := seen.Changed(db + "-wal"); err == nil && w {
		changed = true
	}
	return changed, nil
}

// MarkSeen records the database and its -wal as read.
func MarkSeen(seen *collector.Seen, db string) {
	seen.Mark(db)
	seen.Mark(db + "-wal")
}
