package testkit

import (
	"bytes"
	"errors"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSocketPlantsAFileThatIsNotRegularWhoseReadFailsAndThatLeavesWithTheTest: on every system,
// Socket plants a file at a path longer than any system's limit on a socket's name, inside a
// folder, that os.Lstat, os.Stat and os.ReadDir report as a socket and not regular, and whose
// open and read fail at once. Once the test that planted it ends, the path is gone and the folder
// can be removed. The bound on the read is the test binary's timeout: a read that waited would
// hold the test until it.
//
// What it costs when this fails: a consumer's refusal of a file that is not regular in a folder
// goes untested where the plant is a regular file, a read of it hangs the consumer's test, or the
// folder holding it cannot be removed and fails the consumer's test on Windows.
func TestSocketPlantsAFileThatIsNotRegularWhoseReadFailsAndThatLeavesWithTheTest(t *testing.T) {
	t.Parallel()
	folder := filepath.Join(t.TempDir(), strings.Repeat("f", 60))
	if err := os.Mkdir(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(folder, "socket")
	if len(path) <= 108 {
		t.Fatalf("the path %s is %d bytes, within a socket name's limit, so it proves no rename", path, len(path))
	}
	// Registered before Socket's own, so it runs after it: cleanups run last first.
	t.Cleanup(func() {
		if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("the plant after its test ended: %v; want it gone", err)
		}
		if err := os.Remove(folder); err != nil {
			t.Errorf("removing the folder that held the plant: %v", err)
		}
	})
	Socket(t, path)
	for name, stat := range map[string]func(string) (fs.FileInfo, error){"Lstat": os.Lstat, "Stat": os.Stat} {
		fi, err := stat(path)
		if err != nil || fi.Mode().IsRegular() || fi.Mode().Type() != fs.ModeSocket {
			t.Errorf("os.%s of the plant: %v, %v; want a socket, not regular", name, modeOrNone(fi), err)
		}
	}
	ents, err := os.ReadDir(folder)
	if err != nil || len(ents) != 1 || ents[0].Type() != fs.ModeSocket {
		t.Errorf("os.ReadDir of the folder: %v, %v; want the one entry, a socket", ents, err)
	}
	if data, err := os.ReadFile(path); err == nil {
		t.Errorf("os.ReadFile of the plant read %d bytes and no error; want its open refused", len(data))
	}
}

// TestSocketFailsWhereSomethingIsAtThePathAndLeavesItAsItWas: Socket at a path that holds a file
// fails the test naming the path, and the file is left as it was.
//
// What it costs when this fails: a plant at a path the test had written replaces that file without
// a word, as a rename does, and the test reads a socket where it wrote its data.
func TestSocketFailsWhereSomethingIsAtThePathAndLeavesItAsItWas(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "taken")
	if err := os.WriteFile(path, []byte("kept"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := record(t, func(tb testing.TB) {
		tb.Helper()
		Socket(tb, path)
		tb.Error("Socket went on past a path that was taken")
	})
	if !strings.Contains(r.failed, "something is at "+path+" already") {
		t.Errorf("Socket at a taken path failed with %q; want it naming %s as taken", r.failed, path)
	}
	if data, err := os.ReadFile(path); err != nil || !bytes.Equal(data, []byte("kept")) {
		t.Errorf("the file at the taken path: %q, %v; want it kept as it was", data, err)
	}
}

// TestSocketHoldsNoListenerSoADialToItIsRefused: Socket holds no listener past the call, so a dial
// to the plant fails. The plant is in ShortDir's folder, so its path is short enough to dial on
// every system and the dial's failure is the closed socket, not the name's length.
//
// What it costs when this fails: a listener held open past the call answers a consumer's connect,
// and leaves a socket open after the test that a leak check counts.
func TestSocketHoldsNoListenerSoADialToItIsRefused(t *testing.T) {
	t.Parallel()
	path := filepath.Join(ShortDir(t), "s")
	Socket(t, path)
	var d net.Dialer
	c, err := d.DialContext(t.Context(), "unix", path)
	if err == nil {
		_ = c.Close()
		t.Errorf("a dial to the plant at %s connected; want it refused, as no listener is held", path)
	}
}

// TestSocketFailsWhereAFolderOfThePathIsMissing: Socket at a path whose folder is not there fails
// the test naming the path, and plants nothing.
//
// What it costs when this fails: a plant asked for in a folder the test never made passes the call,
// and the test goes on to check a file that is not there.
func TestSocketFailsWhereAFolderOfThePathIsMissing(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "missing", "socket")
	r := record(t, func(tb testing.TB) {
		tb.Helper()
		Socket(tb, path)
		tb.Error("Socket went on past a path whose folder is missing")
	})
	if !strings.Contains(r.failed, path) || strings.Contains(r.failed, "Socket went on") {
		t.Errorf("Socket at a path whose folder is missing failed with %q; want it naming %s", r.failed, path)
	}
	if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the path after the refusal: %v; want nothing there", err)
	}
}

// TestSocketsFolderRefusesARootTooLongForASocketsPathAndLeavesNothing: the folder Socket makes its
// socket in is refused where the socket's path in it would pass SocketPathMax, naming the limit, and
// the folder is removed.
//
// What it costs when this fails: a consumer whose TMPDIR is long gets the system's bind error, which
// names no limit, or a folder left behind in its temporary folder at each call.
func TestSocketsFolderRefusesARootTooLongForASocketsPathAndLeavesNothing(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), strings.Repeat("d", SocketPathMax))
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	dir, err := socketDir(root)
	if err == nil || !strings.Contains(err.Error(), "over SocketPathMax") {
		t.Errorf("socketDir of a root of %d bytes: %q, %v; want a refusal naming SocketPathMax", len(root), dir, err)
	}
	if ents, err := os.ReadDir(root); err != nil || len(ents) != 0 {
		t.Errorf("the root after the refusal: %v, %v; want it empty", ents, err)
	}
}

// modeOrNone is fi's mode, or none for no fi.
func modeOrNone(fi fs.FileInfo) any {
	if fi == nil {
		return "none"
	}
	return fi.Mode()
}
