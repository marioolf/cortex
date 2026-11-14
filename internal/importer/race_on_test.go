//go:build race

package importer

// The race detector makes everything several times slower; the time limits of
// the performance tests allow for it.
const raceSlowdown = 8
