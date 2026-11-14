//go:build !windows

package summarize

import (
	"errors"
	"os/exec"
	"syscall"
)

// setupProc starts the command in a process group of its own and makes
// cancelling kill the whole group, so a tool that spawned helpers does not
// leave them running after a timeout, Quit or SIGTERM.
func setupProc(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return killTree(cmd) }
}

// killTree kills the command's group: the group ID is the command's PID.
func killTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return cmd.Process.Kill()
	}
	return nil
}

// afterRun removes what the command left behind when it finished by itself.
func afterRun(cmd *exec.Cmd) { killTree(cmd) }
