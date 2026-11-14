package config

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
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
	cases := []struct {
		name string
		s    Summaries
		ok   bool
	}{
		{"empty", Summaries{}, true},
		{"preset", Summaries{Enabled: true, Tool: "claude", Projects: []string{"a"}}, true},
		{"custom with command", Summaries{Tool: "custom", Command: []string{"/bin/x", "--y"}}, true},
		{"custom without command", Summaries{Tool: "custom"}, false},
		{"custom with blank command", Summaries{Tool: "custom", Command: []string{" "}}, false},
		{"unknown tool", Summaries{Tool: "rm"}, false},
		{"negative idle", Summaries{IdleMinutes: -1}, false},
		{"negative rate", Summaries{MaxPerHour: -1}, false},
		{"blank project", Summaries{Projects: []string{""}}, false},
	}
	for _, c := range cases {
		err := (&Config{Vault: "/v", Summaries: c.s}).Validate()
		if (err == nil) != c.ok {
			t.Errorf("%s: err = %v", c.name, err)
		}
	}
	// An invalid file is rejected on load too.
	path := filepath.Join(t.TempDir(), "c.json")
	os.WriteFile(path, []byte(`{"summaries":{"tool":"custom"}}`), 0o600)
	if _, err := LoadFile(path); err == nil {
		t.Error("custom without command loaded")
	}
}
