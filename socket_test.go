package testkit

import (
	"bytes"
	"errors"
	"io/fs"
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
	t.Run("planted", func(t *testing.T) {
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
	})
	if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the plant after its test ended: %v; want it gone", err)
	}
	if err := os.Remove(folder); err != nil {
		t.Errorf("removing the folder that held the plant: %v", err)
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

// modeOrNone is fi's mode, or none for no fi.
func modeOrNone(fi fs.FileInfo) any {
	if fi == nil {
		return "none"
	}
	return fi.Mode()
}
