//go:build !windows

package testkit

import "testing"

// TestStartTerminateSendsSIGTERM: off Windows, a test reads the program's readiness line from Stdout
// before Wait, sends Terminate, and the program ends by SIGTERM, not by an interrupt.
//
// What it costs when this fails: a test of a server's SIGTERM sends SIGINT, and passes on the
// handler it did not mean to test.
func TestStartTerminateSendsSIGTERM(t *testing.T) {
	t.Parallel()
	endsBy(t, (*Running).Terminate, "terminated", 8)
}
