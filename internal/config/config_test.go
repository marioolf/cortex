package config

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
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
