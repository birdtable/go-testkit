//go:build !windows

package testkit

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Off Windows a test that left a file open fails by NoOpenFiles' own report.
const (
	leftOpenFails    = "is still open when its test ends"
	noOpenFilesReads = true
)

// stillOpen is how often a report says an entry is still open.
func stillOpen(report string) int { return strings.Count(report, " is still open when its test ends") }

// create makes the file at path and is it, open.
func create(t *testing.T, path string) *os.File {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// shut closes f.
func shut(t *testing.T, f *os.File) {
	t.Helper()
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// A file left open in a sub-folder is reported by its path, and so is a folder left open, each once
// and with the descriptor that holds it; once the file is closed the folder alone is reported, and
// once both are closed nothing is.
//
// What it costs when this fails: a test that leaks a handle passes on Linux and macOS, or one that
// closed everything fails.
func TestAFileOrAFolderLeftOpenUnderTheFolderIsReportedUntilItIsClosed(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	e := ends(t)
	NoOpenFiles(e, dir)
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	file := create(t, filepath.Join(sub, "left"))
	folder, err := os.Open(sub)
	if err != nil {
		t.Fatal(err)
	}
	got := e.atEnd()
	for _, held := range []*os.File{file, folder} {
		if want := held.Name() + " is still open when its test ends, held by descriptor "; !strings.Contains(got, want) {
			t.Errorf("with %s left open, the report does not say %q:\n%s", held.Name(), want, got)
		}
	}
	if stillOpen(got) != 2 {
		t.Errorf("a file and a folder were left open, and %d entries are reported:\n%s", stillOpen(got), got)
	}
	shut(t, file)
	if got = e.atEnd(); stillOpen(got) != 1 || !strings.Contains(got, sub+" is still open") {
		t.Errorf("with the file closed and the folder open, want the folder alone:\n%s", got)
	}
	shut(t, folder)
	if got = e.atEnd(); got != "" {
		t.Errorf("with both closed, want nothing:\n%s", got)
	}
}

// The folder itself, held open, is reported: its removal is what Windows would refuse.
//
// What it costs when this fails: a handle on the test's own folder is a leak only Windows finds.
func TestTheFolderItselfLeftOpenIsReported(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	e := ends(t)
	NoOpenFiles(e, dir)
	folder, err := os.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := e.atEnd()
	shut(t, folder)
	if stillOpen(got) != 1 || !strings.Contains(got, dir+" is still open") {
		t.Errorf("with the folder itself left open, want it alone:\n%s", got)
	}
}

// A file that was closed, a file removed while still open, a file open outside the folder and a pipe
// are none of them reported, each while a file left open beside them is: so the row measures a
// working check, not a silent one.
//
// What it costs when this fails: a test that leaked nothing under its folder fails, or the check is
// dead and this row cannot tell.
func TestWhatIsNotOpenUnderTheFolderIsNotReported(t *testing.T) {
	t.Parallel()
	dir, outside := t.TempDir(), t.TempDir()
	e := ends(t)
	NoOpenFiles(e, dir)
	shut(t, create(t, filepath.Join(dir, "closed")))
	removed := create(t, filepath.Join(dir, "removed"))
	defer shut(t, removed)
	if err := os.Remove(removed.Name()); err != nil {
		t.Fatal(err)
	}
	elsewhere := create(t, filepath.Join(outside, "elsewhere"))
	defer shut(t, elsewhere)
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer shut(t, r)
	defer shut(t, w)
	if got := e.atEnd(); got != "" {
		t.Errorf("nothing is open under the folder, and the report is:\n%s", got)
	}
	left := create(t, filepath.Join(dir, "left"))
	defer shut(t, left)
	if got := e.atEnd(); stillOpen(got) != 1 || !strings.Contains(got, left.Name()+" is still open") {
		t.Errorf("with one file left open beside them, want it alone:\n%s", got)
	}
}

// A leak is reported under the path the caller gave when that path goes through a link: the default
// t.TempDir(), which on macOS is under /var, a link; a folder reached by a link in the middle of its
// path; and a folder that is itself a link. The report names the caller's spelling, never the
// resolved one.
//
// What it costs when this fails: on macOS every leak under a t.TempDir() is missed, as a compare of
// path prefixes misses it, or a folder given by a link is never walked.
func TestALeakIsReportedWhenTheFoldersPathGoesThroughALink(t *testing.T) {
	t.Parallel()
	real := t.TempDir()
	if err := os.Mkdir(filepath.Join(real, "inner"), 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	Link(t, real, link)
	for name, dir := range map[string]string{
		"the default TempDir":      t.TempDir(),
		"a link in the path":       filepath.Join(link, "inner"),
		"the folder itself a link": link,
	} {
		e := ends(t)
		NoOpenFiles(e, dir)
		left := create(t, filepath.Join(dir, "left"))
		got := e.atEnd()
		shut(t, left)
		if stillOpen(got) != 1 || !strings.Contains(got, left.Name()+" is still open") {
			t.Errorf("%s (%s): want its file alone:\n%s", name, dir, got)
		}
		if err := os.Remove(left.Name()); err != nil {
			t.Fatal(err)
		}
	}
}

// Of 20 tests at once, each with its own folder and a file open in it at the same moment, the one
// that does not close its file is the one that is failed, and for its own file alone.
//
// What it costs when this fails: a leak in one parallel test fails its neighbours, and nobody can
// tell whose handle it was.
func TestOfTwentyTestsAtOnceOnlyTheLeakerIsFailedAndForItsOwnFile(t *testing.T) {
	t.Parallel()
	const tests, leaker = 20, 7
	var opened, checked sync.WaitGroup
	allOpen := make(chan struct{})
	reports, files := make([]string, tests), make([]*os.File, tests)
	for i := range tests {
		dir := t.TempDir()
		e := ends(t)
		NoOpenFiles(e, dir)
		files[i] = create(t, filepath.Join(dir, "file"))
		opened.Add(1)
		checked.Go(func() {
			opened.Done()
			<-allOpen
			if i != leaker {
				if err := files[i].Close(); err != nil {
					e.Errorf("closing %s: %v", files[i].Name(), err)
				}
			}
			reports[i] = e.atEnd()
		})
	}
	opened.Wait()
	close(allOpen)
	checked.Wait()
	shut(t, files[leaker])
	for i, got := range reports {
		switch {
		case i != leaker && got != "":
			t.Errorf("test %d closed its file and was failed:\n%s", i, got)
		case i == leaker && (stillOpen(got) != 1 || !strings.Contains(got, files[i].Name()+" is still open")):
			t.Errorf("the leaker, test %d: want its own file alone:\n%s", i, got)
		}
	}
}

// A folder that is gone when the test ends reports nothing; one that cannot be walked fails the test
// with the error, and so does a descriptor list that cannot be read: a check that could not run is
// never a pass.
//
// What it costs when this fails: a test that removed its own folder fails, or a check that read
// nothing passes a leak.
func TestAFolderThatIsGoneReportsNothingAndOneThatCannotBeReadFails(t *testing.T) {
	t.Parallel()
	gone := filepath.Join(t.TempDir(), "gone")
	e := ends(t)
	NoOpenFiles(e, gone)
	if got := e.atEnd(); got != "" {
		t.Errorf("a folder that is gone: want nothing:\n%s", got)
	}
	file := create(t, filepath.Join(t.TempDir(), "file"))
	shut(t, file)
	e = ends(t)
	NoOpenFiles(e, filepath.Join(file.Name(), "under-a-file"))
	if got := e.atEnd(); !strings.Contains(got, "could not be walked") || stillOpen(got) != 0 {
		t.Errorf("a folder under a plain file: want the walk's error:\n%s", got)
	}
	if held, err := heldOpen(filepath.Join(t.TempDir(), "no-list"), t.TempDir()); err == nil || held != nil || !strings.Contains(err.Error(), "could not be listed") {
		t.Errorf("a descriptor list that is not there: %v, %v; want the listing's error and nothing held", held, err)
	}
}
