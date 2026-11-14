// Package autostart registers Cortex to start when the user logs in. Only the
// content generators live here; each OS has its own file for the registration.
package autostart

import (
	"encoding/xml"
	"path"
	"regexp"
	"strings"
)

// Arguments Cortex is started with at login: the panel is there when you
// want it, not thrown at you on every boot.
const runArgs = "run -no-browser"

// quoted wraps a path in double quotes for Windows-style command lines.
func quoted(exe string) string { return `"` + exe + `"` }

// regArgs is the reg.exe command line that sets the Run value. exec quotes
// each element for us, so the value stays one argument even with spaces.
func regArgs(exe string) []string {
	return []string{"add", runKey, "/v", valueName, "/t", "REG_SZ", "/d", quoted(exe) + " " + runArgs, "/f"}
}

const (
	runKey    = `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`
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

// runValue matches the Cortex line of `reg query`'s output:
//
//	Cortex    REG_SZ    "C:\Users\me\cortex.exe" run -no-browser
var runValue = regexp.MustCompile(`(?im)^\s*` + valueName + `\s+REG_(?:EXPAND_)?SZ\s+(.*?)\s*$`)

// parseRunValue returns the command registered under the Cortex value.
func parseRunValue(out string) (cmd string, ok bool) {
	m := runValue.FindStringSubmatch(out)
	if m == nil {
		return "", false
	}
	return m[1], true
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

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// samePath compares two Windows paths: either slash, any case.
func samePath(a, b string) bool {
	norm := func(p string) string {
		return path.Clean(strings.ToLower(strings.ReplaceAll(p, `\`, "/")))
	}
	return norm(a) == norm(b)
}

// registeredIsThis tells whether the `reg query` output registers exe. reg.exe
// writes in the console's code page, so a registered path with accented
// characters comes back mangled; when both sides have such characters the
// paths cannot be compared and the registration is trusted.
func registeredIsThis(out, exe string) bool {
	cmd, ok := parseRunValue(out)
	if !ok {
		return false
	}
	reg := commandExe(cmd)
	if !isASCII(reg) && !isASCII(exe) {
		return true
	}
	return samePath(reg, exe)
}
