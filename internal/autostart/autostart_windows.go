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

func Enable() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return reg(regArgs(exe)...)
}

func Disable() error {
	if !Enabled() {
		return nil
	}
	return reg("delete", runKey, "/v", valueName, "/f")
}

func Enabled() bool { return reg("query", runKey, "/v", valueName) == nil }
