//go:build !windows

package main

// showFatal does nothing here: a terminal shows the error.
func showFatal(title, msg string) {}
