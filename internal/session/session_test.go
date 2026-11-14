package session

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProject(t *testing.T) {
	repo := t.TempDir()
	os.MkdirAll(filepath.Join(repo, ".git"), 0o755)
	sub := filepath.Join(repo, "pkg", "deep")
	os.MkdirAll(sub, 0o755)

	cases := map[string]string{
		"":                         "",
		"/home/someone":            "",
		"/Users/someone/":          "",
		`C:\Users\someone`:         "",
		`C:\Users\someone\dev\app`: "app",
		"/mnt/c/Users/someone":     "",
		"/mnt/d/Users/someone/":    "",
		"/mnt/c/Users/someone/app": "app",
		`C:\Windows`:               "",
		`c:\WINDOWS\System32`:      "",
		"/mnt/c/Windows/System32":  "",
		"/mnt/c/windows":           "",
		"/mnt/c/Windows10Stuff":    "Windows10Stuff",
		"/nonexistent/path/thing":  "thing",
		sub:                        filepath.Base(repo),
	}
	for cwd, want := range cases {
		if got := Project(cwd); got != want {
			t.Errorf("Project(%q) = %q, want %q", cwd, got, want)
		}
	}
}
