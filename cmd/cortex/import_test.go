package main

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marioolf/cortex/internal/config"
)

// Invented exports, one conversation each.
const gptExport = `[{"id":"cli-gpt-1","title":"Bike repair","current_node":"a1","mapping":{
 "root":{"id":"root","parent":"","children":["u1"],"message":null},
 "u1":{"id":"u1","parent":"root","children":["a1"],"message":{"author":{"role":"user"},"create_time":1700000010,"recipient":"all","content":{"content_type":"text","parts":["How tight should a headset be?"]},"metadata":{}}},
 "a1":{"id":"a1","parent":"u1","children":[],"message":{"author":{"role":"assistant"},"create_time":1700000020,"recipient":"all","content":{"content_type":"text","parts":["No play, but it must turn freely."]},"metadata":{}}}}}]`

const claudeExport = `[{"uuid":"cli-claude-1","name":"Tea notes","chat_messages":[
 {"sender":"human","text":"How long to steep oolong?","created_at":"2025-03-01T10:00:00Z"},
 {"sender":"assistant","text":"About three minutes.","created_at":"2025-03-01T10:00:05Z"}]}]`

func zipFile(t *testing.T, dir, name, data string) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("conversations.json")
	w.Write([]byte(data))
	zw.Close()
	return writeTemp(t, dir, name, buf.String())
}

func writeTemp(t *testing.T, dir, name, data string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// importEnv returns a temp config whose vault is a temp folder.
func importEnv(t *testing.T, paused bool) (dir, cfgPath, vault string) {
	t.Helper()
	dir = t.TempDir()
	vault = filepath.Join(dir, "vault")
	cfgPath = filepath.Join(dir, "config.json")
	cfg := &config.Config{Vault: vault, Paused: paused, DisabledSources: []string{"chatgpt", "claude-ai"}}
	if err := cfg.Save(cfgPath); err != nil {
		t.Fatal(err)
	}
	return
}

func runImport(args ...string) (out, errOut string, err error) {
	var o, e bytes.Buffer
	err = importExport(args, &o, &e)
	return o.String(), e.String(), err
}

func countNotes(t *testing.T, vault string) int {
	t.Helper()
	n := 0
	filepath.WalkDir(vault, func(p string, d os.DirEntry, _ error) error {
		if d != nil && !d.IsDir() && strings.HasSuffix(p, ".md") && d.Name() != "README.md" {
			n++
		}
		return nil
	})
	return n
}

func TestImportCommand(t *testing.T) {
	dir, cfg, vault := importEnv(t, true) // paused and both sources off: an explicit import ignores that
	files := map[string]string{
		"chatgpt zip":  zipFile(t, dir, "gpt.zip", gptExport),
		"chatgpt json": writeTemp(t, dir, "conversations.json", gptExport),
		"claude zip":   zipFile(t, dir, "claude.zip", claudeExport),
		"claude json":  writeTemp(t, dir, "claude.json", claudeExport),
	}
	// Two different conversations: zip and bare file of the same one are the same note.
	for _, name := range []string{"chatgpt zip", "claude json"} {
		out, _, err := runImport(files[name], "-config", cfg)
		if err != nil || out != "1 conversations: 1 created, 0 updated, 0 unchanged, 0 failed, 0 could not be read\n" {
			t.Errorf("%s: %q, %v", name, out, err)
		}
	}
	if n := countNotes(t, vault); n != 2 {
		t.Errorf("notes = %d", n)
	}
	for _, name := range []string{"chatgpt json", "claude zip"} {
		out, _, err := runImport("-config", cfg, files[name]) // flags first works too
		if err != nil || !strings.HasPrefix(out, "1 conversations: 0 created, 0 updated, 1 unchanged") {
			t.Errorf("re-import %s: %q, %v", name, out, err)
		}
	}
	if n := countNotes(t, vault); n != 2 {
		t.Errorf("notes after re-import = %d", n)
	}
}

func TestImportCommandVaultFlag(t *testing.T) {
	dir, cfg, vault := importEnv(t, false)
	other := filepath.Join(dir, "other")
	if _, _, err := runImport(writeTemp(t, dir, "c.json", gptExport), "-config", cfg, "-vault", other); err != nil {
		t.Fatal(err)
	}
	if countNotes(t, other) != 1 || countNotes(t, vault) != 0 {
		t.Error("-vault not honoured")
	}
}

func TestImportCommandErrors(t *testing.T) {
	dir, cfg, vault := importEnv(t, false)
	for name, args := range map[string][]string{
		"no file":       {"-config", cfg},
		"two files":     {"a", "b", "-config", cfg},
		"missing file":  {filepath.Join(dir, "nope.zip"), "-config", cfg},
		"not an export": {writeTemp(t, dir, "x.txt", "hello"), "-config", cfg},
		"other json":    {writeTemp(t, dir, "y.json", `[{"a":1}]`), "-config", cfg},
	} {
		if out, _, err := runImport(args...); err == nil || out != "" {
			t.Errorf("%s: out %q, err %v", name, out, err)
		}
	}
	if _, _, err := runImport(writeTemp(t, dir, "z.txt", "hello"), "-config", cfg); err == nil || !strings.Contains(err.Error(), "unsupported file") {
		t.Errorf("unsupported message: %v", err)
	}
	if countNotes(t, vault) != 0 {
		t.Error("notes from refused files")
	}

	// A conversation that cannot be written is counted as failed and makes the command fail.
	os.MkdirAll(vault, 0o755)
	os.WriteFile(filepath.Join(vault, "Chats"), []byte("a file where a folder should be"), 0o644)
	out, errOut, err := runImport(writeTemp(t, dir, "g.json", gptExport), "-config", cfg)
	if err == nil || !strings.HasPrefix(out, "1 conversations: 0 created, 0 updated, 0 unchanged, 1 failed") || !strings.Contains(errOut, "failed:") {
		t.Errorf("failed write: %q %q %v", out, errOut, err)
	}
}

func TestImportCommandUnreadableConversation(t *testing.T) {
	dir, cfg, _ := importEnv(t, false)
	mixed := strings.TrimSuffix(gptExport, "]") + `,{"id":"bad","mapping":{"x":"not a node"}}]`
	out, _, err := runImport(writeTemp(t, dir, "m.json", mixed), "-config", cfg)
	if err != nil || out != "2 conversations: 1 created, 0 updated, 0 unchanged, 0 failed, 1 could not be read\n" {
		t.Errorf("%q, %v", out, err)
	}
}
