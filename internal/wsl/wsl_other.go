//go:build !windows

package wsl

func listDistros() []string { return nil }

func homes([]string) []string { return nil }
