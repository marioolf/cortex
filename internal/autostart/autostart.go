// Package autostart registers Cortex to start when the user logs in. Only the
// content generators live here; each OS has its own file for the registration.
package autostart

import (
	"encoding/xml"
	"path"
	"strings"
)

// Arguments Cortex is started with at login: the panel is there when you
// want it, not thrown at you on every boot.
const runArgs = "run -no-browser"

// quoted wraps a path in double quotes for Windows-style command lines.
func quoted(exe string) string { return `"` + exe + `"` }

// runValueFor is the command written to the Run key: the quoted program and
// its arguments, which is what Windows expects there (the quotes keep a path
// with spaces in one piece).
func runValueFor(exe string) string { return quoted(exe) + " " + runArgs }

const (
	// runKey is the Run key under HKEY_CURRENT_USER, without the hive.
	runKey    = `Software\Microsoft\Windows\CurrentVersion\Run`
	valueName = "Cortex"
)

const plistLabel = "io.github.marioolf.cortex"

func plist(exe string) string {
	var esc strings.Builder
	xml.EscapeText(&esc, []byte(exe))
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + plistLabel + `</string>
	<key>ProgramArguments</key>
	<array>
		<string>` + esc.String() + `</string>
		<string>run</string>
		<string>-no-browser</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
</dict>
</plist>
`
}

// desktopExec quotes a path for the Exec key of a .desktop file, where
// quoted arguments need ", `, $ and \ escaped, and % doubled; the backslashes
// are then doubled again for the file's own string escaping.
func desktopExec(exe string) string {
	var b strings.Builder
	for _, r := range exe {
		switch r {
		case '"', '`', '$':
			b.WriteString(`\\` + string(r))
		case '\\':
			b.WriteString(`\\\\`)
		case '%':
			b.WriteString("%%")
		default:
			b.WriteRune(r)
		}
	}
	return `"` + b.String() + `"`
}

func desktopFile(exe string) string {
	return `[Desktop Entry]
Type=Application
Name=Cortex
Comment=Saves your AI conversations as Markdown notes
Exec=` + desktopExec(exe) + " " + runArgs + `
Terminal=false
`
}

// commandExe is the program of a registered command line: the quoted part, or
// else everything up to ".exe" (an unquoted path may hold spaces).
func commandExe(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if strings.HasPrefix(cmd, `"`) {
		if i := strings.Index(cmd[1:], `"`); i >= 0 {
			return cmd[1 : 1+i]
		}
		return cmd[1:]
	}
	if i := strings.Index(strings.ToLower(cmd), ".exe"); i >= 0 {
		return cmd[:i+4]
	}
	f := strings.Fields(cmd)
	if len(f) == 0 {
		return ""
	}
	return f[0]
}

// samePath compares two Windows paths: either slash, any case.
func samePath(a, b string) bool {
	norm := func(p string) string {
		return path.Clean(strings.ToLower(strings.ReplaceAll(p, `\`, "/")))
	}
	return norm(a) == norm(b)
}

// registeredIsThis tells whether the registered command line starts exe. Both
// sides are real Unicode strings (the registry stores UTF-16), so accented
// paths compare like any other.
func registeredIsThis(cmd, exe string) bool {
	return samePath(commandExe(cmd), exe)
}
