//go:build !windows

package tray

import "context"

type none struct{}

// New returns a Tray that shows nothing: only Windows has one so far.
func New(Controls) Tray { return none{} }

func (none) Run(ctx context.Context) error {
	<-ctx.Done()
	return nil
}
