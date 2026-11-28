package testkit

import (
	"errors"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"testing"
)

// Socket plants at path a file that is not a regular file, the same on every system, for a test of
// code that refuses one found in a folder: an AF_UNIX socket file. os.Lstat and os.Stat report it
// with fs.ModeSocket, and os.ReadDir its entry too, on Linux, macOS and Windows, where it is a reparse
// point (Windows 10 1803 and later). A read of it fails at its open, at once, never waiting: with
// ENXIO on Linux, EOPNOTSUPP on macOS and ERROR_CANT_ACCESS_FILE on Windows. It holds no listener:
// the socket is closed before Socket returns, and the file stays, so a connect to it is refused.
//
// The socket is made in a folder of its own under os.TempDir, whose short path fits the system's
// limit on a socket's name (104 bytes on macOS, 108 on Linux and Windows), and renamed to path. So
// path may be as long as the system allows a file, and is on the volume of os.TempDir, as t.TempDir
// is. t's cleanup removes it, so the folder holding it can be removed once t ends. Something already
// at path, a folder of path's that is missing, or a socket the system will not make fails t.
func Socket(t testing.TB, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("testkit: Socket: something is at %s already, or it cannot be read (%v)", path, err)
	}
	dir, err := os.MkdirTemp("", "")
	if err != nil {
		t.Fatalf("testkit: Socket: the folder to make the socket in: %v", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	made := filepath.Join(dir, "s")
	var lc net.ListenConfig
	l, err := lc.Listen(t.Context(), "unix", made)
	if err != nil {
		t.Fatalf("testkit: Socket: the system made no socket at %s: %v", made, err)
	}
	l.(*net.UnixListener).SetUnlinkOnClose(false)
	if err := l.Close(); err != nil {
		t.Fatalf("testkit: Socket: closing the socket at %s: %v", made, err)
	}
	if err := os.Rename(made, path); err != nil {
		t.Fatalf("testkit: Socket: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("testkit: Socket's cleanup: %v", err)
		}
	})
}
