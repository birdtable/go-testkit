//go:build !windows && !aix && !illumos && !solaris

package testkit

import (
	"os"
	"testing"
)

// TestHoldReadIsThisAccountsAlone: off Windows the hold is a FIFO only its owner may open, in a
// folder of the test's own.
//
// What it costs when this fails: another account on the machine opens the hold, reads what a test
// writes there or holds the test's child.
func TestHoldReadIsThisAccountsAlone(t *testing.T) {
	t.Parallel()
	path, _ := HoldRead(t)
	fi, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Type() != os.ModeNamedPipe || fi.Mode().Perm() != 0o600 {
		t.Fatalf("the hold %s is %v, want a FIFO of mode 0600", path, fi.Mode())
	}
}
