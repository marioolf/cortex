package panel

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestStateInstallDefaultsToUnsupported(t *testing.T) {
	en := setup(t)
	var s struct{ Install InstallState }
	if err := json.Unmarshal(en.do("GET", "/api/state", good, tok, "", "").Body.Bytes(), &s); err != nil {
		t.Fatal(err)
	}
	if s.Install.Supported {
		t.Errorf("install = %+v", s.Install)
	}
}

func TestStateInstallFromOptions(t *testing.T) {
	want := InstallState{Supported: true, RunningFrom: `C:\Users\Ana\Downloads`, Target: `C:\Users\Ana\AppData\Local\Programs\Cortex\cortex.exe`}
	en := setupWith(t, nil, func(o *Options) { o.InstallState = func() InstallState { return want } })
	var s struct{ Install InstallState }
	json.Unmarshal(en.do("GET", "/api/state", good, tok, "", "").Body.Bytes(), &s)
	if s.Install != want {
		t.Errorf("install = %+v, want %+v", s.Install, want)
	}
	if body := en.do("GET", "/api/state", good, tok, "", "").Body.String(); !strings.Contains(body, `"running_from"`) || !strings.Contains(body, `"installed":false`) {
		t.Errorf("field names: %s", body)
	}
}

func TestInstallRoute(t *testing.T) {
	var installs, quits atomic.Int32
	var fail error
	en := setupWith(t, nil, func(o *Options) {
		o.Install = func() error { installs.Add(1); return fail }
		o.Quit = func() { quits.Add(1) }
	})
	if got := en.do("POST", "/api/install", good, "", "application/json", `{}`).Code; got != 403 {
		t.Errorf("without token: %d", got)
	}
	if installs.Load() != 0 {
		t.Fatal("installed without a token")
	}
	if got := en.do("POST", "/api/install", good, tok, "text/plain", `{}`).Code; got != 415 {
		t.Errorf("wrong content type: %d", got)
	}

	fail = errors.New("cannot replace the file")
	w := en.do("POST", "/api/install", good, tok, "application/json", `{}`)
	if w.Code != 500 || !strings.Contains(w.Body.String(), "cannot replace the file") {
		t.Errorf("failure: %d %s", w.Code, w.Body)
	}
	time.Sleep(20 * time.Millisecond)
	if quits.Load() != 0 {
		t.Error("quit after a failed install")
	}

	fail = nil
	if w := en.do("POST", "/api/install", good, tok, "application/json", `{}`); w.Code != 200 {
		t.Errorf("success: %d %s", w.Code, w.Body)
	}
	for i := 0; i < 100 && quits.Load() == 0; i++ {
		time.Sleep(5 * time.Millisecond)
	}
	if quits.Load() != 1 || installs.Load() != 2 {
		t.Errorf("installs = %d, quits = %d", installs.Load(), quits.Load())
	}
}

func TestInstallRouteNotAvailable(t *testing.T) {
	en := setup(t)
	if got := en.do("POST", "/api/install", good, tok, "application/json", `{}`).Code; got != 501 {
		t.Errorf("got %d, want 501", got)
	}
}

func TestExtQuitNeedsPairingCode(t *testing.T) {
	var quits atomic.Int32
	en := setupWith(t, nil, func(o *Options) { o.Quit = func() { quits.Add(1) } })
	for name, c := range map[string]struct{ code, origin string }{
		"no code": {"", ""}, "wrong code": {"nope", ""}, "web origin": {pairing, "https://evil.example"},
	} {
		if w := en.ext("POST", "/api/ext/quit", c.code, c.origin, `{}`); w.Code != 403 {
			t.Errorf("%s: %d", name, w.Code)
		}
	}
	// The panel token is not what opens this route.
	if got := en.do("POST", "/api/ext/quit", good, tok, "application/json", `{}`).Code; got != 403 {
		t.Errorf("panel token: %d", got)
	}
	time.Sleep(20 * time.Millisecond)
	if quits.Load() != 0 {
		t.Fatal("quit without the code")
	}
	// The installer sends no Origin at all.
	if w := en.ext("POST", "/api/ext/quit", pairing, "", `{}`); w.Code != 200 {
		t.Errorf("right code: %d", w.Code)
	}
	for i := 0; i < 100 && quits.Load() == 0; i++ {
		time.Sleep(5 * time.Millisecond)
	}
	if quits.Load() != 1 {
		t.Errorf("quits = %d", quits.Load())
	}
}

// serve runs a panel on a free port the way run does and returns its URL.
// quit is what the panel's Quit does; it gets stop, which shuts the server down
// gracefully as run does, so a response in flight is still delivered.
func serve(t *testing.T, quit func(stop func())) (url string, stop func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	var srvp atomic.Pointer[http.Server]
	stop = func() {
		if s := srvp.Load(); s != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			s.Shutdown(ctx)
		}
	}
	en := setupWith(t, nil, func(o *Options) {
		o.Port = port
		o.Quit = func() { quit(stop) }
	})
	srv := &http.Server{Handler: en.h}
	srvp.Store(srv)
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return "http://127.0.0.1:" + strconv.Itoa(port) + "/", stop
}

func TestQuitRunning(t *testing.T) {
	url, _ := serve(t, func(stop func()) { stop() })
	if err := QuitRunning(url, pairing, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if Running(url, time.Second) {
		t.Error("still running")
	}
}

func TestQuitRunningWrongCode(t *testing.T) {
	url, _ := serve(t, func(func()) { t.Error("quit called") })
	err := QuitRunning(url, "wrong", time.Second)
	if err == nil || !strings.Contains(err.Error(), "did not accept") {
		t.Errorf("err = %v", err)
	}
}

func TestQuitRunningNothingThere(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	url := "http://" + ln.Addr().String() + "/"
	ln.Close()
	if err := QuitRunning(url, "x", time.Second); err != nil {
		t.Errorf("err = %v", err)
	}
}

func TestQuitRunningOtherProgram(t *testing.T) {
	// Something that does not carry our header is not Cortex: leave it alone.
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet { // the probe is a GET
			t.Errorf("request to a foreign program: %s %s", r.Method, r.URL)
		}
	})}
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	go srv.Serve(ln)
	defer srv.Close()
	_ = QuitRunning("http://"+ln.Addr().String()+"/", "x", time.Second)
}

func TestRunningExe(t *testing.T) {
	url, _ := serve(t, func(func()) {})
	exe, err := RunningExe(url, pairing, 2*time.Second)
	if err != nil || exe != `C:\Apps\Cortex\cortex.exe` {
		t.Errorf("exe = %q, err = %v", exe, err)
	}
	for _, code := range []string{"", "wrong"} {
		if exe, err := RunningExe(url, code, 2*time.Second); err == nil || exe != "" {
			t.Errorf("code %q: exe = %q, err = %v", code, exe, err)
		}
	}
	// An older Cortex answers without exe.
	old := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(Header, "1")
		w.Write([]byte(`{"ok":true,"version":"0.9"}`))
	}))
	defer old.Close()
	if _, err := RunningExe(old.URL+"/", pairing, 2*time.Second); err == nil {
		t.Error("an answer without exe was accepted")
	}
}
