// Package collector defines what a conversation source must implement.
package collector

import "github.com/marioolf/cortex/internal/session"

type Collector interface {
	// ID is the stable source name used in config and note frontmatter.
	ID() string
	// Detect returns the directories where this source keeps data; empty means not installed.
	Detect() []string
	// Collect calls fn once per session found. Unreadable sessions are
	// reported through warn and skipped, never fatal.
	Collect(fn func(*session.Session), warn func(error))
}
