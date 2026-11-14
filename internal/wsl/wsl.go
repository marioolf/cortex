// Package wsl finds the home directories of running WSL distros so that a
// Cortex running on Windows can read what Linux-side tools saved there.
package wsl

import (
	"strings"
	"sync"
	"time"
	"unicode/utf16"
)

// Homes returns the home directories of running distros as UNC paths, such as
// \\wsl.localhost\Ubuntu\home\alice. It returns nil when there are none, and
// always on systems other than Windows.
func Homes() []string { return homes(running.get()) }

// Roots returns the root of each running distro as a UNC path, such as
// \\wsl.localhost\Ubuntu. Same caveats as Homes.
func Roots() []string {
	var roots []string
	for _, d := range running.get() {
		roots = append(roots, rootOf(d))
	}
	return roots
}

func rootOf(distro string) string { return `\\wsl.localhost\` + distro }

// cacheTTL is how long the list of running distros is reused: Homes and Roots
// are called several times per sync round and each lookup spawns wsl.exe.
const cacheTTL = 20 * time.Second

// distroCache remembers the result of fetch for ttl. The clock and fetch are
// fields so tests need neither wsl.exe nor a sleep.
type distroCache struct {
	ttl   time.Duration
	now   func() time.Time
	fetch func() []string

	mu    sync.Mutex
	at    time.Time
	names []string
	have  bool
}

var running = &distroCache{ttl: cacheTTL, now: time.Now, fetch: listDistros}

func (c *distroCache) get() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.have || c.now().Sub(c.at) >= c.ttl {
		c.names, c.at, c.have = c.fetch(), c.now(), true
	}
	return c.names
}

// parseDistros decodes the output of `wsl.exe -l -q --running`. wsl.exe writes
// UTF-16LE, usually with a byte order mark, and CRLF line endings.
func parseDistros(out []byte) []string {
	if len(out)%2 == 1 {
		out = out[:len(out)-1]
	}
	u := make([]uint16, 0, len(out)/2)
	for i := 0; i < len(out); i += 2 {
		u = append(u, uint16(out[i])|uint16(out[i+1])<<8)
	}
	text := strings.TrimPrefix(string(utf16.Decode(u)), "\ufeff")
	var names []string
	for _, line := range strings.Split(text, "\n") {
		if name := strings.TrimSpace(strings.ReplaceAll(line, "\x00", "")); name != "" {
			names = append(names, name)
		}
	}
	return names
}

// homeGlob is the pattern for the home directories of one distro.
func homeGlob(distro string) string {
	return `\\wsl.localhost\` + distro + `\home\*`
}
