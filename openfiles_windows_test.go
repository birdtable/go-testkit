package testkit

import (
	"os"
	"path/filepath"
	"testing"
)

// On Windows a test that left a file open fails by testing's removal of its TempDir, and NoOpenFiles
// reads nothing.
const (
	leftOpenFails    = "TempDir RemoveAll cleanup"
	noOpenFilesReads = false
)

// On Windows NoOpenFiles reports nothing and fails nothing, with a file left open under its folder:
// the removal of the folder is the check there.
//
// What it costs when this fails: a Windows run reports a leak twice, or NoOpenFiles reads handles
// there by a way nobody proved.
func TestNoOpenFilesReadsNothingOnWindows(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	e := ends(t)
	NoOpenFiles(e, dir)
	f, err := os.Create(filepath.Join(dir, leftOpenName))
	if err != nil {
		t.Fatal(err)
	}
	got := e.atEnd()
	if err := f.Close(); err != nil { // closed here, so this test's own folder is removed
		t.Fatal(err)
	}
	if got != "" {
		t.Errorf("NoOpenFiles reported on Windows, where it reads nothing: %s", got)
	}
}
