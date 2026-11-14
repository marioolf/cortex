package main

import (
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestFatalBoxDecision(t *testing.T) {
	for _, c := range []struct {
		goos   string
		stderr bool
		want   bool
	}{
		{"windows", false, true}, {"windows", true, false}, {"linux", false, false}, {"darwin", false, false},
	} {
		if got := fatalBoxWanted(c.goos, c.stderr); got != c.want {
			t.Errorf("%s stderr=%v: %v", c.goos, c.stderr, got)
		}
	}
	m := fatalMessage(errors.New("port 1 is taken"), `C:\x\cortex.log`)
	if !strings.Contains(m, "port 1 is taken") || !strings.Contains(m, `C:\x\cortex.log`) {
		t.Errorf("message = %q", m)
	}
}

// The first run has no config folder yet; the log must still be there from the first line.
func TestSetupLogCreatesThePrivateFolder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new", "cortex", logName)
	closeLog, err := setupLog(path)
	if err != nil {
		t.Fatal(err)
	}
	log.Print("first line")
	closeLog()
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "first line") {
		t.Errorf("log = %q", b)
	}
	if st, _ := os.Stat(filepath.Dir(path)); runtime.GOOS != "windows" && st.Mode().Perm() != 0o700 {
		t.Errorf("folder mode = %v", st.Mode().Perm())
	}
}

// A fatal start error must be in the log file, which does not exist before the run.
func TestRunLogsAFatalErrorToTheFile(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	go http.Serve(ln, http.NotFoundHandler()) // some other program on the port
	cfg := filepath.Join(t.TempDir(), "fresh", "config.json")
	err := run([]string{"-config", cfg, "-port", strconv.Itoa(ln.Addr().(*net.TCPAddr).Port), "-no-browser", "-no-tray", "-no-autostart"})
	var logged loggedError
	if !errors.As(err, &logged) {
		t.Fatalf("err = %v, want a logged error", err)
	}
	b, rerr := os.ReadFile(filepath.Join(filepath.Dir(cfg), logName))
	if rerr != nil {
		t.Fatalf("no log file: %v", rerr)
	}
	log := string(b)
	if !strings.Contains(log, "Cortex ") || !strings.Contains(log, "fatal: port") || !strings.Contains(log, "taken by another program") {
		t.Errorf("log = %q", log)
	}
	if _, err := os.Stat(cfg); err == nil {
		t.Error("a failed start wrote a config")
	}
}
