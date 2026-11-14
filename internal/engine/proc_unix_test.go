//go:build !windows

package engine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/marioolf/cortex/internal/session"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// goneEventually waits for pid to disappear (a zombie counts as gone).
func goneEventually(pid int) bool {
	for i := 0; i < 100; i++ {
		b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
		if err != nil {
			if syscall.Kill(pid, 0) != nil {
				return true
			}
		} else if f := string(b); strings.HasPrefix(f[strings.LastIndex(f, ")")+2:], "Z") {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

func TestStopKillsTheSummariserProcessTree(t *testing.T) {
	pidfile := filepath.Join(t.TempDir(), "pid")
	t.Setenv("CORTEX_FAKE_PIDFILE", pidfile)
	s := setupSummaries(t, "grandchild", []session.Session{conv("a1", "/work/alpha", 4)}, nil)
	s.round()
	s.clk.Add(20 * time.Minute)
	s.e.Sync()
	waitFor(t, pidfile)
	var pid int
	for i := 0; i < 100 && pid == 0; i++ {
		b, _ := os.ReadFile(pidfile)
		pid, _ = strconv.Atoi(string(b))
		time.Sleep(20 * time.Millisecond)
	}
	s.e.Stop()
	if !goneEventually(pid) {
		t.Errorf("grandchild %d survived Engine.Stop", pid)
	}
}
