package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/marioolf/cortex/internal/config"
)

// TestHelperSummariser is the fake summariser: this test binary run as the
// custom command. It does nothing unless CORTEX_FAKE_MODE is set.
func TestHelperSummariser(t *testing.T) {
	if os.Getenv("CORTEX_FAKE_MODE") == "" {
		t.Skip("helper")
	}
	if os.Getenv("CORTEX_FAKE_MODE") == "fail" {
		fmt.Fprintf(os.Stderr, "\n  boom with ghp_abcdefghijklmnopqrstuvwxyz0123 %s\nsecond line SECONDLINE\n", strings.Repeat("x", 500))
		os.Exit(1)
	}
	in, _ := io.ReadAll(os.Stdin)
	fmt.Printf("Summary of %d bytes.", len(in))
	os.Exit(0)
}

func runSummarize(args ...string) (out, errOut string, err error) {
	var o, e bytes.Buffer
	err = summarizeNote(args, &o, &e)
	return o.String(), e.String(), err
}

func TestSummarizeCommand(t *testing.T) {
	t.Setenv("CORTEX_FAKE_MODE", "1")
	dir := t.TempDir()
	vault := filepath.Join(dir, "vault")
	cfgPath := filepath.Join(dir, "config.json")
	// Summaries are off and no project is selected: the command does not care.
	cfg := &config.Config{Vault: vault, Summaries: config.Summaries{Tool: "custom", Command: []string{os.Args[0], "-test.run=TestHelperSummariser"}}}
	if err := cfg.Save(cfgPath); err != nil {
		t.Fatal(err)
	}
	// A note made by importing an invented export.
	if _, _, err := runImport(writeTemp(t, dir, "c.json", claudeExport), "-config", cfgPath); err != nil {
		t.Fatal(err)
	}
	var note string
	filepath.WalkDir(vault, func(p string, d os.DirEntry, _ error) error {
		if d != nil && !d.IsDir() && strings.Contains(d.Name(), "Tea notes") {
			note = p
		}
		return nil
	})
	if note == "" {
		t.Fatal("no note")
	}
	rel, _ := filepath.Rel(vault, note)

	for _, arg := range []string{rel, note} {
		out, _, err := runSummarize(arg, "-config", cfgPath)
		if err != nil || !strings.HasPrefix(out, "Summary of ") {
			t.Fatalf("%s: out=%q err=%v", arg, out, err)
		}
	}
	b, _ := os.ReadFile(note)
	if strings.Count(string(b), "cortex:summary:start") != 1 || !strings.Contains(string(b), "Summary of ") {
		t.Errorf("note:\n%s", b)
	}

	// Errors: no argument, outside the vault, no tool.
	if _, _, err := runSummarize("-config", cfgPath); err == nil {
		t.Error("no note given")
	}
	if _, _, err := runSummarize("../x.md", "-config", cfgPath); err == nil {
		t.Error("path outside the vault accepted")
	}
	cfg.Summaries = config.Summaries{}
	cfg.Save(cfgPath)
	if _, _, err := runSummarize(rel, "-config", cfgPath); err == nil || !strings.Contains(err.Error(), "no usable summary tool") {
		t.Errorf("without a tool: %v", err)
	}
}

// A failing tool's stderr reaches the terminal as the panel shows it: one
// line, 200 characters, redacted.
func TestSummarizeFailureShowsOneRedactedLine(t *testing.T) {
	t.Setenv("CORTEX_FAKE_MODE", "fail")
	dir := t.TempDir()
	vault := filepath.Join(dir, "vault")
	cfgPath := filepath.Join(dir, "config.json")
	cfg := &config.Config{Vault: vault, Summaries: config.Summaries{Tool: "custom", Command: []string{os.Args[0], "-test.run=TestHelperSummariser"}}}
	if err := cfg.Save(cfgPath); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runImport(writeTemp(t, dir, "c.json", claudeExport), "-config", cfgPath); err != nil {
		t.Fatal(err)
	}
	var note string
	filepath.WalkDir(vault, func(p string, d os.DirEntry, _ error) error {
		if d != nil && !d.IsDir() && strings.Contains(d.Name(), "Tea notes") {
			note = p
		}
		return nil
	})
	_, _, err := runSummarize(note, "-config", cfgPath)
	if err == nil {
		t.Fatal("no error")
	}
	msg := err.Error()
	if strings.Contains(msg, "ghp_abcdefghijklmnopqrstuvwxyz0123") || strings.Contains(msg, "SECONDLINE") || strings.Contains(msg, "\n") {
		t.Errorf("secret, second line or newline in %q", msg)
	}
	if !strings.Contains(msg, "boom with") || strings.Count(msg, "x") > 250 {
		t.Errorf("message: %q", msg)
	}
	if strings.Count(msg, "x") > 200 { // 200 characters of the line, not the 400 of the old excerpt
		t.Errorf("too long: %d x", strings.Count(msg, "x"))
	}
}

// A broken summaries block must not stop sync, import or the MCP server: they
// run with summaries off and say why on stderr.
func TestCommandsRunWithAnInvalidSummariesBlock(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	vault := filepath.Join(dir, "vault")
	os.WriteFile(cfgPath, []byte(`{"vault":`+strconv.Quote(vault)+`,"summaries":{"enabled":true,"tool":"bogus"}}`), 0o600)
	_, errOut, err := runImport(writeTemp(t, dir, "c.json", claudeExport), "-config", cfgPath)
	if err != nil || !strings.Contains(errOut, "warning") || !strings.Contains(errOut, "bogus") {
		t.Errorf("import: err=%v stderr=%q", err, errOut)
	}
	isolateHome(t) // the collectors must not read the real tools' folders
	if err := syncOnce([]string{"-config", cfgPath, "-vault", vault}); err != nil {
		t.Errorf("sync: %v", err)
	}
	if _, errOut, err := runSummarize("x.md", "-config", cfgPath); err == nil || !strings.Contains(errOut, "bogus") {
		t.Errorf("summarize: err=%v stderr=%q", err, errOut)
	}
}
