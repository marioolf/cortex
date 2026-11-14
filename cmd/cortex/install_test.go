package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marioolf/cortex/internal/install"
)

type recSys struct {
	calls       []string
	shortcutErr error
}

func (r *recSys) QuitRunning() error { r.calls = append(r.calls, "quit"); return nil }
func (r *recSys) CreateShortcut(lnk, target string) error {
	r.calls = append(r.calls, "shortcut")
	if r.shortcutErr != nil {
		return r.shortcutErr
	}
	return os.WriteFile(lnk, nil, 0o644)
}
func (r *recSys) EnableAutostart(exe string) error {
	r.calls = append(r.calls, "autostart on "+exe)
	return nil
}
func (r *recSys) DisableAutostart() error { r.calls = append(r.calls, "autostart off"); return nil }
func (r *recSys) Start(exe string, args []string) error {
	r.calls = append(r.calls, "start "+exe+" "+strings.Join(args, " "))
	return nil
}
func (r *recSys) DeleteAfterExit(string) error {
	r.calls = append(r.calls, "delete-after-exit")
	return nil
}

func fakeEnv(t *testing.T, goos string, fromTarget bool) (installEnv, *install.Installer, *recSys) {
	t.Helper()
	root := t.TempDir()
	sys := &recSys{}
	in := &install.Installer{
		Exe:        filepath.Join(root, "Downloads", "cortex.exe"),
		Target:     filepath.Join(root, "Local", "Programs", "Cortex", "cortex.exe"),
		Shortcut:   filepath.Join(root, "Roaming", "Cortex.lnk"),
		ConfigFile: filepath.Join(root, "cfg", "config.json"),
		LogFile:    filepath.Join(root, "cfg", "cortex.log"),
		Vault:      filepath.Join(root, "vault"),
		Sys:        sys,
	}
	for _, d := range []string{"Downloads", "Local/Programs/Cortex", "Roaming", "cfg", "vault"} {
		os.MkdirAll(filepath.Join(root, d), 0o755)
	}
	if fromTarget {
		in.Exe = in.Target
	}
	os.WriteFile(in.Exe, []byte("exe"), 0o755)
	env := installEnv{goos: goos, new: func(configPath string, port int) (*install.Installer, error) {
		in.ConfigFile = configPath
		return in, nil
	}}
	return env, in, sys
}

func TestInstallUnsupportedSystems(t *testing.T) {
	for _, goos := range []string{"linux", "darwin"} {
		env, _, sys := fakeEnv(t, goos, false)
		var out bytes.Buffer
		for name, f := range map[string]func([]string, *bytes.Buffer) error{
			"install":   func(a []string, b *bytes.Buffer) error { return installCmd(a, b, b, env) },
			"uninstall": func(a []string, b *bytes.Buffer) error { return uninstallCmd(a, b, b, env) },
		} {
			err := f(nil, &out)
			if err == nil || !strings.Contains(err.Error(), "manual") || !strings.Contains(err.Error(), "PATH") {
				t.Errorf("%s on %s: err = %v", name, goos, err)
			}
		}
		if len(sys.calls) != 0 {
			t.Errorf("%s: touched the system: %q", goos, sys.calls)
		}
	}
}

func TestInstallCmd(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "c.json")
	env, in, sys := fakeEnv(t, "windows", false)
	var out, errb bytes.Buffer
	if err := installCmd([]string{"-port", "47971", "-config", cfg}, &out, &errb, env); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(in.Target); err != nil {
		t.Errorf("not copied: %v", err)
	}
	want := "quit|shortcut|autostart on " + in.Target + "|start " + in.Target + " run -port 47971 -config " + cfg
	if got := strings.Join(sys.calls, "|"); got != want {
		t.Errorf("calls = %q\nwant    %q", got, want)
	}
	if !strings.Contains(out.String(), in.Target) {
		t.Errorf("output = %q", out.String())
	}
}

func TestInstallCmdAlreadyInstalledStartsNothing(t *testing.T) {
	env, _, sys := fakeEnv(t, "windows", true)
	var out bytes.Buffer
	if err := installCmd(nil, &out, &out, env); err != nil {
		t.Fatal(err)
	}
	for _, c := range sys.calls {
		if strings.HasPrefix(c, "start") || c == "quit" {
			t.Errorf("unexpected %q", c)
		}
	}
	if !strings.Contains(out.String(), "already installed") {
		t.Errorf("output = %q", out.String())
	}
}

func TestInstallCmdRejectsArguments(t *testing.T) {
	env, _, _ := fakeEnv(t, "windows", false)
	var out bytes.Buffer
	if err := installCmd([]string{"extra"}, &out, &out, env); err == nil {
		t.Error("stray argument accepted")
	}
}

func TestUninstallCmdPurge(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "cfgdir", "config.json")
	env, in, _ := fakeEnv(t, "windows", false)
	os.MkdirAll(filepath.Dir(cfg), 0o755)
	os.WriteFile(cfg, []byte("{}"), 0o600)
	note := filepath.Join(in.Vault, "n.md")
	os.WriteFile(note, []byte("mine"), 0o644)
	os.WriteFile(in.Target, []byte("x"), 0o755)

	var out bytes.Buffer
	if err := uninstallCmd([]string{"-config", cfg, "-purge"}, &out, &out, env); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cfg); err == nil {
		t.Error("config survived -purge")
	}
	if _, err := os.Stat(note); err != nil {
		t.Error("vault touched:", err)
	}
	if !strings.Contains(out.String(), "left in place: "+in.Vault) {
		t.Errorf("output = %q", out.String())
	}

	// Without -purge the config stays and the output says where it is.
	os.MkdirAll(filepath.Dir(cfg), 0o755) // -purge removed the emptied folder
	os.WriteFile(cfg, []byte("{}"), 0o600)
	out.Reset()
	if err := uninstallCmd([]string{"-config", cfg}, &out, &out, env); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cfg); err != nil {
		t.Error("config removed without -purge")
	}
	if !strings.Contains(out.String(), cfg) {
		t.Errorf("output = %q", out.String())
	}
}

func TestRunArgs(t *testing.T) {
	if got := strings.Join(runArgs(47821, "/x/c.json", false, false), " "); got != "run -port 47821" {
		t.Errorf("got %q", got)
	}
	if got := strings.Join(runArgs(1, "/x/c.json", true, true), " "); got != "run -port 1 -config /x/c.json -no-tray" {
		t.Errorf("got %q", got)
	}
}

// A missing shortcut must not keep autostart or the installed copy from starting,
// and the failure is reported at the end, saying the program was copied.
func TestInstallCmdContinuesAfterAFailedShortcut(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "c.json")
	env, in, sys := fakeEnv(t, "windows", false)
	sys.shortcutErr = errors.New("powershell failed")
	var out, errb bytes.Buffer
	err := installCmd([]string{"-port", "47971", "-config", cfg}, &out, &errb, env)
	if err == nil || !strings.Contains(err.Error(), "WAS copied to "+in.Target) || !strings.Contains(err.Error(), "Start Menu shortcut") {
		t.Fatalf("err = %v", err)
	}
	want := "quit|shortcut|autostart on " + in.Target + "|start " + in.Target + " run -port 47971 -config " + cfg
	if got := strings.Join(sys.calls, "|"); got != want {
		t.Errorf("calls = %q\nwant    %q", got, want)
	}
	if _, err := os.Stat(in.Target); err != nil {
		t.Errorf("not copied: %v", err)
	}
	if strings.Contains(out.String(), "Added the Start Menu shortcut") {
		t.Errorf("claims a shortcut that failed: %q", out.String())
	}
}

// An unreadable config must not stop uninstall (or install): only the pairing code is needed.
func TestInstallEnvSurvivesABrokenConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOCALAPPDATA", filepath.Join(dir, "local"))
	t.Setenv("APPDATA", filepath.Join(dir, "roaming"))
	for name, body := range map[string]string{
		"not json":         `{broken`,
		"bad summaries":    `{"extension_token":"abc","summaries":{"tool":"bogus"}}`,
		"wrong type field": `{"extension_token":"abc","paused":"yes"}`,
	} {
		cfg := filepath.Join(dir, "c.json")
		os.WriteFile(cfg, []byte(body), 0o600)
		in, err := realInstallEnv.new(cfg, 47972)
		if err != nil || in == nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}
