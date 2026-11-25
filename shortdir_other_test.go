//go:build !windows

package testkit

import (
	"os"
	"path/filepath"
	"testing"
)

// TestShortDirIsThisAccountsAlone: off Windows the folder, in the shared /tmp, is a folder of mode
// 0700, not a link, so another account neither reads nor plants a socket in it.
//
// What it costs when this fails: another account on the machine reads, replaces or listens in
// place of a test's socket.
func TestShortDirIsThisAccountsAlone(t *testing.T) {
	t.Parallel()
	dir := ShortDir(t)
	fi, err := os.Lstat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !fi.IsDir() || fi.Mode().Perm() != 0o700 {
		t.Fatalf("ShortDir %s is %v, want a folder of mode 0700", dir, fi.Mode())
	}
}

// rootBranch names ShortDir's root branch off Windows, where /tmp is the root and there is no 8.3
// short form: "long" unless /tmp itself is over the budget, which it is not.
func rootBranch() string {
	sr, _ := shortRoot()
	if len(filepath.Clean(sr)) > maxRootLen {
		return "deep"
	}
	return "long"
}
