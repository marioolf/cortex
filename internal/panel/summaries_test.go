package panel

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/marioolf/cortex/internal/config"
)

// fakeOnPath makes a program with that name findable on PATH, and nothing else.
func fakeOnPath(t *testing.T, name string) {
	t.Helper()
	dir := t.TempDir()
	file := name
	if runtime.GOOS == "windows" {
		file += ".exe"
	}
	if err := os.WriteFile(filepath.Join(dir, file), []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

func TestSummariesAPI(t *testing.T) {
	fakeOnPath(t, "claude")
	en := setup(t)
	post := func(body string) int {
		return en.do("POST", "/api/config", good, tok, "application/json", body).Code
	}
	st := en.state(t).Summaries
	if st.Enabled || st.Tool != "" || st.CustomAvailable || len(st.Command) != 0 {
		t.Fatalf("initial = %+v", st)
	}
	avail := map[string]bool{}
	for _, p := range st.Presets {
		avail[p.ID] = p.Available
	}
	if !avail["claude"] || avail["codex"] {
		t.Errorf("presets = %+v (only claude is on PATH)", st.Presets)
	}

	// Refused: nothing to enable, a tool that is not there, one that does not exist, custom without a command.
	for name, body := range map[string]string{
		"enable without a tool":    `{"summaries":{"enabled":true}}`,
		"codex is not installed":   `{"summaries":{"tool":"codex"}}`,
		"unknown tool":             `{"summaries":{"tool":"rm"}}`,
		"opencode is not offered":  `{"summaries":{"tool":"opencode"}}`,
		"custom without a command": `{"summaries":{"tool":"custom"}}`,
		"empty project":            `{"summaries":{"projects":[""]}}`,
	} {
		if c := post(body); c != 400 {
			t.Errorf("%s: status %d", name, c)
		}
	}
	// The command, the idle time and the rate can never come through the API.
	for name, body := range map[string]string{
		"command":      `{"summaries":{"command":["/bin/sh","-c","id"]}}`,
		"command+tool": `{"summaries":{"tool":"custom","command":["/bin/sh"]}}`,
		"idle":         `{"summaries":{"idle_minutes":1}}`,
		"rate":         `{"summaries":{"max_per_hour":1000}}`,
		"top level":    `{"command":["x"]}`,
	} {
		if c := post(body); c != 400 {
			t.Errorf("%s accepted: status %d", name, c)
		}
	}
	if disk, _ := config.LoadFile(en.cfgPath); len(disk.Summaries.Command) != 0 || disk.Summaries.Tool != "" || disk.Summaries.Enabled {
		t.Errorf("a refused request changed the file: %+v", disk.Summaries)
	}

	if c := post(`{"summaries":{"tool":"claude","projects":["<alpha>"]}}`); c != 200 {
		t.Fatalf("status %d", c)
	}
	if c := post(`{"summaries":{"enabled":true}}`); c != 200 {
		t.Fatalf("status %d", c)
	}
	st = en.state(t).Summaries
	if !st.Enabled || st.Tool != "claude" || len(st.Projects) != 1 || st.Projects[0] != "<alpha>" || st.Command[0] != "claude" {
		t.Errorf("state = %+v", st)
	}
	disk, _ := config.LoadFile(en.cfgPath)
	if !disk.Summaries.Enabled || disk.Summaries.Tool != "claude" || len(disk.Summaries.Projects) != 1 {
		t.Errorf("saved = %+v", disk.Summaries)
	}
	if c := post(`{"summaries":{"enabled":false,"projects":[]}}`); c != 200 || en.state(t).Summaries.Enabled {
		t.Error("could not switch off")
	}
}

func TestSummariesCustomFromTheConfigFile(t *testing.T) {
	fakeOnPath(t, "nothing-relevant")
	en := setupWith(t, func(c *config.Config) {
		c.Summaries.Command = []string{"/opt/my tool/summarise", "--fast"}
	}, nil)
	st := en.state(t).Summaries
	if !st.CustomAvailable || st.Tool != "" {
		t.Fatalf("state = %+v", st)
	}
	if c := en.do("POST", "/api/config", good, tok, "application/json", `{"summaries":{"tool":"custom"}}`).Code; c != 200 {
		t.Fatalf("custom refused although the file has a command: %d", c)
	}
	st = en.state(t).Summaries
	if st.Tool != "custom" || len(st.Command) != 2 || st.Command[0] != "/opt/my tool/summarise" {
		t.Errorf("state = %+v", st)
	}
	// Changing other things from the panel keeps the hand-written command.
	en.do("POST", "/api/config", good, tok, "application/json", `{"summaries":{"enabled":true,"projects":["<alpha>"]}}`)
	if disk, _ := config.LoadFile(en.cfgPath); len(disk.Summaries.Command) != 2 {
		t.Errorf("command lost: %+v", disk.Summaries)
	}
}

func TestSummariesRequireTheToken(t *testing.T) {
	en := setup(t)
	if c := en.do("POST", "/api/config", good, "wrong", "application/json", `{"summaries":{"enabled":true}}`).Code; c != 403 {
		t.Errorf("status %d", c)
	}
}

func TestPageHasTheSummariesSection(t *testing.T) {
	body := setup(t).do("GET", "/", good, "", "", "").Body.String()
	for _, want := range []string{"Summaries (optional)", "Off by default.", "sends the text to its AI provider and uses your account and quota",
		`id="sum-enabled"`, `id="sum-tool"`, `id="sum-cmd"`, `id="sum-projects"`, "Custom command from the config file", "cannot be set from this page",
		"Chats without a project are never summarised"} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}
