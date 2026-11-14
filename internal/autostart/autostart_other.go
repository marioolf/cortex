//go:build !windows && !darwin

package autostart

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// XDG autostart: desktop environments run every entry in this folder at login.
func desktopPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "autostart", "cortex.desktop"), nil
}

// Enable registers the running program.
func Enable() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return EnableFor(exe)
}

// EnableFor registers exe, for the installer, which sets up the installed copy
// from another one.
func EnableFor(exe string) error {
	p, err := desktopPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(desktopFile(exe)), 0o644)
}

func Disable() error {
	p, err := desktopPath()
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func Enabled() bool {
	p, err := desktopPath()
	if err != nil {
		return false
	}
	_, err = os.Stat(p)
	return err == nil
}
