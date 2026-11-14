//go:build windows

package wsl

import (
	"os/exec"
	"path/filepath"
	"syscall"
)

// listDistros returns the names of the running distros.
func listDistros() []string {
	cmd := exec.Command("wsl.exe", "-l", "-q", "--running")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.Output()
	if err != nil {
		return nil // no WSL installed, or none running
	}
	// Only running distros: touching the path of a stopped one boots it, and
	// we get called every 30 seconds.
	return parseDistros(out)
}

func homes(distros []string) []string {
	var found []string
	for _, d := range distros {
		m, _ := filepath.Glob(homeGlob(d))
		found = append(found, m...)
	}
	return found
}
