package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/marioolf/cortex/internal/wsl"
)

// homeVars are every variable through which the collectors, the config and the
// installer find the developer's real home, tool data and settings.
var homeVars = []string{"HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "CLAUDE_CONFIG_DIR", "CODEX_HOME"}

// isolateHome points all of homeVars at a temp dir and makes the WSL lookup
// find nothing, so that a test running the real collectors reads no session of
// the person running the tests.
func isolateHome(t *testing.T) string {
	t.Helper()
	home := filepath.Join(t.TempDir(), "home")
	for _, k := range homeVars {
		t.Setenv(k, home)
	}
	t.Cleanup(wsl.SetLookup(func() []string { return nil }))
	return home
}

// TestMain applies the same protection to the whole package, for tests that
// reach the real collectors without asking (import and run build them too).
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "cortex-test-home-")
	if err != nil {
		panic(err)
	}
	for _, k := range homeVars {
		os.Setenv(k, dir)
	}
	wsl.SetLookup(func() []string { return nil })
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
