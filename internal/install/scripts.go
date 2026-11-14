package install

import (
	"encoding/base64"
	"errors"
	"path/filepath"
	"strings"
	"unicode/utf16"
)

// Paths never go into command text. The commands below are constant and read
// the paths from environment variables, so a path with quotes, spaces, $,
// backticks or semicolons cannot change what runs.

const (
	envLnk    = "CORTEX_LNK"
	envTarget = "CORTEX_TARGET"
	envDir    = "CORTEX_DIR"
)

// shortcutScript creates the .lnk with the WScript.Shell COM object, the usual
// way without a compiled helper.
const shortcutScript = `$s = (New-Object -ComObject WScript.Shell).CreateShortcut($env:` + envLnk + `);` +
	` $s.TargetPath = $env:` + envTarget + `;` +
	` $s.WorkingDirectory = $env:` + envDir + `;` +
	` $s.Description = 'Cortex';` +
	` $s.Save()`

// encodePS is PowerShell's -EncodedCommand form: base64 of UTF-16LE. It spares
// the script from the command-line quoting rules altogether.
func encodePS(script string) string {
	u := utf16.Encode([]rune(script))
	b := make([]byte, 0, 2*len(u))
	for _, c := range u {
		b = append(b, byte(c), byte(c>>8))
	}
	return base64.StdEncoding.EncodeToString(b)
}

// shortcutCommand returns the program, arguments and extra environment that
// create the shortcut lnk pointing at target.
func shortcutCommand(lnk, target string) (name string, args, env []string) {
	return "powershell.exe",
		[]string{"-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-EncodedCommand", encodePS(shortcutScript)},
		[]string{envLnk + "=" + lnk, envTarget + "=" + target, envDir + "=" + filepath.Dir(target)}
}

// deleteScript is the cmd.exe line that waits for the program to be deletable
// and then removes it and its folder. It tries once a second for a minute (ping
// is the usual sleep in a batch line). %VAR% inside double quotes is literal
// whatever the path holds, and a Windows path cannot contain a double quote,
// which deleteCommand checks anyway.
const deleteScript = `for /l %i in (1,1,60) do (` +
	`ping -n 2 127.0.0.1 >nul & ` +
	`del /f /q "%` + envTarget + `%" 2>nul & ` +
	`if not exist "%` + envTarget + `%" (rmdir "%` + envDir + `%" 2>nul & exit))`

// deleteCommand returns the raw command line (for SysProcAttr.CmdLine, because
// Go's argument escaping is not cmd's) and the environment of the helper that
// deletes target after this process exits.
func deleteCommand(target string) (cmdLine string, env []string, err error) {
	dir := filepath.Dir(target)
	if strings.ContainsAny(target+dir, "\"\r\n\x00") {
		return "", nil, errors.New("the path contains characters that cannot be handled safely")
	}
	return `cmd.exe /d /s /c "` + deleteScript + `"`,
		[]string{envTarget + "=" + target, envDir + "=" + dir}, nil
}
