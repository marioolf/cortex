package main

import (
	"fmt"
	"os"
	"runtime"
)

// loggedError is a fatal error that has been written to the log and to stderr
// already: main exits with status 1 without printing it again.
type loggedError struct{ error }

func (e loggedError) Unwrap() error { return e.error }

// fatalBoxWanted decides whether a fatal start error also needs a message box:
// only on Windows, and only when there is no console to show it on (the
// windowsgui build started by double click or at login has no usable stderr).
func fatalBoxWanted(goos string, stderrWorks bool) bool {
	return goos == "windows" && !stderrWorks
}

// fatalMessage is the text of that box.
func fatalMessage(err error, logPath string) string {
	msg := fmt.Sprintf("Cortex could not start:\n\n%v", err)
	if logPath != "" {
		msg += fmt.Sprintf("\n\nMore details are in the log file:\n%s", logPath)
	}
	return msg
}

// reportFatal shows the box when it is wanted.
func reportFatal(err error, logPath string) {
	_, serr := os.Stderr.Stat()
	if fatalBoxWanted(runtime.GOOS, serr == nil) {
		showFatal("Cortex", fatalMessage(err, logPath))
	}
}
