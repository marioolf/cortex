// Package session defines the normalized conversation every collector produces.
package session

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/marioolf/cortex/internal/wsl"
)

type Message struct {
	Role string // "user" or "assistant"
	Text string
	Time time.Time
}

type Session struct {
	Source   string // collector ID, e.g. "claude-code"
	ID       string
	Cwd      string
	Title    string
	URL      string // where the conversation lives on the web, if known; collectors leave it empty
	Messages []Message
}

// Started is the time of the first message that has one.
func (s *Session) Started() time.Time {
	for _, m := range s.Messages {
		if !m.Time.IsZero() {
			return m.Time
		}
	}
	return time.Time{}
}

// A home directory is where people start chats about anything; it is not a project.
// That includes a Windows home seen from WSL (/mnt/c/Users/name).
var homeDir = regexp.MustCompile(`^(/home/[^/]+|/Users/[^/]+|[A-Za-z]:/Users/[^/]+|/mnt/[A-Za-z]/Users/[^/]+|/root|)$`)

// Tools that start in the Windows directory (a shell opened as administrator,
// for instance) are not working on a project either.
var winSystemDir = regexp.MustCompile(`(?i)^([a-z]:|/mnt/[a-z])/windows(/|$)`)

// Where Project looks for a .git directory. Tests replace them.
var (
	onWindows = runtime.GOOS == "windows"
	wslRoots  = wsl.Roots
)

// Project returns the project name for a working directory: the name of the
// enclosing git repository if there is one, else the directory's own name.
// It returns "" when cwd is empty, a home directory, a drive or filesystem
// root, or inside a Windows system directory. Both / and \ paths are
// accepted regardless of the OS we run on, since sessions may come from WSL.
func Project(cwd string) string {
	if strings.TrimSpace(cwd) == "" {
		return ""
	}
	// Clean first so that "/tmp/.." or "/a/b/." never yield a project named ".." or ".".
	p := strings.TrimRight(path.Clean(strings.ReplaceAll(cwd, `\`, "/")), "/")
	last := p[strings.LastIndex(p, "/")+1:]
	if homeDir.MatchString(p) || winSystemDir.MatchString(p) || driveRoot.MatchString(p) || last == "." || last == ".." {
		return ""
	}
	for dir := p; !homeDir.MatchString(dir) && strings.Contains(dir, "/"); dir = dir[:strings.LastIndex(dir, "/")] {
		if hasGit(dir) {
			return dir[strings.LastIndex(dir, "/")+1:]
		}
	}
	return last
}

var driveRoot = regexp.MustCompile(`^[A-Za-z]:$`)

// hasGit reports whether dir (slash-separated) holds a .git entry. On Windows a
// POSIX path comes from a WSL session; it does not exist on the host, so it is
// looked up under the root of each running distro instead.
func hasGit(dir string) bool {
	if onWindows && strings.HasPrefix(dir, "/") && !strings.HasPrefix(dir, "//") {
		for _, root := range wslRoots() {
			if exists(filepath.Join(root, filepath.FromSlash(dir), ".git")) {
				return true
			}
		}
		return false
	}
	return exists(filepath.Join(filepath.FromSlash(dir), ".git"))
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// maxToolLine is the longest a tool-call line may be, in characters.
const maxToolLine = 120

// ShortenToolLines cuts every tool-call line ("> `Name` ...") of text to
// maxToolLine characters plus an ellipsis. It must run after redaction: cutting
// first could leave a prefix of a secret that no redaction rule recognises.
func ShortenToolLines(text string) string {
	if !strings.Contains(text, "> `") {
		return text
	}
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		if !strings.HasPrefix(l, "> `") {
			continue
		}
		if r := []rune(l); len(r) > maxToolLine {
			lines[i] = string(r[:maxToolLine]) + "…"
		}
	}
	return strings.Join(lines, "\n")
}

// MaxMessageChars is the most characters of one message (or title) Cortex
// saves. One conversation can hold a single message of hundreds of MB; saving
// it whole would make a note nobody can open and keep redaction busy for
// minutes.
const MaxMessageChars = 1_000_000

// CapText cuts text to max characters, on a character boundary, and ends it
// with a line saying how many were left out. Text that fits is returned as is.
// Run it before redaction so that redaction has a bounded amount of work; the
// cut can leave the first few characters of a secret at the very end, which no
// redaction rule will recognise, the same trade-off as ShortenToolLines.
func CapText(text string, max int) string {
	if len(text) <= max { // bytes >= characters: it surely fits
		return text
	}
	n := 0
	for i := range text {
		if n == max {
			return fmt.Sprintf("%s\n\n[… %d more characters not saved by Cortex]", text[:i], utf8.RuneCountInString(text[i:]))
		}
		n++
	}
	return text
}
