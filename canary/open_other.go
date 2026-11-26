//go:build !windows

package canary

import (
	"os"
	"syscall"
)

// openFlags opens a file Files reads without waiting: an entry the walk saw as a regular file and that
// is a FIFO by the open, swapped in between, opens at once in place of waiting for a writer.
const openFlags = os.O_RDONLY | syscall.O_NONBLOCK
