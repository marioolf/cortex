//go:build !windows

package summarize

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// alive tells whether pid runs; a zombie (killed, not yet reaped by init) does not count.
func alive(pid int) bool {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return syscall.Kill(pid, 0) == nil
	}
	f := string(b)
	return !strings.HasPrefix(f[strings.LastIndex(f, ")")+2:], "Z")
}

func waitGone(t *testing.T, pid int) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if !alive(pid) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Errorf("grandchild %d is still running", pid)
}

func readPID(t *testing.T, path string) int {
	t.Helper()
	for i := 0; i < 200; i++ {
		if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
			pid, _ := strconv.Atoi(string(b))
			return pid
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("the fake never recorded its child")
	return 0
}

func TestTimeoutKillsTheProcessTree(t *testing.T) {
	pidfile := filepath.Join(t.TempDir(), "pid")
	t.Setenv("CORTEX_FAKE_PIDFILE", pidfile)
	r := runner(t)
	r.Timeout = 700 * time.Millisecond
	if _, err := r.Run(context.Background(), self(t, "grandchild"), "p"); err == nil {
		t.Fatal("expected a timeout")
	}
	waitGone(t, readPID(t, pidfile))
}

func TestCancelKillsTheProcessTree(t *testing.T) {
	pidfile := filepath.Join(t.TempDir(), "pid")
	t.Setenv("CORTEX_FAKE_PIDFILE", pidfile)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { runner(t).Run(ctx, self(t, "grandchild"), "p"); close(done) }()
	pid := readPID(t, pidfile)
	cancel()
	<-done
	waitGone(t, pid)
}
