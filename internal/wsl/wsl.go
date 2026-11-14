// Package wsl finds the home directories of running WSL distros so that a
// Cortex running on Windows can read what Linux-side tools saved there.
package wsl

import (
	"strings"
	"unicode/utf16"
)

// Homes returns the home directories of running distros as UNC paths, such as
// \\wsl.localhost\Ubuntu\home\mario. It returns nil when there are none, and
// always on systems other than Windows.
func Homes() []string { return homes() }

// parseDistros decodes the output of `wsl.exe -l -q --running`. wsl.exe writes
// UTF-16LE, usually with a byte order mark, and CRLF line endings.
func parseDistros(out []byte) []string {
	if len(out)%2 == 1 {
		out = out[:len(out)-1]
	}
	u := make([]uint16, 0, len(out)/2)
	for i := 0; i < len(out); i += 2 {
		u = append(u, uint16(out[i])|uint16(out[i+1])<<8)
	}
	text := strings.TrimPrefix(string(utf16.Decode(u)), "\ufeff")
	var names []string
	for _, line := range strings.Split(text, "\n") {
		if name := strings.TrimSpace(strings.ReplaceAll(line, "\x00", "")); name != "" {
			names = append(names, name)
		}
	}
	return names
}

// homeGlob is the pattern for the home directories of one distro.
func homeGlob(distro string) string {
	return `\\wsl.localhost\` + distro + `\home\*`
}
