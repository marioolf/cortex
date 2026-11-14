//go:build windows

package autostart

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

func regCmd(args ...string) *exec.Cmd {
	cmd := exec.Command("reg.exe", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd
}

func reg(args ...string) error { return regCmd(args...).Run() }

// registered returns the output of the query for the Cortex value.
func registered() (string, bool) {
	out, err := regCmd("query", runKey, "/v", valueName).Output()
	return string(out), err == nil
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
	return reg(regArgs(exe)...)
}

func Disable() error {
	if _, ok := registered(); !ok {
		return nil
	}
	return reg("delete", runKey, "/v", valueName, "/f")
}

// Enabled reports whether the registered command starts THIS program: an
// entry that points at another copy (the installed one, seen from a download)
// does not count.
func Enabled() bool {
	out, ok := registered()
	if !ok {
		return false
	}
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil { // long names instead of 8.3 ones
		exe = r
	}
	return registeredIsThis(out, exe)
}
