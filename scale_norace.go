//go:build !race

package testkit

// raceFactor is 1 in a build without the race detector: Scale stretches a wait by the load alone.
const raceFactor = 1.0
