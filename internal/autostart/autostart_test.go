package autostart

import (
	"strings"
	"testing"
)

func TestRegArgs(t *testing.T) {
	got := regArgs(`C:\Program Files\Cortex\cortex.exe`)
	want := []string{"add", `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`, "/v", "Cortex", "/t", "REG_SZ",
		"/d", `"C:\Program Files\Cortex\cortex.exe" run -no-browser`, "/f"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("got %q, want %q", got, want)
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

func TestParseRunValueAndRegisteredIsThis(t *testing.T) {
	out := "\r\nHKEY_CURRENT_USER\\Software\\Microsoft\\Windows\\CurrentVersion\\Run\r\n" +
		"    Cortex    REG_SZ    \"C:\\Users\\Ana\\AppData\\Local\\Programs\\Cortex\\cortex.exe\" run -no-browser\r\n\r\n"
	cmd, ok := parseRunValue(out)
	if !ok || cmd != `"C:\Users\Ana\AppData\Local\Programs\Cortex\cortex.exe" run -no-browser` {
		t.Fatalf("cmd = %q %v", cmd, ok)
	}
	for exe, want := range map[string]bool{
		`C:\Users\Ana\AppData\Local\Programs\Cortex\cortex.exe`: true,
		`c:\users\ana\appdata\local\programs\cortex\CORTEX.EXE`: true,
		`C:/Users/Ana/AppData/Local/Programs/Cortex/cortex.exe`: true,
		`C:\Users\Ana\Downloads\cortex.exe`:                     false,
	} {
		if got := registeredIsThis(out, exe); got != want {
			t.Errorf("registeredIsThis(%q) = %v", exe, got)
		}
	}
	if _, ok := parseRunValue("ERROR: The system was unable to find the specified registry key or value."); ok {
		t.Error("error text parsed as a value")
	}
	if registeredIsThis("ERROR: nothing", `C:\x\cortex.exe`) {
		t.Error("no value counts as registered")
	}
	// Unquoted command with spaces in the path.
	if got := commandExe(`C:\Program Files\Cortex\cortex.exe run -no-browser`); got != `C:\Program Files\Cortex\cortex.exe` {
		t.Errorf("commandExe = %q", got)
	}
	// Mangled accented paths on both sides cannot be told apart: trust the entry.
	mangled := "    Cortex    REG_SZ    \"C:\\Users\\Jos\xe9\\cortex.exe\" run -no-browser\r\n"
	if !registeredIsThis(mangled, "C:\\Users\\Jos\u00e9\\cortex.exe") {
		t.Error("accented path not trusted")
	}
	if registeredIsThis(mangled, `C:\Users\Jose\cortex.exe`) {
		t.Error("a plain path matched a mangled one")
	}
}
