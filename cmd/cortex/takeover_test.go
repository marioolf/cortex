package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDecideTakeover(t *testing.T) {
	same := func(a, b string) bool { return strings.EqualFold(a, b) }
	for name, c := range map[string]struct {
		running string
		err     error
		self    string
		want    takeoverAction
	}{
		"different file":       {`C:\Users\a\AppData\Local\Programs\Cortex\cortex.exe`, nil, `C:\Users\a\Downloads\cortex.exe`, takeOver},
		"same file":            {`C:\x\cortex.exe`, nil, `C:\X\Cortex.exe`, openExisting},
		"ping failed":          {"", errors.New("403"), `C:\x\cortex.exe`, openExisting},
		"ping failed, differs": {`C:\y\cortex.exe`, errors.New("no exe"), `C:\x\cortex.exe`, openExisting},
		"own path unknown":     {`C:\y\cortex.exe`, nil, "", openExisting},
	} {
		got, why := decideTakeover(c.running, c.err, c.self, same)
		if got != c.want || why == "" {
			t.Errorf("%s: %v %q", name, got, why)
		}
	}
}

// fakeCortex answers like a Cortex panel: the header, a ping that says where it
// runs from (when exe is not empty) and a quit that shuts it down.
func fakeCortex(t *testing.T, exe, code string) (port int, quits *atomic.Int32) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	quits = new(atomic.Int32)
	var srv *http.Server
	srv = &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Cortex", "1")
		if r.Header.Get("X-Cortex-Extension") != code {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		switch r.URL.Path {
		case "/api/ext/ping":
			m := map[string]any{"ok": true, "version": "1"}
			if exe != "" {
				m["exe"] = exe
			}
			json.NewEncoder(w).Encode(m)
		case "/api/ext/quit":
			quits.Add(1)
			w.Write([]byte(`{"ok":true}`))
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				srv.Shutdown(ctx)
			}()
		}
	})}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return ln.Addr().(*net.TCPAddr).Port, quits
}

func cfgWithCode(t *testing.T, code string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(p, []byte(`{"extension_token":"`+code+`","summaries":{"tool":"bogus"}}`), 0o600)
	return p
}

func TestClaimPortTakesOverADifferentCopy(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "installed", "cortex.exe")
	port, quits := fakeCortex(t, exe, "code-1")
	ln, existing, err := claimPort(port, cfgWithCode(t, "code-1"), filepath.Join(t.TempDir(), "cortex.exe"), 5*time.Second)
	if err != nil || existing || ln == nil {
		t.Fatalf("ln=%v existing=%v err=%v", ln, existing, err)
	}
	defer ln.Close()
	if quits.Load() != 1 {
		t.Errorf("quit requests = %d", quits.Load())
	}
}

func TestClaimPortLeavesTheSameFileAlone(t *testing.T) {
	self, _ := os.Executable()
	port, quits := fakeCortex(t, self, "code-1")
	ln, existing, err := claimPort(port, cfgWithCode(t, "code-1"), self, 2*time.Second)
	if err != nil || !existing || ln != nil || quits.Load() != 0 {
		t.Fatalf("ln=%v existing=%v err=%v quits=%d", ln, existing, err, quits.Load())
	}
}

func TestClaimPortFallsBackWhenItCannotAsk(t *testing.T) {
	other := filepath.Join(t.TempDir(), "cortex.exe")
	for name, c := range map[string]struct{ runningExe, runningCode, configCode string }{
		"older version, no exe": {"", "code-1", "code-1"},
		"wrong code":            {other, "code-1", "different"},
		"no code":               {other, "code-1", ""},
	} {
		port, quits := fakeCortex(t, c.runningExe, c.runningCode)
		ln, existing, err := claimPort(port, cfgWithCode(t, c.configCode), filepath.Join(t.TempDir(), "cortex.exe"), 2*time.Second)
		if err != nil || !existing || ln != nil || quits.Load() != 0 {
			t.Errorf("%s: ln=%v existing=%v err=%v quits=%d", name, ln, existing, err, quits.Load())
		}
	}
}

func TestClaimPortRefusesAForeignProgram(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/" {
			t.Errorf("request to a foreign program: %s %s", r.Method, r.URL)
		}
	})}
	go srv.Serve(ln)
	defer srv.Close()
	_, existing, err := claimPort(ln.Addr().(*net.TCPAddr).Port, cfgWithCode(t, "x"), "/x/cortex", time.Second)
	if err == nil || existing || !strings.Contains(err.Error(), "taken by another program") {
		t.Errorf("existing=%v err=%v", existing, err)
	}
}
