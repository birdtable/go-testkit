//go:build !windows

package testkit

import "os"

// endedTheSystemsWay reports whether a child of RerunInterrupted with code and err was ended by the
// interrupt it did not catch: off Windows a death by SIGINT, which Signal reads, with code -1.
func endedTheSystemsWay(code int, err error) bool {
	sig, ok := Signal(err)
	return ok && sig == os.Interrupt && code == -1
}
