//go:build windows

package autostart

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows/registry"
)

// runKeyPath is a variable so that a test can point the code at a throwaway key.
var runKeyPath = runKey

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
func EnableFor(exe string) error { return enableAt(runKeyPath, exe) }

func enableAt(keyPath, exe string) error {
	// CreateKey opens the key when it exists, as the Run key always does.
	k, _, err := registry.CreateKey(registry.CURRENT_USER, keyPath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	return k.SetStringValue(valueName, runValueFor(exe))
}

func Disable() error { return disableAt(runKeyPath) }

func disableAt(keyPath string) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, keyPath, registry.SET_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer k.Close()
	if err := k.DeleteValue(valueName); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	return nil
}

// Enabled reports whether the registered command starts THIS program: an
// entry that points at another copy (the installed one, seen from a download)
// does not count.
func Enabled() bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil { // long names instead of 8.3 ones
		exe = r
	}
	return enabledAt(runKeyPath, exe)
}

func enabledAt(keyPath, exe string) bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, keyPath, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	cmd, _, err := k.GetStringValue(valueName)
	return err == nil && registeredIsThis(cmd, exe)
}
