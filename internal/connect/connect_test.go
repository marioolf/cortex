package connect

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

var entry = Entry{Command: `C:\Program Files\Cortex\cortex.exe`, Args: []string{"mcp"}}

func TestAddKeepsEverythingElse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude_desktop_config.json")
	// Odd spacing, key order, escapes and a number that must not be re-serialised.
	orig := "{\r\n \"theme\":   \"dark\",\n \"big\": 12345678901234567890.0e0,\n \"mcpServers\": {\n   \"other\": {\"command\":\"x\",   \"args\": [ \"a\\u0026b\" ]}\n },\n \"last\": [1,  2]\n}\n"
	if err := os.WriteFile(path, []byte(orig), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := Add(path, entry); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	for _, keep := range []string{`"theme"`, `"dark"`, `12345678901234567890.0e0`, `{"command":"x",   "args": [ "a\u0026b" ]}`, `[1,  2]`} {
		if !strings.Contains(string(got), keep) {
			t.Errorf("lost %q in\n%s", keep, got)
		}
	}
	if strings.Index(string(got), `"theme"`) > strings.Index(string(got), `"last"`) {
		t.Error("key order changed")
	}
	var v struct {
		MCPServers map[string]Entry `json:"mcpServers"`
	}
	if err := json.Unmarshal(got, &v); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, got)
	}
	if v.MCPServers["cortex"].Command != entry.Command || v.MCPServers["other"].Command != "x" {
		t.Errorf("servers = %+v", v.MCPServers)
	}
	if bak, _ := os.ReadFile(path + ".bak"); string(bak) != orig {
		t.Errorf("backup = %q", bak)
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".cortex-*")); len(left) != 0 {
		t.Errorf("temp files left: %v", left)
	}
	if s, _ := Status(path, entry); s != "connected" {
		t.Errorf("status = %q", s)
	}
	if s, _ := Status(path, Entry{Command: "/else", Args: []string{"mcp"}}); s != "outdated" {
		t.Errorf("status = %q", s)
	}

	// Again, with another path: replaces only our entry.
	moved := Entry{Command: "/new/cortex", Args: []string{"mcp"}}
	if err := Add(path, moved); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(path)
	if strings.Count(string(got), `"cortex"`) != 1 || !strings.Contains(string(got), "/new/cortex") || !strings.Contains(string(got), `[1,  2]`) {
		t.Errorf("second add:\n%s", got)
	}
}

func TestAddCreatesAndStatusOfMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	if s, err := Status(path, entry); s != "" || err != nil {
		t.Errorf("missing file: %q %v", s, err)
	}
	if err := Add(path, entry); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".bak"); err == nil {
		t.Error("a backup of nothing was made")
	}
	if s, _ := Status(path, entry); s != "connected" {
		t.Errorf("status = %q", s)
	}
	// An empty file counts as an empty object.
	os.WriteFile(path, []byte(" \n"), 0o600)
	if err := Add(path, entry); err != nil {
		t.Errorf("empty file: %v", err)
	}
}

func TestAddRefusesWhatItCannotPreserve(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"array": `[1,2]`, "null": `null`, "text": `not json`, "trunc": `{"a":`, "trailing": `{} {}`,
		"servers": `{"mcpServers": []}`, "servers-str": `{"mcpServers": "x"}`,
	} {
		path := filepath.Join(dir, name+".json")
		os.WriteFile(path, []byte(content), 0o600)
		err := Add(path, entry)
		if err == nil || !strings.Contains(err.Error(), "by hand") {
			t.Errorf("%s: err = %v", name, err)
		}
		if got, _ := os.ReadFile(path); string(got) != content {
			t.Errorf("%s: file was changed to %q", name, got)
		}
		if _, err := os.Stat(path + ".bak"); err == nil {
			t.Errorf("%s: backup written for a refused change", name)
		}
	}
	if _, err := Status(filepath.Join(dir, "array.json"), entry); !errors.Is(err, ErrNotObject) {
		t.Errorf("status err = %v", err)
	}
}

func TestAddBOMAndSymlink(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real.json")
	os.WriteFile(real, []byte("\xef\xbb\xbf{\"a\": 1}"), 0o600)
	link := filepath.Join(dir, "link.json")
	if err := os.Symlink(real, link); err != nil {
		t.Log("symlinks unavailable:", err)
		link = real
	}
	if err := Add(link, entry); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Lstat(link); link != real && st.Mode()&os.ModeSymlink == 0 {
		t.Error("the symlink was replaced by a file")
	}
	got, _ := os.ReadFile(real)
	if !strings.Contains(string(got), `"a": 1`) || !strings.Contains(string(got), "cortex") {
		t.Errorf("got %s", got)
	}
}

func TestAvailable(t *testing.T) {
	dir := t.TempDir()
	if !Available(filepath.Join(dir, "x.json")) || Available(filepath.Join(dir, "nope", "x.json")) || Available("") {
		t.Error("Available is wrong")
	}
	if _, err := os.Stat(filepath.Join(dir, "nope")); err == nil {
		t.Error("created a folder")
	}
}

func TestClaudeCodeCommandAndWSLPath(t *testing.T) {
	const pre = `claude mcp add --scope user cortex -- `
	for _, c := range []struct{ exe, goos, want string }{
		{`C:\Users\me\My Apps\cortex.exe`, "windows", pre + `'C:\Users\me\My Apps\cortex.exe' mcp`},
		// PowerShell expands $ and backticks in double quotes; in single quotes only ' needs doubling.
		{"C:\\Users\\o'brien\\$x`y\\\"z\\cortex.exe", "windows", pre + "'C:\\Users\\o''brien\\$x`y\\\"z\\cortex.exe' mcp"},
		{"C:\\a\u2019b\\c.exe", "windows", pre + "'C:\\a\u2019\u2019b\\c.exe' mcp"},
		{`/opt/my "apps"/$x/cortex`, "linux", pre + `'/opt/my "apps"/$x/cortex' mcp`},
		{"/opt/it's!/`id`/cortex", "darwin", pre + `'/opt/it'\''s!/` + "`id`" + `/cortex' mcp`},
	} {
		if got := ClaudeCodeCommand(c.exe, c.goos); got != c.want {
			t.Errorf("%s %q:\n got %s\nwant %s", c.goos, c.exe, got, c.want)
		}
	}
	for in, want := range map[string]string{
		`C:\Users\me\cortex.exe`: "/mnt/c/Users/me/cortex.exe",
		`d:\a b\c.exe`:           "/mnt/d/a b/c.exe",
		`D:/x/y`:                 "/mnt/d/x/y",
		`\\server\share\c.exe`:   "",
		`relative\c.exe`:         "",
		`C:`:                     "",
	} {
		got, ok := WSLPath(in)
		if got != want || ok != (want != "") {
			t.Errorf("WSLPath(%q) = %q, %v", in, got, ok)
		}
	}
}

func TestTrailingGarbageIsRefused(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{"word": `{"a":1} oops`, "brace": `{"a":1}}`, "comment": "{\"a\":1}\n// note"} {
		path := filepath.Join(dir, name+".json")
		os.WriteFile(path, []byte(content), 0o600)
		if err := Add(path, entry); err == nil || !strings.Contains(err.Error(), "by hand") {
			t.Errorf("%s: err = %v", name, err)
		}
		if got, _ := os.ReadFile(path); string(got) != content {
			t.Errorf("%s: changed to %q", name, got)
		}
	}
	ok := filepath.Join(dir, "ok.json")
	os.WriteFile(ok, []byte("{\"a\":1} \r\n\t\n"), 0o600)
	if err := Add(ok, entry); err != nil {
		t.Errorf("trailing whitespace: %v", err)
	}
}

func TestBackupKeepsTheOriginal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	orig := "{\"mine\": true}\n"
	os.WriteFile(path, []byte(orig), 0o600)
	if err := Add(path, entry); err != nil {
		t.Fatal(err)
	}
	// The executable moved: the entry is replaced, the backup is not.
	if err := Add(path, Entry{Command: "/new/cortex", Args: []string{"mcp"}}); err != nil {
		t.Fatal(err)
	}
	if bak, _ := os.ReadFile(path + ".bak"); string(bak) != orig {
		t.Errorf("backup = %q, want the user's original", bak)
	}
	// Nor is one that the user keeps there created by a replacement.
	os.Remove(path + ".bak")
	if err := Add(path, entry); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".bak"); err == nil {
		t.Error("a replacement wrote a backup of Cortex's own entry")
	}
}

func TestReadOnlyFileIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	os.WriteFile(path, []byte(`{"a":1}`), 0o444)
	err := Add(path, entry)
	if !errors.Is(err, ErrReadOnly) || !strings.Contains(err.Error(), "by hand") {
		t.Fatalf("err = %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != `{"a":1}` {
		t.Errorf("file changed: %s", got)
	}
	if _, err := os.Stat(path + ".bak"); err == nil {
		t.Error("backup for a refused change")
	}
}

func TestAddKeepsBOMAndPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	os.WriteFile(path, []byte("\xef\xbb\xbf{\"a\": 1}"), 0o640)
	os.Chmod(path, 0o640)
	if err := Add(path, entry); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(got), "\xef\xbb\xbf{") || strings.Count(string(got), "\xef\xbb\xbf") != 1 {
		t.Errorf("BOM not kept once: %q", got)
	}
	if st, _ := os.Stat(path); runtime.GOOS != "windows" && st.Mode().Perm() != 0o640 {
		t.Errorf("mode = %v", st.Mode().Perm())
	}
	if s, _ := Status(path, entry); s != "connected" {
		t.Errorf("status = %q", s)
	}
	// No BOM stays no BOM.
	p2 := filepath.Join(t.TempDir(), "x.json")
	os.WriteFile(p2, []byte(`{}`), 0o600)
	Add(p2, entry)
	if got, _ := os.ReadFile(p2); strings.HasPrefix(string(got), "\xef") {
		t.Error("a BOM was added")
	}
}
