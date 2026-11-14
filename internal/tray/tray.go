// Package tray puts a notification-area icon on Windows so the user can see that
// Cortex runs without a console and reach it. The icon library is imported only
// by the Windows file; every other platform gets a Tray that does nothing, so
// those builds stay free of it (and of cgo).
package tray

import (
	"context"
	"time"
)

// Controls is what the tray needs from Cortex. Everything is a function so the
// package knows nothing about the engine or the panel.
type Controls struct {
	Open      func()            // show the control panel
	Paused    func() bool       // whether capturing is paused
	SetPaused func(paused bool) // pause or resume
	Quit      func()            // stop Cortex
}

// Tray shows the icon until ctx is done.
type Tray interface {
	// Run blocks until ctx is done or the icon went away. It returns an error
	// only when the icon could not be shown; the caller then goes on without it.
	Run(ctx context.Context) error
}

// pollEvery is how often the menu looks at the paused state, which the panel
// can change too. A subscription would need a hook in the engine for a label.
const pollEvery = 2 * time.Second

const (
	labelRunning = "Running"
	labelPaused  = "Paused"
	labelPause   = "Pause"
	labelResume  = "Resume"
)

// item is the part of a menu item the loop uses, so the loop can be tested
// without the library.
type item interface {
	SetTitle(string)
	Clicked() <-chan struct{}
}

// labels returns the status line and the pause item's title for a state.
func labels(paused bool) (status, toggle string) {
	if paused {
		return labelPaused, labelResume
	}
	return labelRunning, labelPause
}

// drive reacts to clicks and keeps the labels in step with the engine until
// ctx is done. poll delivers the times to look at the state again.
func drive(ctx context.Context, c Controls, open, status, toggle, quit item, poll <-chan time.Time) {
	var shown, have bool
	refresh := func() {
		p := c.Paused()
		if have && p == shown {
			return // setting a title rebuilds the menu on Windows; do it only on change
		}
		shown, have = p, true
		s, t := labels(p)
		status.SetTitle(s)
		toggle.SetTitle(t)
	}
	refresh()
	for {
		select {
		case <-ctx.Done():
			return
		case <-open.Clicked():
			c.Open()
		case <-toggle.Clicked():
			c.SetPaused(!c.Paused())
			refresh()
		case <-quit.Clicked():
			c.Quit()
		case <-poll:
			refresh()
		}
	}
}
