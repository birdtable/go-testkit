//go:build race

package testkit

// raceFactor is how much Scale stretches a wait under the race detector: ten, from its documented
// cost of two to twenty times the time a program takes (go.dev/doc/articles/race_detector), and the
// caller's limit holds it.
const raceFactor = 10.0
