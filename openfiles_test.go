package testkit

import (
	"os"
	"slices"
	"strings"
	"testing"
)

// ending is a recorder that keeps the cleanups registered on it, so a row runs them when it chooses
// and reads what they reported: NoOpenFiles reports at the end of its test, which a row cannot wait
// for.
type ending struct {
	*recorder
	cleanups []func()
}

// ends is an ending of t.
func ends(t *testing.T) *ending {
	t.Helper()
	return &ending{recorder: &recorder{TB: t}}
}

func (e *ending) Cleanup(f func()) { e.cleanups = append(e.cleanups, f) }

// atEnd runs the cleanups, last registered first as the end of a test does, and is what they
// reported. The cleanups are kept, so a row asks again after it closed something.
func (e *ending) atEnd() string {
	e.failed = ""
	for _, f := range slices.Backward(e.cleanups) {
		f()
	}
	return e.failed
}

// leftOpenName is the file the helper leaves open under its TempDir.
const leftOpenName = "left-open"

// A test that leaves a file open under its t.TempDir() and calls NoOpenFiles fails on every system:
// on Linux and macOS by NoOpenFiles' own report, which names the file, and on Windows by testing's
// removal of the folder, which the system refuses, with nothing reported by NoOpenFiles. The test is
// the helper's, run as a child, so its failure is read and is not this run's. It is a case seen in
// a consumer's tests, cut down to the file left open.
//
// What it costs when this fails: a leaked handle fails a test on Windows alone, so a pull request
// that is run on Linux or macOS lands the leak, and the next Windows run meets it.
func TestAFileLeftOpenUnderItsTempDirFailsTheTestOnEverySystem(t *testing.T) {
	t.Parallel()
	code, out, err := Rerun(bounded(t), testCap, "leave a file open")
	if err != nil || code == 0 {
		t.Fatalf("the child that left a file open: code %d, %v; want a failed test\n%s", code, err, out)
	}
	if !strings.Contains(string(out), leftOpenFails) {
		t.Errorf("the child failed, but not by %q:\n%s", leftOpenFails, out)
	}
	if said := strings.Contains(string(out), "NoOpenFiles"); said != noOpenFilesReads {
		t.Errorf("NoOpenFiles reported in the child: %v, want %v on this system\n%s", said, noOpenFilesReads, out)
	}
	if noOpenFilesReads && !strings.Contains(string(out), leftOpenName+" is still open") {
		t.Errorf("the report does not name the file %s:\n%s", leftOpenName, out)
	}
}

// leftOpen is the file the helper leaves open, kept here so no collection closes it before the test
// ends.
var leftOpen *os.File //nolint:gochecknoglobals // the helper's file left open, held for the child's life so no collection closes it
