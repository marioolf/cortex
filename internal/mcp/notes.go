package mcp

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// note is the frontmatter of one Cortex note, without its body.
type note struct {
	Path    string // relative to the vault, with forward slashes
	Title   string
	Project string
	Source  string
	Started string // RFC3339 as written in the note, or ""
	Mod     time.Time
}

// fileInfo is what a directory walk learns about a candidate note.
type fileInfo struct {
	abs, rel string
	mod      time.Time
	size     int64
}

type cacheKey struct {
	mod  int64
	size int64
}

type cacheEntry struct {
	key  cacheKey
	n    note
	ours bool // false: an .md file without a Cortex frontmatter
}

// index lists the vault's notes and caches parsed frontmatter by path, mtime
// and size, so a repeated search only re-reads files that changed. Nothing
// else is kept in memory.
type index struct {
	root string
	mu   sync.Mutex
	seen map[string]cacheEntry
}

func newIndex(root string) *index { return &index{root: root, seen: map[string]cacheEntry{}} }

// errNoVault tells the model, not the user, what is going on.
var errNoVault = errors.New("The vault folder does not exist yet: Cortex has not saved any conversation. " +
	"Ask the user to run Cortex (or `cortex sync`) first.")

// skipDirs hold things that are not conversations: Obsidian's settings and
// trash (a deleted note), version control data.
var skipDirs = map[string]bool{".obsidian": true, ".trash": true, ".git": true}

// walk lists the regular .md files of the vault. Symlinks are not followed,
// so a link inside the vault cannot lead a search outside it.
func (x *index) walk() ([]fileInfo, error) {
	st, err := os.Stat(x.root)
	if err != nil || !st.IsDir() {
		return nil, errNoVault
	}
	var files []fileInfo
	filepath.WalkDir(x.root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entries are skipped
		}
		if d.IsDir() {
			if p != x.root && skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || !strings.EqualFold(filepath.Ext(d.Name()), ".md") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(x.root, p)
		if err != nil {
			return nil
		}
		files = append(files, fileInfo{p, filepath.ToSlash(rel), info.ModTime(), info.Size()})
		return nil
	})
	return files, nil
}

// meta returns the frontmatter of f; ok is false for files that are not Cortex notes.
func (x *index) meta(f fileInfo) (n note, ok bool) {
	key := cacheKey{f.mod.UnixNano(), f.size}
	x.mu.Lock()
	e, hit := x.seen[f.rel]
	x.mu.Unlock()
	if !hit || e.key != key {
		e = cacheEntry{key: key}
		e.n, e.ours = parseHead(f)
		x.mu.Lock()
		x.seen[f.rel] = e
		x.mu.Unlock()
	}
	return e.n, e.ours
}

// forget drops cache entries of files that are gone.
func (x *index) forget(files []fileInfo) {
	keep := make(map[string]bool, len(files))
	for _, f := range files {
		keep[f.rel] = true
	}
	x.mu.Lock()
	for p := range x.seen {
		if !keep[p] {
			delete(x.seen, p)
		}
	}
	x.mu.Unlock()
}

const headSize = 8 << 10

func parseHead(f fileInfo) (note, bool) {
	fh, err := os.Open(f.abs)
	if err != nil {
		return note{}, false
	}
	defer fh.Close()
	buf := make([]byte, headSize)
	k, _ := io.ReadFull(fh, buf)
	n, _, ok := parseNote(string(buf[:k]))
	if !ok {
		return note{}, false
	}
	n.Path, n.Mod = f.rel, f.mod
	if n.Title == "" {
		n.Title = strings.TrimSuffix(filepath.Base(f.abs), filepath.Ext(f.abs))
	}
	return n, true
}

// parseNote reads the frontmatter and the first heading of text. body is what
// follows the frontmatter. ok needs a `session:` line: that is what marks a
// note as written by Cortex.
func parseNote(text string) (n note, body string, ok bool) {
	rest, found := strings.CutPrefix(text, "---")
	if !found {
		return n, text, false
	}
	rest = strings.TrimLeft(rest, " \t")
	if !strings.HasPrefix(rest, "\n") && !strings.HasPrefix(rest, "\r\n") {
		return n, text, false
	}
	body = text
	hasSession := false
	rest = strings.TrimLeft(rest, "\r\n")
	for rest != "" {
		var line string
		line, rest, _ = strings.Cut(rest, "\n")
		line = strings.TrimRight(line, "\r")
		if strings.TrimRight(line, " \t") == "---" {
			body = rest
			break
		}
		k, v, _ := strings.Cut(line, ":")
		v = unquote(strings.TrimSpace(v))
		switch strings.TrimSpace(k) {
		case "session":
			hasSession = true
		case "project":
			n.Project = v
		case "source":
			n.Source = v
		case "started":
			n.Started = v
		}
	}
	if !hasSession {
		return n, text, false
	}
	// The title is the note's first level-1 heading, as the vault writes it.
	for _, l := range strings.SplitN(body, "\n", 40) {
		if t, ok := strings.CutPrefix(l, "# "); ok {
			n.Title = oneLine(t, 200)
			break
		}
	}
	return n, body, true
}

func unquote(v string) string {
	if u, err := strconv.Unquote(v); err == nil {
		return u
	}
	return v
}

// oneLine collapses whitespace and cuts s to at most max characters.
func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > max {
		return string(r[:max]) + "…"
	}
	return s
}
