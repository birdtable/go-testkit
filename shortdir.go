package testkit

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// SocketPathMax is the most bytes a Unix socket's path may hold on this system: its sun_path, less
// the NUL that ends it, as syscall has the system's own headers write it. It is 107 on
// Linux and on Windows, whose AF_UNIX takes 108, and 103 on macOS and the BSDs, which take 104. A
// longer path is refused at the listen or the dial.
const SocketPathMax = len(syscall.RawSockaddrUnix{}.Path) - 1

// shortDirRoom is the fewest bytes ShortDir's folder leaves under it, its separator apart, for the
// path of a socket in it.
const shortDirRoom = 40

// shortDirName is the length of ShortDir's folder's name: "t" and 8 characters of crypto/rand's
// text, 40 bits, so every folder's path from one root is the same length.
const shortDirName = 9

// maxRootLen is the most bytes a root may have for ShortDir to use it: a socket's path in the folder
// is the root, two separators, the name and up to shortDirRoom bytes, and may not pass SocketPathMax.
// It is worked out from the constants, not written as a number.
const maxRootLen = SocketPathMax - shortDirName - shortDirRoom - 2

// ShortDir is a new folder, of mode 0700 on Unix, short enough for a socket's path in it on every
// system, removed with t. t.TempDir is not: it holds the test's name, and on macOS
// its root is long. The folder is in /tmp on Unix, as made, its link on macOS not resolved (so
// filepath.EvalSymlinks and os.Getwd read it under /private/tmp, and a test that compares the two
// resolves its own first), and in
// os.TempDir on Windows, the account's own, so its path is the root's and 10 bytes more, the same
// length for every call. It leaves at least 40 bytes under it, and a socket's path in it may be up
// to SocketPathMax. A root over maxRootLen bytes, or that is not an absolute folder, fails t with no
// fallback to a shared root; the failure names the most bytes a root may have here and how to give
// the host a shorter one. ShortDir takes the root's word and checks no access list on it, so the
// root, and only the root, must be a folder another account cannot open. On Windows the folder may
// be named by the root's 8.3 short form, so its path does not compare equal as a string with the long
// form of the same folder. A folder that cannot be made fails t too; a removal that fails is an error
// on t.
func ShortDir(t testing.TB) string {
	t.Helper()
	dir, err := shortDir(shortRoot())
	if err != nil {
		t.Fatalf("testkit: ShortDir: %v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Errorf("testkit: ShortDir's removal of %s: %v", dir, err)
		}
	})
	return dir
}

// shortDir makes a folder of a new name of shortDirName bytes in root and gives its path. It
// refuses a root that is not absolute, or whose folder would leave fewer than shortDirRoom bytes
// under it, before it makes anything; where the root was picked by shortening a deeper one, shortErr
// carries that short call's own error, and a refusal over the budget names it. A name taken already,
// by anyone, is tried again with another, up to 20 times, so the folder is always one this call made.
func shortDir(root string, shortErr error) (string, error) {
	root = filepath.Clean(root)
	if !filepath.IsAbs(root) {
		return "", fmt.Errorf("the root %q is not an absolute path", root)
	}
	if len(root) > maxRootLen {
		return "", fmt.Errorf("the root %s is %d bytes, over the %d a root may have here before a socket's path in ShortDir's folder would pass SocketPathMax (%d): %s", root, len(root), maxRootLen, SocketPathMax, shortDirRemedy(shortErr))
	}
	var err error
	for range 20 {
		dir := filepath.Join(root, "t"+rand.Text()[:shortDirName-1])
		if err = os.Mkdir(dir, 0o700); err == nil {
			return dir, nil
		}
		if !errors.Is(err, os.ErrExist) {
			break
		}
	}
	return "", err
}
