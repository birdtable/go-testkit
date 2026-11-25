//go:build !windows && !aix && !illumos && !solaris

package testkit

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// holdRead makes the FIFO in dir and holds it open for reading and writing, which takes no partner,
// so the open does not block. While it is open a reader's open does not block either, and its read
// waits for a writer's bytes, which never come. let puts an empty file at the name, so an open after
// it reads that and meets no FIFO, and then closes the one writer, which ends every read of the FIFO.
func holdRead(dir string) (string, func() error, error) {
	path := filepath.Join(dir, "hold")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		return "", nil, fmt.Errorf("making the FIFO: %w", err)
	}
	w, err := os.OpenFile(path, os.O_RDWR, 0) //nolint:gosec // G304: the FIFO this call made in the test's own folder
	if err != nil {
		return "", nil, fmt.Errorf("opening the FIFO: %w", err)
	}
	return path, func() error {
		empty := filepath.Join(dir, "empty")
		err := os.WriteFile(empty, nil, 0o600)
		if err == nil {
			err = os.Rename(empty, path)
		}
		return errors.Join(err, w.Close())
	}, nil
}
