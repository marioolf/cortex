//go:build darwin

package autostart

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// launchd reads this folder at login, so writing the file is enough; we do
// not call launchctl and leave the running process alone.
func plistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", plistLabel+".plist"), nil
}

func Enable() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	p, err := plistPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(plist(exe)), 0o644)
}

func Disable() error {
	p, err := plistPath()
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func Enabled() bool {
	p, err := plistPath()
	if err != nil {
		return false
	}
	_, err = os.Stat(p)
	return err == nil
}
