//go:build !windows

package summarize

import "os/exec"

func hideWindow(*exec.Cmd) {}
