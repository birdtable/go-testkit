package testkit

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// NoModes is the one message of every test skipped for mode bits: the system keeps no POSIX mode
// bits on a file, as Windows keeps only a read-only flag, so the test is not measured there and says so.
const NoModes = "not measured: this system keeps no POSIX mode bits on a file (on Windows, only a read-only flag), and this test reads them"

// probeMode is the mode KeepsModes sets and reads back: owner read and write, group read, so a system
// that keeps only a read-only flag, or folds the group's bits into the owner's, gives another.
const probeMode fs.FileMode = 0o640

// Modes skips t with NoModes where KeepsModes is false. A test whose every case reads a file's mode
// calls it before its subtests, since a parent whose subtests all skip is reported as passed.
func Modes(t testing.TB) {
	t.Helper()
	if !KeepsModes(t) {
		t.Skip(NoModes)
	}
}

// KeepsModes is whether the system keeps POSIX mode bits on a file: a file of the test's own, set to
// 0640, reads back as that mode. It acts on nothing, so a test that reads a mode in only some of its
// assertions reads it where this is true and checks the rest on every system. It asks the system,
// not its name, as Links does for a link. A probe that cannot make, set or read its file fails t:
// that is no answer about mode bits.
func KeepsModes(t testing.TB) bool {
	t.Helper()
	name := filepath.Join(t.TempDir(), "mode")
	if err := os.WriteFile(name, nil, 0o600); err != nil {
		t.Fatalf("the mode probe could not make its file: %v", err)
	}
	if err := os.Chmod(name, probeMode); err != nil {
		t.Fatalf("the mode probe could not set its file's mode: %v", err)
	}
	fi, err := os.Stat(name)
	if err != nil {
		t.Fatalf("the mode probe could not read its file's mode: %v", err)
	}
	return fi.Mode().Perm() == probeMode
}
