//go:build !windows

package tray

import (
	"context"
	"testing"
	"time"
)

func TestNoTrayBlocksUntilDone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	res := make(chan error, 1)
	go func() { res <- New(Controls{}).Run(ctx) }()
	select {
	case <-res:
		t.Fatal("Run returned before the context was done")
	case <-time.After(50 * time.Millisecond):
	}
	cancel()
	select {
	case err := <-res:
		if err != nil {
			t.Errorf("err = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}
