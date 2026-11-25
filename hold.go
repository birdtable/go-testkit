package testkit

import (
	"errors"
	"sync"
	"testing"
)

// NoHold is the one message of every test HoldRead skips: Go's syscall package makes no FIFO there.
const NoHold = "not measured: Go's syscall package has no Mkfifo on aix, illumos and solaris, and this test needs a FIFO to hold a read on"

// HoldRead is a path one reader, a test's child say, opens and reads, whose read waits until the test
// calls release. It is a FIFO of mode 0600 in a folder of t's own on Unix, and on
// Windows a pipe in the system's pipe namespace (\\.\pipe\...), in no folder, that this account alone
// may open, once, from this machine. An open before release does not block, and its read waits;
// release ends that read with nothing read, as an empty file's. An open after release does not block
// either: on Unix it reads an empty file, and on Windows it finds no pipe. So a test releases once its
// reader has the path open. A hold is for one reader: on Windows a second open is refused, and on
// Unix a second reader's read may outlast release, as macOS wakes only some of a FIFO's readers when
// its last writer closes. release may be called from any goroutine and more than once, and t's
// cleanup calls it, so the reader waits no longer than t. On aix, illumos and solaris, where Go's
// syscall package has no Mkfifo, it skips t with NoHold. Any other hold that cannot be made fails t; a
// release that fails is an error on t. On Unix the path is in t.TempDir as it is given, not resolved:
// on macOS under /var, which filepath.EvalSymlinks reads as /private/var, so a test that compares it
// with a path its reader reports resolves it first.
func HoldRead(t testing.TB) (path string, release func()) {
	t.Helper()
	return holdWith(t, holdRead)
}

// holdWith is HoldRead with mk, the system's holdRead, making the hold in a folder of t's own.
func holdWith(t testing.TB, mk func(dir string) (string, func() error, error)) (string, func()) {
	t.Helper()
	path, let, err := mk(t.TempDir())
	switch {
	case errors.Is(err, errors.ErrUnsupported):
		t.Skip(NoHold)
	case err != nil:
		t.Fatalf("testkit: HoldRead: %v", err)
	}
	var once sync.Once
	release := func() {
		once.Do(func() {
			if err := let(); err != nil {
				t.Errorf("testkit: HoldRead's release of %s: %v", path, err)
			}
		})
	}
	t.Cleanup(release)
	return path, release
}
