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
		runVer  string
		err     error
		self    string
		selfVer string
		want    takeoverAction
	}{
		"different file":       {`C:\Users\a\AppData\Local\Programs\Cortex\cortex.exe`, "1.0.0", nil, `C:\Users\a\Downloads\cortex.exe`, "1.0.0", takeOver},
		"same file":            {`C:\x\cortex.exe`, "1.0.0", nil, `C:\X\Cortex.exe`, "1.0.0", openExisting},
		"ping failed":          {"", "", errors.New("403"), `C:\x\cortex.exe`, "1.0.0", openExisting},
		"ping failed, differs": {`C:\y\cortex.exe`, "", errors.New("no exe"), `C:\x\cortex.exe`, "1.0.0", openExisting},
		"own path unknown":     {`C:\y\cortex.exe`, "1.0.0", nil, "", "1.0.0", openExisting},
		"running is newer":     {`C:\y\cortex.exe`, "1.10.0", nil, `C:\x\cortex.exe`, "1.9.5", openExisting},
		"running is newer, v":  {`C:\y\cortex.exe`, "v2", nil, `C:\x\cortex.exe`, "1.9.5", openExisting},
		"running is older":     {`C:\y\cortex.exe`, "1.9.5", nil, `C:\x\cortex.exe`, "1.10.0", takeOver},
		"same version":         {`C:\y\cortex.exe`, "1.2", nil, `C:\x\cortex.exe`, "1.2.0", takeOver},
		"running is dev":       {`C:\y\cortex.exe`, "dev", nil, `C:\x\cortex.exe`, "1.0.0", takeOver},
		"this is dev":          {`C:\y\cortex.exe`, "3.0.0", nil, `C:\x\cortex.exe`, "dev", takeOver},
		"unparsable":           {`C:\y\cortex.exe`, "3.0.0-rc1", nil, `C:\x\cortex.exe`, "1.0.0", takeOver},
		"empty version":        {`C:\y\cortex.exe`, "", nil, `C:\x\cortex.exe`, "1.0.0", takeOver},
	} {
		got, why := decideTakeover(c.running, c.runVer, c.err, c.self, c.selfVer, same)
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
	return serveFakeCortex(t, ln, exe, code, nil)
}

// serveFakeCortex is fakeCortex on ln; onQuit, when set, runs instead of the
// normal answer to a quit request.
func serveFakeCortex(t *testing.T, ln net.Listener, exe, code string, onQuit func(w http.ResponseWriter, srv *http.Server)) (port int, quits *atomic.Int32) {
	t.Helper()
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
			if onQuit != nil {
				onQuit(w, srv)
				return
			}
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

func TestClaimPortLeavesANewerRunningCopyAlone(t *testing.T) {
	old := version
	defer func() { version = old }()
	// fakeCortex reports version "1"; this copy is older.
	port, quits := fakeCortex(t, filepath.Join(t.TempDir(), "installed", "cortex.exe"), "code-1")
	version = "0.9.0"
	_, existing, err := claimPort(port, cfgWithCode(t, "code-1"), filepath.Join(t.TempDir(), "cortex.exe"), 2*time.Second)
	if err != nil || !existing || quits.Load() != 0 {
		t.Fatalf("existing=%v err=%v quits=%d", existing, err, quits.Load())
	}
}

// Two new copies start at once: the other one wins, so the old Cortex quits on
// the request of the other and this one's request dies with EOF. This copy
// must then treat the winner as "already running", not fail.
func TestClaimPortLosingTheRaceCountsAsAlreadyRunning(t *testing.T) {
	old := raceGrace
	raceGrace = 300 * time.Millisecond
	defer func() { raceGrace = old }()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	port, _ := serveFakeCortex(t, ln, filepath.Join(t.TempDir(), "installed", "cortex.exe"), "code-1", func(w http.ResponseWriter, srv *http.Server) {
		// The winner takes the port, then this request's connection drops (EOF).
		conn, _, _ := w.(http.Hijacker).Hijack()
		srv.Close()
		nl, lerr := net.Listen("tcp", addr)
		if lerr != nil {
			t.Error(lerr)
		} else {
			serveFakeCortex(t, nl, filepath.Join(t.TempDir(), "winner", "cortex.exe"), "code-1", nil)
		}
		conn.Close()
	})
	ln2, existing, err := claimPort(port, cfgWithCode(t, "code-1"), filepath.Join(t.TempDir(), "cortex.exe"), 2*time.Second)
	if err != nil || !existing || ln2 != nil {
		t.Fatalf("ln=%v existing=%v err=%v", ln2, existing, err)
	}
}
