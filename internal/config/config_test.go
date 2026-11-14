package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestSaveRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.json")
	want := &Config{Vault: "/v", DisabledSources: []string{"x"}, ExcludedProjects: []string{"p"}, Paused: true}
	if err := want.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
	if runtime.GOOS != "windows" {
		st, _ := os.Stat(path)
		if st.Mode().Perm() != 0o600 {
			t.Errorf("mode = %v", st.Mode().Perm())
		}
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".config-*")); len(left) != 0 {
		t.Errorf("temp files left: %v", left)
	}
}

func TestSummariesDefaultsAndOff(t *testing.T) {
	c, err := LoadFile(filepath.Join(t.TempDir(), "none.json"))
	if err != nil {
		t.Fatal(err)
	}
	s := c.Summaries
	if s.Enabled || s.Tool != "" || s.Idle() != 15*time.Minute || s.Hourly() != 20 {
		t.Errorf("defaults = %+v", s)
	}
	s.IdleMinutes, s.MaxPerHour = 3, 5
	if s.Idle() != 3*time.Minute || s.Hourly() != 5 {
		t.Errorf("explicit values ignored: %v %v", s.Idle(), s.Hourly())
	}
}

func TestSummariesValidation(t *testing.T) {
	abs, _ := filepath.Abs("x")
	cases := []struct {
		name string
		s    Summaries
		ok   bool
	}{
		{"empty", Summaries{}, true},
		{"preset", Summaries{Enabled: true, Tool: "claude", Projects: []string{"a"}}, true},
		{"custom with command", Summaries{Tool: "custom", Command: []string{abs, "--y"}}, true},
		{"custom without command", Summaries{Tool: "custom"}, false},
		{"custom with blank command", Summaries{Tool: "custom", Command: []string{" "}}, false},
		{"unknown tool", Summaries{Tool: "rm"}, false},
		{"negative idle", Summaries{IdleMinutes: -1}, false},
		{"negative rate", Summaries{MaxPerHour: -1}, false},
		{"custom relative ./x", Summaries{Tool: "custom", Command: []string{"./x"}}, false},
		{"custom relative dir/x", Summaries{Tool: "custom", Command: []string{"bin/x"}}, false},
		{"custom dot-dot", Summaries{Tool: "custom", Command: []string{"../x"}}, false},
		{"custom backslash", Summaries{Tool: "custom", Command: []string{`bin\x`}}, false},
		{"custom bare name", Summaries{Tool: "custom", Command: []string{"summarise"}}, true},
		{"blank project", Summaries{Projects: []string{""}}, false},
	}
	for _, c := range cases {
		err := (&Config{Vault: "/v", Summaries: c.s}).Validate()
		if (err == nil) != c.ok {
			t.Errorf("%s: err = %v", c.name, err)
		}
	}
}

// A broken summaries block must never stop Cortex: it loads with summaries off
// and a warning, and a save keeps what the user wrote.
func TestInvalidSummariesLoadAsOffWithAWarning(t *testing.T) {
	for name, body := range map[string]string{
		"unknown tool":        `{"summaries":{"enabled":true,"tool":"bogus"}}`,
		"custom no command":   `{"summaries":{"enabled":true,"tool":"custom"}}`,
		"negative":            `{"summaries":{"tool":"claude","idle_minutes":-5}}`,
		"wrong type":          `{"summaries":{"enabled":"yes","tool":"claude"}}`,
		"wrong type command":  `{"summaries":{"tool":"custom","command":"/bin/x"}}`,
		"block is not object": `{"summaries":[1,2]}`,
		"relative command":    `{"summaries":{"enabled":true,"tool":"custom","command":["./x"]}}`,
	} {
		path := filepath.Join(t.TempDir(), "c.json")
		os.WriteFile(path, []byte(body), 0o600)
		c, err := LoadFile(path)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if c.Summaries.Enabled || c.Summaries.Tool != "" || len(c.Warnings) != 1 || !strings.Contains(c.Warnings[0], "summaries") {
			t.Errorf("%s: summaries = %+v, warnings = %q", name, c.Summaries, c.Warnings)
		}
		// Saving (a panel change) keeps the block as written.
		c.Paused = true
		if err := c.Save(path); err != nil {
			t.Fatal(err)
		}
		b, _ := os.ReadFile(path)
		if !strings.Contains(string(b), `"paused": true`) || strings.Contains(string(b), "warnings") {
			t.Errorf("%s: saved %s", name, b)
		}
		raw := body[len(`{"summaries":`) : len(body)-1]
		var got struct{ Summaries json.RawMessage }
		json.Unmarshal(b, &got)
		var a, w any
		json.Unmarshal(got.Summaries, &a)
		json.Unmarshal([]byte(raw), &w)
		if !reflect.DeepEqual(a, w) {
			t.Errorf("%s: summaries rewritten as %s", name, got.Summaries)
		}
	}
	// A file that is not JSON at all stays an error.
	path := filepath.Join(t.TempDir(), "c.json")
	os.WriteFile(path, []byte(`{not json`), 0o600)
	if _, err := LoadFile(path); err == nil {
		t.Error("garbage loaded")
	}
}

func TestPairingNeverFailsOnABrokenConfig(t *testing.T) {
	dir := t.TempDir()
	write := func(s string) string {
		p := filepath.Join(dir, "c.json")
		os.WriteFile(p, []byte(s), 0o600)
		return p
	}
	tok, vault, err := Pairing(write(`{"extension_token":"abc","vault":"/v","summaries":{"tool":7},"paused":"x"}`))
	if tok != "abc" || vault != "/v" || err != nil {
		t.Errorf("%q %q %v", tok, vault, err)
	}
	if _, _, err := Pairing(write(`{broken`)); err == nil {
		t.Error("no problem reported for invalid JSON")
	}
	if tok, _, err := Pairing(filepath.Join(dir, "missing.json")); tok != "" || err != nil {
		t.Errorf("missing file: %q %v", tok, err)
	}
}
