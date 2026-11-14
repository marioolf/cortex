//go:build windows

package wsl

import (
	"os/exec"
	"path/filepath"
	"syscall"
)

func homes() []string {
	cmd := exec.Command("wsl.exe", "-l", "-q", "--running")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.Output()
	if err != nil {
		return nil // no WSL installed, or none running
	}
	var found []string
	// Only running distros: touching the path of a stopped one boots it, and
	// we get called every 30 seconds.
	for _, d := range parseDistros(out) {
		m, _ := filepath.Glob(homeGlob(d))
		found = append(found, m...)
	}
	return found
}
