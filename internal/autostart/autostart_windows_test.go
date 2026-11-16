//go:build windows

package autostart

import (
	"crypto/rand"
	"encoding/hex"
	"testing"

	"golang.org/x/sys/windows/registry"
)

// Runs against a throwaway key under HKCU, never the real Run key.
func TestRegistryRoundTrip(t *testing.T) {
	var b [6]byte
	rand.Read(b[:])
	key := `Software\CortexTest-` + hex.EncodeToString(b[:])
	t.Cleanup(func() { registry.DeleteKey(registry.CURRENT_USER, key) })

	const exe = `C:\Users\José\AppData\Local\Programs\Cortex\cortex.exe`
	if enabledAt(key, exe) {
		t.Fatal("enabled before Enable")
	}
	if err := disableAt(key); err != nil { // nothing there: not an error
		t.Fatal(err)
	}
	if err := enableAt(key, exe); err != nil {
		t.Fatal(err)
	}
	if !enabledAt(key, exe) || !enabledAt(key, `c:\users\josÉ\appdata\local\programs\cortex\cortex.exe`) {
		t.Error("not enabled after Enable")
	}
	if enabledAt(key, `C:\Downloads\cortex.exe`) {
		t.Error("another program counts as enabled")
	}

	k, err := registry.OpenKey(registry.CURRENT_USER, key, registry.QUERY_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	got, typ, err := k.GetStringValue(valueName)
	k.Close()
	if err != nil || typ != registry.SZ || got != `"`+exe+`" run -no-browser` {
		t.Errorf("stored %q type %d err %v", got, typ, err)
	}

	if err := disableAt(key); err != nil {
		t.Fatal(err)
	}
	if enabledAt(key, exe) {
		t.Error("enabled after Disable")
	}
	if err := disableAt(key); err != nil { // twice is fine
		t.Fatal(err)
	}
}
