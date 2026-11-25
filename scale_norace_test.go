//go:build !race

package testkit

// wantRace is the race factor a build without the race detector has.
const wantRace = 1.0
