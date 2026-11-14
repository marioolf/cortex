//go:build windows

package summarize

import (
	"os/exec"
	"strconv"
	"syscall"
)

// setupProc hides the console window and, on cancel, kills the whole process
// tree with taskkill /T /F before killing the child itself. taskkill was
// chosen over a Job Object because it is a few lines and needs no handle
// bookkeeping; it has to run while the child is still alive, which Cancel
// guarantees (it runs before the child is waited for).
func setupProc(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		kill := exec.Command("taskkill.exe", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid))
		kill.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		kill.Run()
		return cmd.Process.Kill()
	}
}

// afterRun does nothing here: once the child has exited its tree can no longer be found.
func afterRun(*exec.Cmd) {}
