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
