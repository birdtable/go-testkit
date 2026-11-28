package testkit

import (
	"errors"
	"fmt"
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
// The socket is made in a folder of its own under os.TempDir and renamed to path. So path may be as
// long as the system allows a file, and is on the volume of os.TempDir, as t.TempDir is. The socket's
// path in that folder may not pass SocketPathMax: where os.TempDir is too long for it, t fails naming
// the limit, before any listen. t's cleanup removes the plant, so the folder holding it can be
// removed once t ends. Something already at path, a folder of path's that is missing, or a socket
// the system will not make fails t.
func Socket(t testing.TB, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("testkit: Socket: something is at %s already, or it cannot be read (%v)", path, err)
	}
	dir, err := socketDir(os.TempDir())
	if err != nil {
		t.Fatalf("testkit: Socket: the folder to make the socket in: %v", err)
	}
	defer func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Errorf("testkit: Socket: removing the folder it made the socket in: %v", err)
		}
	}()
	made := filepath.Join(dir, "s")
	var lc net.ListenConfig
	l, err := lc.Listen(t.Context(), "unix", made)
	if err != nil {
		t.Fatalf("testkit: Socket: the system made no socket at %s: %v", made, err)
	}
	if ul, ok := l.(*net.UnixListener); ok {
		ul.SetUnlinkOnClose(false)
	}
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

// socketDir makes a new folder in root for Socket's socket, named "s" in it, and refuses one where
// that path would pass SocketPathMax, removing the folder, so the refusal names the limit and not
// the system's bind error.
func socketDir(root string) (string, error) {
	dir, err := os.MkdirTemp(root, "")
	if err != nil {
		return "", err
	}
	if made := filepath.Join(dir, "s"); len(made) > SocketPathMax {
		return "", errors.Join(fmt.Errorf("the socket's path %s would be %d bytes, over SocketPathMax (%d): give the system's temporary folder (TMPDIR, or TMP on Windows) a shorter path", made, len(made), SocketPathMax), os.Remove(dir))
	}
	return dir, nil
}
