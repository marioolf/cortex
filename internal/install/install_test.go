package install

import (
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
)

type fakeSys struct {
	calls     []string
	quitErr   error
	shortcut  string // path the shortcut would be written to
	deleteErr error
}

func (f *fakeSys) QuitRunning() error { f.calls = append(f.calls, "quit"); return f.quitErr }
func (f *fakeSys) CreateShortcut(lnk, target string) error {
	f.calls = append(f.calls, "shortcut "+lnk+" -> "+target)
	return os.WriteFile(lnk, []byte("lnk"), 0o644) // the test's temp dir; stands in for the .lnk
}
func (f *fakeSys) EnableAutostart(exe string) error {
	f.calls = append(f.calls, "autostart on "+exe)
	return nil
}
func (f *fakeSys) DisableAutostart() error { f.calls = append(f.calls, "autostart off"); return nil }
func (f *fakeSys) Start(exe string, args []string) error {
	f.calls = append(f.calls, "start "+exe+" "+strings.Join(args, " "))
	return nil
}
func (f *fakeSys) DeleteAfterExit(target string) error {
	f.calls = append(f.calls, "delete-after-exit "+target)
	return f.deleteErr
}

// layout builds a fake user profile in a temp dir.
type layout struct {
	root, download, local, appdata string
	in                             *Installer
	sys                            *fakeSys
}

func newLayout(t *testing.T) *layout {
	t.Helper()
	root := t.TempDir()
	l := &layout{root: root, sys: &fakeSys{}}
	l.download = filepath.Join(root, "Downloads")
	l.local = filepath.Join(root, "Local")
	l.appdata = filepath.Join(root, "Roaming")
	env := map[string]string{"LOCALAPPDATA": l.local, "APPDATA": l.appdata}
	target, lnk, err := Locations(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{l.download, filepath.Dir(lnk), filepath.Join(root, "cfg"), filepath.Join(root, "vault")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	exe := filepath.Join(l.download, "cortex.exe")
	if err := os.WriteFile(exe, []byte("new build"), 0o755); err != nil {
		t.Fatal(err)
	}
	l.in = &Installer{
		Exe: exe, Target: target, Shortcut: lnk,
		ConfigFile: filepath.Join(root, "cfg", "config.json"), LogFile: filepath.Join(root, "cfg", "cortex.log"),
		Vault: filepath.Join(root, "vault"), Sys: l.sys, Wait: func(time.Duration) {},
	}
	return l
}

func TestLocations(t *testing.T) {
	env := map[string]string{"LOCALAPPDATA": `C:\Users\Ana\AppData\Local`, "APPDATA": `C:\Users\Ana\AppData\Roaming`}
	target, lnk, err := Locations(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(env["LOCALAPPDATA"], "Programs", "Cortex", "cortex.exe"); target != want {
		t.Errorf("target = %q, want %q", target, want)
	}
	if want := filepath.Join(env["APPDATA"], "Microsoft", "Windows", "Start Menu", "Programs", "Cortex.lnk"); lnk != want {
		t.Errorf("shortcut = %q, want %q", lnk, want)
	}
	if _, _, err := Locations(func(string) string { return "" }); err == nil {
		t.Error("empty environment accepted")
	}
}

func TestSame(t *testing.T) {
	ident := func(s string) string { return s }
	in := &Installer{Canon: ident}
	if in.Same(`C:\Users\Ana\cortex.exe`, `C:\Users\ana\cortex.exe`) {
		t.Error("case-sensitive installer treated different case as the same")
	}
	in.FoldCase = true
	if !in.Same(`C:\Users\Ana\cortex.exe`, `c:\users\ANA\Cortex.EXE`) {
		t.Error("case-insensitive installer did not match")
	}
	if in.Same(`C:\Users\Ana\cortex.exe`, `C:\Users\Ana\other.exe`) {
		t.Error("different files matched")
	}
	// A short-name spelling is resolved by the canonicalizer (EvalSymlinks on Windows).
	short := &Installer{FoldCase: true, Canon: func(p string) string {
		return strings.Replace(p, "PROGRA~1", "Programs", 1)
	}}
	if !short.Same(`C:\Users\ANA\PROGRA~1\Cortex\cortex.exe`, `C:\Users\Ana\Programs\Cortex\cortex.exe`) {
		t.Error("short and long names not matched")
	}
}

func TestSameResolvesRelativeAndSymlinks(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "a", "cortex")
	os.MkdirAll(filepath.Dir(real), 0o755)
	os.WriteFile(real, nil, 0o755)
	link := filepath.Join(dir, "link")
	if err := os.Symlink(filepath.Dir(real), link); err != nil {
		t.Skip("no symlinks here:", err)
	}
	in := &Installer{}
	if !in.Same(real, filepath.Join(link, "cortex")) {
		t.Error("symlinked path not recognised")
	}
	if !in.Same(real, filepath.Join(dir, "a", "..", "a", "cortex")) {
		t.Error("unclean path not recognised")
	}
}

func TestInstallCopiesAndRegisters(t *testing.T) {
	l := newLayout(t)
	copied, err := l.in.Install(true)
	if err != nil || !copied {
		t.Fatalf("Install = %v, %v", copied, err)
	}
	got, err := os.ReadFile(l.in.Target)
	if err != nil || string(got) != "new build" {
		t.Fatalf("target holds %q, %v", got, err)
	}
	want := []string{"quit", "shortcut " + l.in.Shortcut + " -> " + l.in.Target, "autostart on " + l.in.Target}
	if strings.Join(l.sys.calls, "|") != strings.Join(want, "|") {
		t.Errorf("calls = %q, want %q", l.sys.calls, want)
	}
	// Nothing but the program is left in the folder (no temp file).
	ents, _ := os.ReadDir(filepath.Dir(l.in.Target))
	if len(ents) != 1 {
		t.Errorf("install folder has %d entries, want 1", len(ents))
	}
}

func TestInstallReplacesExisting(t *testing.T) {
	l := newLayout(t)
	os.MkdirAll(filepath.Dir(l.in.Target), 0o755)
	os.WriteFile(l.in.Target, []byte("old build"), 0o755)
	if _, err := l.in.Install(false); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(l.in.Target); string(got) != "new build" {
		t.Errorf("target holds %q", got)
	}
	if len(l.sys.calls) > 0 && l.sys.calls[0] == "quit" {
		t.Error("stopRunning=false still asked a Cortex to quit")
	}
}

func TestInstallFailsWhenQuitFails(t *testing.T) {
	l := newLayout(t)
	l.sys.quitErr = errors.New("a Cortex is still running; quit it first")
	if _, err := l.in.Install(true); err == nil || !strings.Contains(err.Error(), "quit it first") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(l.in.Target); err == nil {
		t.Error("the program was copied although the running Cortex did not quit")
	}
	if len(l.sys.calls) != 1 {
		t.Errorf("shortcut or autostart set after a failed quit: %q", l.sys.calls)
	}
}

func TestInstallFromInstalledCopyOnlyRepairs(t *testing.T) {
	l := newLayout(t)
	os.MkdirAll(filepath.Dir(l.in.Target), 0o755)
	os.WriteFile(l.in.Target, []byte("installed"), 0o755)
	l.in.Exe = l.in.Target
	copied, err := l.in.Install(true)
	if err != nil || copied {
		t.Fatalf("Install = %v, %v", copied, err)
	}
	if got, _ := os.ReadFile(l.in.Target); string(got) != "installed" {
		t.Errorf("target rewritten: %q", got)
	}
	for _, c := range l.sys.calls {
		if c == "quit" {
			t.Error("quit asked of ourselves")
		}
	}
	if len(l.sys.calls) != 2 {
		t.Errorf("calls = %q", l.sys.calls)
	}
}

func TestInstallRetriesWhileFileIsBusy(t *testing.T) {
	// A directory at the target makes rename fail every time, standing in for
	// a program that is still running.
	l := newLayout(t)
	os.MkdirAll(filepath.Join(l.in.Target, "keep"), 0o755)
	waits := 0
	l.in.Retries, l.in.Wait = 3, func(time.Duration) { waits++ }
	_, err := l.in.Install(false)
	if err == nil || !strings.Contains(err.Error(), "Quit it") {
		t.Fatalf("err = %v", err)
	}
	if waits != 2 {
		t.Errorf("waited %d times, want 2", waits)
	}
	ents, _ := os.ReadDir(filepath.Dir(l.in.Target))
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("temp file %s left behind", e.Name())
		}
	}
}

func TestUninstallKeepsConfigAndVault(t *testing.T) {
	l := newLayout(t)
	if _, err := l.in.Install(false); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(l.in.ConfigFile, []byte("{}"), 0o600)
	note := filepath.Join(l.in.Vault, "note.md")
	os.WriteFile(note, []byte("mine"), 0o644)
	l.sys.calls = nil

	r, err := l.in.Uninstall(false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(l.in.Target); err == nil {
		t.Error("installed program still there")
	}
	if _, err := os.Stat(filepath.Dir(l.in.Target)); err == nil {
		t.Error("empty install folder still there")
	}
	if _, err := os.Stat(l.in.Shortcut); err == nil {
		t.Error("shortcut still there")
	}
	for _, p := range []string{l.in.ConfigFile, note} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s was removed: %v", p, err)
		}
	}
	if want := "quit|autostart off"; strings.Join(l.sys.calls, "|") != want {
		t.Errorf("calls = %q", l.sys.calls)
	}
	left := strings.Join(r.Left, "\n")
	if !strings.Contains(left, l.in.ConfigFile) || !strings.Contains(left, l.in.Vault) {
		t.Errorf("report does not say what was left: %q", left)
	}
}

func TestPurgeRemovesConfigOnly(t *testing.T) {
	l := newLayout(t)
	cfg := filepath.Dir(l.in.ConfigFile)
	os.WriteFile(l.in.ConfigFile, []byte("{}"), 0o600)
	os.WriteFile(l.in.LogFile, []byte("log"), 0o600)
	os.WriteFile(filepath.Join(cfg, ".config-123.tmp"), nil, 0o600)
	note := filepath.Join(l.in.Vault, "note.md")
	os.WriteFile(note, []byte("mine"), 0o644)

	if _, err := l.in.Uninstall(true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cfg); err == nil {
		t.Error("config folder still there")
	}
	if _, err := os.Stat(note); err != nil {
		t.Errorf("vault note removed: %v", err)
	}
}

func TestPurgeLeavesForeignFilesInConfigFolder(t *testing.T) {
	// -config can point anywhere; a folder with other things in it must survive.
	l := newLayout(t)
	cfg := filepath.Dir(l.in.ConfigFile)
	os.WriteFile(l.in.ConfigFile, []byte("{}"), 0o600)
	other := filepath.Join(cfg, "thesis.docx")
	os.WriteFile(other, []byte("important"), 0o644)

	r, err := l.in.Uninstall(true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Errorf("foreign file removed: %v", err)
	}
	if _, err := os.Stat(l.in.ConfigFile); err == nil {
		t.Error("config file still there")
	}
	if !strings.Contains(strings.Join(r.Left, "\n"), "not empty") {
		t.Errorf("report = %+v", r)
	}
}

func TestUninstallFromInstalledCopySchedulesDeletion(t *testing.T) {
	l := newLayout(t)
	os.MkdirAll(filepath.Dir(l.in.Target), 0o755)
	os.WriteFile(l.in.Target, []byte("installed"), 0o755)
	l.in.Exe = l.in.Target
	if _, err := l.in.Uninstall(false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(l.in.Target); err != nil {
		t.Error("a running program must not be deleted directly:", err)
	}
	if got := l.sys.calls[len(l.sys.calls)-1]; got != "delete-after-exit "+l.in.Target {
		t.Errorf("last call = %q", got)
	}
}

func TestUninstallWhenNothingInstalled(t *testing.T) {
	l := newLayout(t)
	if _, err := l.in.Uninstall(false); err != nil {
		t.Fatal(err)
	}
}

func TestUninstallStopsWhenQuitFails(t *testing.T) {
	l := newLayout(t)
	l.in.Install(false)
	l.sys.calls = nil
	l.sys.quitErr = errors.New("still running")
	if _, err := l.in.Uninstall(true); err == nil {
		t.Fatal("no error")
	}
	if _, err := os.Stat(l.in.Target); err != nil {
		t.Error("removed the program although Cortex would not quit")
	}
}

var nasty = []string{
	`C:\Users\O'Brien\AppData\Local\Programs\Cortex\cortex.exe`,
	`C:\Users\a b\$env:USERNAME\cortex.exe`,
	"C:\\Users\\back`tick\\$(calc)\\cortex.exe",
	`C:\Users\x;Remove-Item -Recurse C:\;\cortex.exe`,
	`C:\Users\"quoted"\cortex.exe`,
	`C:\Users\%PATH%&calc\cortex.exe`,
}

func decodePS(t *testing.T, enc string) string {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(enc)
	if err != nil || len(b)%2 != 0 {
		t.Fatalf("bad -EncodedCommand: %v", err)
	}
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = uint16(b[2*i]) | uint16(b[2*i+1])<<8
	}
	return string(utf16.Decode(u))
}

func TestShortcutCommandHasNoPathText(t *testing.T) {
	var scripts []string
	for _, p := range nasty {
		lnk := strings.Replace(p, "cortex.exe", "Cortex.lnk", 1)
		name, args, env := shortcutCommand(lnk, p)
		if name != "powershell.exe" {
			t.Errorf("name = %q", name)
		}
		joined := strings.Join(args, " ")
		if strings.Contains(joined, p) || strings.Contains(joined, lnk) {
			t.Errorf("path leaked into the arguments for %q", p)
		}
		if args[len(args)-2] != "-EncodedCommand" {
			t.Errorf("args = %q", args)
		}
		script := decodePS(t, args[len(args)-1])
		scripts = append(scripts, script)
		if strings.Contains(script, "Users") || strings.Contains(script, "calc") {
			t.Errorf("path text in the script: %q", script)
		}
		wantEnv := map[string]string{"CORTEX_LNK": lnk, "CORTEX_TARGET": p, "CORTEX_DIR": filepath.Dir(p)}
		for _, e := range env {
			k, v, _ := strings.Cut(e, "=")
			if wantEnv[k] != v {
				t.Errorf("env %s = %q, want %q", k, v, wantEnv[k])
			}
			delete(wantEnv, k)
		}
		if len(wantEnv) != 0 {
			t.Errorf("missing env %v", wantEnv)
		}
	}
	for _, s := range scripts[1:] {
		if s != scripts[0] {
			t.Error("the script changes with the path")
		}
	}
	for _, want := range []string{"WScript.Shell", "$env:CORTEX_LNK", "$env:CORTEX_TARGET", "$env:CORTEX_DIR", ".Save()"} {
		if !strings.Contains(scripts[0], want) {
			t.Errorf("script lacks %q: %s", want, scripts[0])
		}
	}
	args := []string{"-NoProfile", "-NonInteractive"}
	_, got, _ := shortcutCommand("a", "b")
	for _, a := range args {
		found := false
		for _, g := range got {
			found = found || g == a
		}
		if !found {
			t.Errorf("missing flag %s", a)
		}
	}
}

func TestDeleteCommand(t *testing.T) {
	for _, p := range nasty {
		line, env, err := deleteCommand(p)
		if strings.ContainsRune(p, '"') {
			if err == nil {
				t.Errorf("%q accepted", p)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%q: %v", p, err)
		}
		if strings.Contains(line, "Users") {
			t.Errorf("path in the command line: %s", line)
		}
		if !strings.HasPrefix(line, `cmd.exe /d /s /c "`) || !strings.HasSuffix(line, `"`) {
			t.Errorf("line = %s", line)
		}
		if env[0] != "CORTEX_TARGET="+p || env[1] != "CORTEX_DIR="+filepath.Dir(p) {
			t.Errorf("env = %q", env)
		}
	}
	line, _, _ := deleteCommand(`C:\x\cortex.exe`)
	for _, want := range []string{`del /f /q "%CORTEX_TARGET%"`, `rmdir "%CORTEX_DIR%"`, `if not exist "%CORTEX_TARGET%"`} {
		if !strings.Contains(line, want) {
			t.Errorf("line lacks %q", want)
		}
	}
}
