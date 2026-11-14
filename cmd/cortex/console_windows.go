//go:build windows

package main

import (
	"log"
	"os"
	"syscall"
)

// The Windows binary is built with -H windowsgui so that starting it at login
// or by double click shows no console. Such a process has no stdout or stderr,
// so when it is started from a terminal (`cortex.exe sync`) it attaches to that
// terminal's console and prints there. Best effort: any failure is ignored.
func init() {
	const attachParentProcess = ^uint32(0) // (DWORD)-1
	attach := syscall.NewLazyDLL("kernel32.dll").NewProc("AttachConsole")
	if r, _, _ := attach.Call(uintptr(attachParentProcess)); r == 0 {
		return // no parent console: started from the Start menu, login or a double click
	}
	// Leave redirections alone: `cortex.exe sync > out.txt` has a valid handle already.
	if _, err := os.Stdout.Stat(); err != nil {
		if f, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0); err == nil {
			os.Stdout = f
		}
	}
	if _, err := os.Stderr.Stat(); err != nil {
		if f, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0); err == nil {
			os.Stderr = f
		}
	}
	log.SetOutput(os.Stderr) // the logger kept the old (invalid) stderr
}
