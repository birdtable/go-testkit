//go:build aix || illumos || solaris

package testkit

import (
	"errors"
	"fmt"
)

// holdRead makes no hold: these systems have no syscall.Mkfifo, so HoldRead skips its test with NoHold.
func holdRead(string) (string, func() error, error) {
	return "", nil, fmt.Errorf("no syscall.Mkfifo on this system: %w", errors.ErrUnsupported)
}
