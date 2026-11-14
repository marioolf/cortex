//go:build windows

package tray

import (
	"context"
	_ "embed"
	"errors"
	"runtime"
	"time"

	"fyne.io/systray"
)

// cortex.ico holds the 16, 32 and 48 px images of extension/icons/icon.svg
// (teal rounded square, white open ring and dot). It was written by a small
// throwaway program that draws that SVG's shapes at each size; it is not part of
// the build, so regenerate it by hand if the mark changes.
//
//go:embed cortex.ico
var icon []byte

// startTimeout bounds the wait for the icon: the library reports a failure to
// create its window only in the log and then waits forever.
const startTimeout = 10 * time.Second

type winTray struct{ c Controls }

// New returns the Windows Tray.
func New(c Controls) Tray { return winTray{c} }

// sysItem adapts a library menu item to item (SetTitle comes from the embedded one).
type sysItem struct{ *systray.MenuItem }

func (i sysItem) Clicked() <-chan struct{} { return i.ClickedCh }

func (t winTray) Run(ctx context.Context) error {
	ready, done := make(chan struct{}), make(chan struct{})
	dctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// The library creates its window and runs the message loop in this one
	// call, and Windows delivers a window's messages to the thread that
	// created it, so the goroutine must stay on one OS thread.
	go func() {
		runtime.LockOSThread()
		defer close(done)
		systray.Run(func() {
			systray.SetIcon(icon)
			systray.SetTooltip("Cortex")
			open := sysItem{systray.AddMenuItem("Open Cortex", "Open the control panel")}
			systray.AddSeparator()
			status := sysItem{systray.AddMenuItem(labelRunning, "")}
			status.Disable()
			toggle := sysItem{systray.AddMenuItem(labelPause, "Pause or resume capturing")}
			systray.AddSeparator()
			quit := sysItem{systray.AddMenuItem("Quit", "Stop Cortex")}
			systray.SetOnTapped(t.c.Open) // left click opens the panel; right click shows the menu
			close(ready)
			drive(dctx, t.c, open, status, toggle, quit, time.Tick(pollEvery))
		}, cancel)
	}()

	select {
	case <-ready:
	case <-done:
		return errors.New("the notification-area icon could not be created")
	case <-time.After(startTimeout):
		return errors.New("the notification-area icon did not appear")
	case <-ctx.Done():
		systray.Quit()
		return nil
	}
	select {
	case <-ctx.Done():
		systray.Quit()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
	case <-done: // the icon went away on its own (the session is ending)
	}
	return nil
}
