// Package session defines the normalized conversation every collector produces.
package session

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
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
var homeDir = regexp.MustCompile(`^(/home/[^/]+|/Users/[^/]+|[A-Za-z]:/Users/[^/]+|/root|)$`)

// Project returns the project name for a working directory: the name of the
// enclosing git repository if there is one, else the directory's own name.
// It returns "" when cwd is empty or a home directory. Both / and \ paths are
// accepted regardless of the OS we run on, since sessions may come from WSL.
func Project(cwd string) string {
	p := strings.TrimRight(strings.ReplaceAll(cwd, `\`, "/"), "/")
	if homeDir.MatchString(p) {
		return ""
	}
	name := p[strings.LastIndex(p, "/")+1:]
	for dir := p; !homeDir.MatchString(dir) && strings.Contains(dir, "/"); dir = dir[:strings.LastIndex(dir, "/")] {
		if _, err := os.Stat(filepath.Join(filepath.FromSlash(dir), ".git")); err == nil {
			return dir[strings.LastIndex(dir, "/")+1:]
		}
	}
	return name
}
