//go:build windows

package summarize

import (
	"os/exec"
	"syscall"
)

// hideWindow keeps a console window from flashing up for the tool.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}
