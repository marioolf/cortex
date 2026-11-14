package session

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

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

func TestCapText(t *testing.T) {
	if got := CapText("short", 10); got != "short" {
		t.Fatal(got)
	}
	if got := CapText("12345", 5); got != "12345" {
		t.Fatal(got)
	}
	got := CapText("héllo wörld", 4)
	if got != "héll\n\n[… 7 more characters not saved by Cortex]" {
		t.Fatalf("%q", got)
	}
	// Multi-byte characters are never split, and the count is in characters.
	got = CapText(strings.Repeat("日本", 10), 3)
	if !strings.HasPrefix(got, "日本日\n\n[… 17 more") || !utf8.ValidString(got) {
		t.Fatalf("%q", got)
	}
	// A huge text yields a small one.
	big := strings.Repeat("a", 50<<20)
	if out := CapText(big, 1000); len(out) > 1100 || !strings.HasSuffix(out, "[… 52427800 more characters not saved by Cortex]") {
		t.Fatalf("%d bytes, %q", len(out), out[len(out)-60:])
	}
}

func TestMergerJoinsConsecutiveSameRole(t *testing.T) {
	t0 := time.Unix(100, 0)
	var g Merger
	g.Add("user", "a", t0)
	g.Add("assistant", "b", t0.Add(time.Second))
	g.Add("assistant", "c", t0.Add(2*time.Second))
	g.Add("assistant", "d", t0.Add(3*time.Second))
	_ = g.Messages() // reading in the middle must not break later additions
	g.Add("assistant", "e", t0)
	g.Add("user", "f", t0)
	got := g.Messages()
	if len(got) != 3 || got[0].Text != "a" || got[1].Text != "b\n\nc\n\nd\n\ne" || got[2].Text != "f" {
		t.Fatalf("%+v", got)
	}
	if !got[1].Time.Equal(t0.Add(time.Second)) {
		t.Errorf("time of a merged message is the first piece's: %v", got[1].Time)
	}
	if n := len((&Merger{}).Messages()); n != 0 {
		t.Errorf("empty: %d", n)
	}
}

func TestMergerIsLinear(t *testing.T) {
	var g Merger
	start := time.Now()
	for i := 0; i < 300_000; i++ {
		g.Add("assistant", "piece", time.Time{})
	}
	got := g.Messages()
	if len(got) != 1 || strings.Count(got[0].Text, "piece") != 300_000 {
		t.Fatal("not merged")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("took %v", d)
	}
}
