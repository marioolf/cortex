package session

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/marioolf/cortex/internal/wsl"
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
		"/":                        "",
		".":                        "",
		"..":                       "",
		"../..":                    "",
		"/tmp/..":                  "",
		"/tmp/.":                   "tmp",
		"/tmp/../..":               "",
		`C:\`:                      "",
		`C:\work\.`:                "work",
	}
	for cwd, want := range cases {
		if got := Project(cwd); got != want {
			t.Errorf("Project(%q) = %q, want %q", cwd, got, want)
		}
	}
}

func TestProjectOfWSLSessionOnWindows(t *testing.T) {
	// Two temp dirs stand in for the roots of two running distros.
	other, distro := t.TempDir(), t.TempDir()
	os.MkdirAll(filepath.Join(distro, "home", "x", "repo", ".git"), 0o755)

	onWindows, wslRoots = true, func() []string { return []string{other, distro} }
	defer func() { onWindows, wslRoots = runtime.GOOS == "windows", wsl.Roots }()

	cases := map[string]string{
		"/home/x/repo/internal/pkg": "repo",
		"/home/x/repo":              "repo",
		"/home/x/other/pkg":         "pkg",
		"/home/x":                   "",
		"/tmp/..":                   "",
	}
	for cwd, want := range cases {
		if got := Project(cwd); got != want {
			t.Errorf("Project(%q) = %q, want %q", cwd, got, want)
		}
	}

	// Not on Windows, a POSIX path is looked up on the host only.
	onWindows = false
	if got := Project("/home/x/repo/internal/pkg"); got != "pkg" {
		t.Errorf("without WSL lookup, got %q", got)
	}
}

func TestShortenToolLines(t *testing.T) {
	long := strings.Repeat("é", 200)
	cases := []struct{ name, in, want string }{
		{"short", "> `Bash` ls", "> `Bash` ls"},
		{"no tool line", long, long},
		{"long tool line", "hi\n\n> `Bash` " + long + "\n\nbye", "hi\n\n" + string([]rune("> `Bash` " + long)[:120]) + "…\n\nbye"},
		{"exactly 120", "> `x` " + strings.Repeat("a", 114), "> `x` " + strings.Repeat("a", 114)},
		{"not at line start", "see > `x` " + long, "see > `x` " + long},
	}
	for _, c := range cases {
		if got := ShortenToolLines(c.in); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}
