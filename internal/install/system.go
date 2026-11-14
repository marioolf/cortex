package install

import (
	"errors"

	"github.com/marioolf/cortex/internal/autostart"
)

var errUnsupported = errors.New("not supported on this operating system")

type system struct{ quit func() error }

// NewSystem returns the real System. quit asks a running Cortex to stop (it
// needs the panel client, which this package does not import).
func NewSystem(quit func() error) System { return system{quit} }

func (s system) QuitRunning() error             { return s.quit() }
func (system) EnableAutostart(exe string) error { return autostart.EnableFor(exe) }
func (system) DisableAutostart() error          { return autostart.Disable() }
