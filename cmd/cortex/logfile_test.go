package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestOpenLog(t *testing.T) {
	cases := []struct {
		name    string
		initial int // bytes already in the file; -1 for no file
		want    int // size after writing "x"
	}{
		{"new", -1, 1},
		{"small is appended to", 10, 11},
		{"at the limit is appended to", maxLogSize, maxLogSize + 1},
		{"large is started over", maxLogSize + 1, 1},
	}
	for _, c := range cases {
		path := filepath.Join(t.TempDir(), "cortex.log")
		if c.initial >= 0 {
			os.WriteFile(path, []byte(strings.Repeat("a", c.initial)), 0o644)
		}
		f, err := openLog(path)
		if err != nil {
			t.Fatal(err)
		}
		f.WriteString("x")
		f.Close()
		st, _ := os.Stat(path)
		if int(st.Size()) != c.want {
			t.Errorf("%s: size = %d, want %d", c.name, st.Size(), c.want)
		}
		if c.initial < 0 && runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
			t.Errorf("%s: mode = %v, want 0600", c.name, st.Mode().Perm())
		}
	}
}
