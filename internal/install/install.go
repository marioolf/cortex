// Package install copies Cortex to a fixed per-user folder on Windows and
// undoes that. A downloaded cortex.exe is run from Downloads, and an autostart
// entry that points there breaks as soon as the download is deleted, so
// installing is an explicit step. Everything that touches the system (the
// registry, shortcuts, starting processes) goes through the System interface,
// and the paths are fields, so the logic is tested with fakes and temp dirs.
package install

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// System is what the installer asks of the operating system.
type System interface {
	// QuitRunning asks a Cortex that answers on the configured port to quit
	// and waits until it has. It is nil when none answers.
	QuitRunning() error
	CreateShortcut(lnk, target string) error
	EnableAutostart(exe string) error
	DisableAutostart() error
	// Start runs exe with args, detached from this process, and returns.
	Start(exe string, args []string) error
	// DeleteAfterExit removes target and its folder once this process, which
	// is running from it, has exited.
	DeleteAfterExit(target string) error
}

// Installer holds the paths of one installation.
type Installer struct {
	Exe        string // the running executable
	Target     string // where the installed copy lives
	Shortcut   string // the Start Menu .lnk
	ConfigFile string // the config file in use; purge removes it
	LogFile    string // cortex.log next to it
	Vault      string // never removed; only reported

	// FoldCase makes path comparisons case-insensitive (Windows).
	FoldCase bool
	// Canon turns a path into a comparable form; nil means Canonical.
	Canon func(string) string

	Sys     System
	Retries int                 // attempts to replace or delete a file that may still be in use; 0 means 20
	Wait    func(time.Duration) // pause between attempts; nil means time.Sleep
}

// Locations returns where the installed program and the Start Menu shortcut
// go, from the environment (LOCALAPPDATA and APPDATA).
func Locations(getenv func(string) string) (target, shortcut string, err error) {
	local, roaming := getenv("LOCALAPPDATA"), getenv("APPDATA")
	if local == "" || roaming == "" {
		return "", "", errors.New("LOCALAPPDATA or APPDATA is not set")
	}
	target = filepath.Join(local, "Programs", "Cortex", "cortex.exe")
	shortcut = filepath.Join(roaming, "Microsoft", "Windows", "Start Menu", "Programs", "Cortex.lnk")
	return target, shortcut, nil
}

// Canonical makes a path comparable: absolute, cleaned and with symlinks
// resolved when it exists. On Windows resolving also turns 8.3 short names
// (PROGRA~1) into long ones.
func Canonical(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	return filepath.Clean(p)
}

// Same reports whether a and b name the same file by path.
func (in *Installer) Same(a, b string) bool {
	canon := in.Canon
	if canon == nil {
		canon = Canonical
	}
	a, b = canon(a), canon(b)
	if in.FoldCase {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// Installed reports whether the running program is the installed copy.
func (in *Installer) Installed() bool { return in.Same(in.Exe, in.Target) }

func (in *Installer) retry(f func() error) error {
	n, wait := in.Retries, in.Wait
	if n <= 0 {
		n = 20
	}
	if wait == nil {
		wait = time.Sleep
	}
	var err error
	for i := 0; i < n; i++ {
		if err = f(); err == nil {
			return nil
		}
		if errors.Is(err, fs.ErrNotExist) {
			return err // waiting does not help
		}
		if i < n-1 {
			wait(250 * time.Millisecond)
		}
	}
	return err
}

// Install puts this program at Target, creates the shortcut and points
// autostart at it. stopRunning asks a Cortex on the configured port to quit
// first; the panel's own install passes false, since that Cortex is itself.
// Running it from the installed copy only repairs the shortcut and autostart.
// It does not start anything: the caller hands over.
func (in *Installer) Install(stopRunning bool) (copied bool, err error) {
	if !in.Installed() {
		if stopRunning {
			if err := in.Sys.QuitRunning(); err != nil {
				return false, err
			}
		}
		if err := in.copyReplace(); err != nil {
			return false, err
		}
		copied = true
	}
	if err := in.Sys.CreateShortcut(in.Shortcut, in.Target); err != nil {
		return copied, fmt.Errorf("Start Menu shortcut: %w", err)
	}
	if err := in.Sys.EnableAutostart(in.Target); err != nil {
		return copied, fmt.Errorf("autostart: %w", err)
	}
	return copied, nil
}

// copyReplace writes the program next to Target under a temporary name and
// renames it over Target, so a failure never leaves half a program there.
func (in *Installer) copyReplace() error {
	dir := filepath.Dir(in.Target)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	src, err := os.Open(in.Exe)
	if err != nil {
		return err
	}
	defer src.Close()
	tmp, err := os.CreateTemp(dir, ".cortex-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // a no-op after a successful rename
	if _, err := io.Copy(tmp, src); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// Windows refuses to replace a program that is running; the one we asked
	// to quit may need a moment to let go of the file.
	if err := in.retry(func() error { return os.Rename(tmp.Name(), in.Target) }); err != nil {
		return fmt.Errorf("cannot replace %s: %w (is Cortex still running from there? Quit it from its tray icon or panel and try again)", in.Target, err)
	}
	return nil
}

// Report says what Uninstall did, as lines for the user.
type Report struct {
	Removed []string
	Left    []string
}

// Uninstall quits a running Cortex, removes autostart, the shortcut and the
// installed program, and with purge the config file and log. The vault is
// never touched. A running program cannot delete itself, so when the
// uninstall runs from the installed copy, the deletion is handed to a helper
// that waits for this process to exit.
func (in *Installer) Uninstall(purge bool) (Report, error) {
	var r Report
	if err := in.Sys.QuitRunning(); err != nil {
		return r, err
	}
	if err := in.Sys.DisableAutostart(); err != nil {
		return r, fmt.Errorf("autostart: %w", err)
	}
	r.Removed = append(r.Removed, "autostart entry")

	if err := os.Remove(in.Shortcut); err == nil {
		r.Removed = append(r.Removed, in.Shortcut)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return r, err
	}

	if _, err := os.Stat(in.Target); err == nil {
		if in.Installed() {
			if err := in.Sys.DeleteAfterExit(in.Target); err != nil {
				return r, fmt.Errorf("cannot schedule the removal of %s: %w", in.Target, err)
			}
			r.Removed = append(r.Removed, in.Target+" (removed a moment after this program exits)")
		} else {
			if err := in.retry(func() error { return os.Remove(in.Target) }); err != nil {
				return r, fmt.Errorf("cannot remove %s: %w (is Cortex still running from there?)", in.Target, err)
			}
			os.Remove(filepath.Dir(in.Target)) // only when empty
			r.Removed = append(r.Removed, in.Target)
		}
	}

	if purge {
		if err := in.purgeConfig(&r); err != nil {
			return r, err
		}
	} else {
		r.Left = append(r.Left, in.ConfigFile+" (settings; -purge removes it)")
	}
	if in.Vault != "" {
		r.Left = append(r.Left, in.Vault+" (your notes; never removed)")
	}
	return r, nil
}

// purgeConfig removes the files Cortex keeps in the config folder and then the
// folder if that left it empty. The folder can be anywhere when -config was
// given, so it is never removed recursively.
func (in *Installer) purgeConfig(r *Report) error {
	dir := filepath.Dir(in.ConfigFile)
	files := []string{in.ConfigFile, in.LogFile}
	if tmp, _ := filepath.Glob(filepath.Join(dir, ".config-*.tmp")); tmp != nil {
		files = append(files, tmp...)
	}
	for _, f := range files {
		if f == "" {
			continue
		}
		if err := os.Remove(f); err == nil {
			r.Removed = append(r.Removed, f)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	if err := os.Remove(dir); err == nil {
		r.Removed = append(r.Removed, dir)
	} else if !errors.Is(err, fs.ErrNotExist) {
		r.Left = append(r.Left, dir+" (not empty)")
	}
	return nil
}

// Supported reports whether this OS has an installer (only Windows).
func Supported() bool { return runtime.GOOS == "windows" }
