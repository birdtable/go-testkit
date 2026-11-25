package testkit

import "testing"

// NoOpenFiles fails t, when the test ends, for each file or folder under dir that this process still
// holds open, dir itself among them. Call it right after the folder is made: cleanups run last
// registered first, so a call made after t.TempDir() runs before that folder's removal, and after
// every close the test deferred or registered later.
//
// On Linux and macOS it lists this process's descriptors and reports each entry under dir whose
// device and inode a descriptor holds, by its path and the descriptor's number, so a path through a
// link is matched as surely as a plain one. On Windows it reads nothing: there the removal of the
// folder is the check, since Windows refuses to remove a file a handle holds and testing fails a test
// whose TempDir cannot be removed. So for a t.TempDir folder a leak fails the test on all three.
//
// It does not see, on any system, a file removed while still open, a descriptor a child process
// holds, or a handle of another process. A dir that cannot be walked when the test ends fails t with
// the error, as does a descriptor list that cannot be read; a dir that is no longer there reports
// nothing.
func NoOpenFiles(t testing.TB, dir string) {
	t.Helper()
	t.Cleanup(func() {
		t.Helper()
		reportOpen(t, dir)
	})
}
