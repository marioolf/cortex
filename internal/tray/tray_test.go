package tray

import (
	"context"
	"sync"
	"testing"
	"time"
)

type fakeItem struct {
	mu    sync.Mutex
	title string
	ch    chan struct{}
}

func newItem() *fakeItem                     { return &fakeItem{ch: make(chan struct{})} }
func (f *fakeItem) SetTitle(s string)        { f.mu.Lock(); f.title = s; f.mu.Unlock() }
func (f *fakeItem) Clicked() <-chan struct{} { return f.ch }
func (f *fakeItem) Title() string            { f.mu.Lock(); defer f.mu.Unlock(); return f.title }

func TestLabels(t *testing.T) {
	if s, b := labels(false); s != "Running" || b != "Pause" {
		t.Errorf("running: %q %q", s, b)
	}
	if s, b := labels(true); s != "Paused" || b != "Resume" {
		t.Errorf("paused: %q %q", s, b)
	}
}

type rig struct {
	open, status, toggle, quit *fakeItem
	poll                       chan time.Time
	mu                         sync.Mutex
	paused                     bool
	opened, quits              int
	done                       chan struct{}
	cancel                     context.CancelFunc
}

func (r *rig) get() bool  { r.mu.Lock(); defer r.mu.Unlock(); return r.paused }
func (r *rig) set(p bool) { r.mu.Lock(); r.paused = p; r.mu.Unlock() }

func start(t *testing.T, paused bool) *rig {
	t.Helper()
	r := &rig{open: newItem(), status: newItem(), toggle: newItem(), quit: newItem(),
		poll: make(chan time.Time), paused: paused, done: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	c := Controls{
		Open:      func() { r.mu.Lock(); r.opened++; r.mu.Unlock() },
		Paused:    r.get,
		SetPaused: r.set,
		Quit:      func() { r.mu.Lock(); r.quits++; r.mu.Unlock() },
	}
	go func() { drive(ctx, c, r.open, r.status, r.toggle, r.quit, r.poll); close(r.done) }()
	t.Cleanup(func() { cancel(); <-r.done })
	return r
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if ok() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestDriveClicks(t *testing.T) {
	r := start(t, false)
	eventually(t, "initial labels", func() bool { return r.status.Title() == "Running" && r.toggle.Title() == "Pause" })

	r.open.ch <- struct{}{}
	r.toggle.ch <- struct{}{}
	r.quit.ch <- struct{}{}
	// Unbuffered sends return when the loop took them, but the handler runs after.
	eventually(t, "handlers", func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.opened == 1 && r.quits == 1 && r.paused
	})
	eventually(t, "paused labels", func() bool { return r.status.Title() == "Paused" && r.toggle.Title() == "Resume" })

	r.toggle.ch <- struct{}{}
	eventually(t, "resumed", func() bool { return !r.get() && r.toggle.Title() == "Pause" })
}

func TestDriveFollowsPanel(t *testing.T) {
	// Paused from the panel, not from the tray: the next poll shows it.
	r := start(t, false)
	eventually(t, "initial", func() bool { return r.toggle.Title() == "Pause" })
	r.set(true)
	r.poll <- time.Now()
	eventually(t, "paused label", func() bool { return r.status.Title() == "Paused" && r.toggle.Title() == "Resume" })
	r.set(false)
	r.poll <- time.Now()
	eventually(t, "running label", func() bool { return r.status.Title() == "Running" })
}

func TestDriveStopsWithContext(t *testing.T) {
	r := start(t, true)
	r.cancel()
	select {
	case <-r.done:
	case <-time.After(2 * time.Second):
		t.Fatal("drive did not return")
	}
}
