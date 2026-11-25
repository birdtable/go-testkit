package contract_test

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// oldPath is the repository's name and module path before go-testkit, written in two parts so
// this file does not hold it.
const oldPath = "birdtable/" + "testkit"

// No file git tracks names the old path, so no import, go.mod, workflow, task or link still points at
// it. CHANGELOG.md and .changes/ keep it, as history: the notes say the path moved and v0.1.0 was
// tagged at the old one.
//
// What it costs when this fails: a library that follows a README, an issue form or an import to the old
// path pins v0.1.0, whose go.mod names a module go-testkit is not, or a gate reads a module path no
// go.mod declares.
func TestNoFileNamesTheOldPath(t *testing.T) {
	t.Parallel()
	for name := range strings.SplitSeq(strings.TrimSuffix(string(git(t, "", "ls-files", "-z")), "\x00"), "\x00") {
		if name == "CHANGELOG.md" || strings.HasPrefix(name, ".changes/") {
			continue
		}
		b := read(t, filepath.Join(root, filepath.FromSlash(name)))
		if bytes.IndexByte(b, 0) >= 0 || !bytes.Contains(b, []byte(oldPath)) {
			continue
		}
		for i, s := range textLines(b) {
			if strings.Contains(s, oldPath) {
				t.Errorf("%s:%d names %s, the old path; the module is github.com/birdtable/go-testkit: %s", name, i+1, oldPath, s)
			}
		}
	}
}
