//go:build windows

package autostart

import (
	"os"
	"os/exec"
	"syscall"
)

func reg(args ...string) error {
	cmd := exec.Command("reg.exe", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd.Run()
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
	if !Enabled() {
		return nil
	}
	return reg("delete", runKey, "/v", valueName, "/f")
}

func Enabled() bool { return reg("query", runKey, "/v", valueName) == nil }
