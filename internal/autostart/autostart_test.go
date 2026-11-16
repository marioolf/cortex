package autostart

import (
	"strings"
	"testing"
)

func TestRunValueFor(t *testing.T) {
	got := runValueFor(`C:\Program Files\Cortex\cortex.exe`)
	if want := `"C:\Program Files\Cortex\cortex.exe" run -no-browser`; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestPlist(t *testing.T) {
	p := plist(`/Applications/My & Apps/cortex`)
	for _, want := range []string{
		"<string>io.github.marioolf.cortex</string>",
		"<string>/Applications/My &amp; Apps/cortex</string>",
		"<string>run</string>", "<string>-no-browser</string>",
		"<key>RunAtLoad</key>\n\t<true/>",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("plist lacks %q:\n%s", want, p)
		}
	}
}

func TestDesktopFile(t *testing.T) {
	d := desktopFile(`/home/me/my apps/cortex`)
	for _, want := range []string{"[Desktop Entry]", "Type=Application", `Exec="/home/me/my apps/cortex" run -no-browser`} {
		if !strings.Contains(d, want) {
			t.Errorf("desktop file lacks %q:\n%s", want, d)
		}
	}
	if got, want := desktopExec(`/a/$b "c"`), `"/a/\\$b \\"c\\""`; got != want {
		t.Errorf("desktopExec = %s, want %s", got, want)
	}
}

func TestRegisteredIsThis(t *testing.T) {
	const installed = `C:\Users\José\AppData\Local\Programs\Cortex\cortex.exe`
	cmd := runValueFor(installed)
	for exe, want := range map[string]bool{
		installed: true,
		`c:\users\josÉ\appdata\local\programs\cortex\CORTEX.EXE`: true, // case, also of accents
		`C:/Users/José/AppData/Local/Programs/Cortex/cortex.exe`: true,
		`C:\Users\Jose\AppData\Local\Programs\Cortex\cortex.exe`: false, // no accent
		`C:\Users\José\Downloads\cortex.exe`:                     false,
	} {
		if got := registeredIsThis(cmd, exe); got != want {
			t.Errorf("registeredIsThis(%q) = %v", exe, got)
		}
	}
	if registeredIsThis("", `C:\x\cortex.exe`) {
		t.Error("empty command counts as registered")
	}
}

func TestCommandExe(t *testing.T) {
	for cmd, want := range map[string]string{
		`"C:\Program Files\Cortex\cortex.exe" run -no-browser`: `C:\Program Files\Cortex\cortex.exe`,
		`C:\Program Files\Cortex\cortex.exe run -no-browser`:   `C:\Program Files\Cortex\cortex.exe`, // unquoted, spaces
		`C:\x\cortex run`: `C:\x\cortex`,
		`  `:              "",
	} {
		if got := commandExe(cmd); got != want {
			t.Errorf("commandExe(%q) = %q, want %q", cmd, got, want)
		}
	}
}
