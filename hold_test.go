package testkit

import (
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"slices"
	"testing"
)

// read is what a reader of a hold got: its bytes and its error.
type read struct {
	b   []byte
	err error
}

// readAll opens path and reads it to its end in a goroutine of its own, and gives the result on the
// channel it returns. The open is the caller's, so a test fails on an open that blocks or fails.
func readAll(t *testing.T, path string) <-chan read {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("opening the hold %s: %v", path, err)
	}
	done := make(chan read, 1)
	go func() {
		b, err := io.ReadAll(f)
		_ = f.Close()
		done <- read{b, err}
	}()
	return done
}

// waiting fails t if the read on done ends within 1000 yields, a count of yields as NoLeaks counts,
// never a clock: a read the hold does not hold ends at once with nothing, which the yields give it
// the time to do.
func waiting(t *testing.T, done <-chan read) {
	t.Helper()
	for range 1000 {
		select {
		case r := <-done:
			t.Fatalf("the read ended before release, with %q and %v", r.b, r.err)
		default:
			runtime.Gosched()
		}
	}
}

// ended fails t unless the read on done ended as an empty file's does: nothing read, and no error.
func ended(t *testing.T, done <-chan read) {
	t.Helper()
	if r := <-done; r.err != nil || len(r.b) != 0 {
		t.Fatalf("the read ended with %q and %v, want an empty file's end", r.b, r.err)
	}
}

// TestHoldReadHoldsAReaderUntilRelease: a reader opens a hold without blocking and its read waits until
// release, which ends it with nothing read; a second release is nothing.
//
// What it costs when this fails: a test of what a child does while it waits on a read, a signal sent
// to it say, runs once the child has stopped waiting, so it holds nothing.
func TestHoldReadHoldsAReaderUntilRelease(t *testing.T) {
	t.Parallel()
	path, release := HoldRead(t)
	done := readAll(t, path)
	waiting(t, done)
	release()
	ended(t, done)
	release()
}

// TestHoldReadEndsTheReadAtTheTestsEnd: a hold its test never releases is released by the test's
// cleanup, so its reader's read ends.
//
// What it costs when this fails: a test that fails before it releases leaves its child waiting on
// the read past the test, and the run hangs to its deadline.
func TestHoldReadEndsTheReadAtTheTestsEnd(t *testing.T) {
	t.Parallel()
	ct := &cleanupTB{TB: t}
	path, _ := HoldRead(ct)
	done := readAll(t, path)
	waiting(t, done)
	for _, f := range slices.Backward(ct.cleanups) {
		f()
	}
	ended(t, done)
}

// cleanupTB is t with its cleanups kept, for the test to run when it says its end is.
type cleanupTB struct {
	testing.TB
	cleanups []func()
}

func (c *cleanupTB) Cleanup(f func()) { c.cleanups = append(c.cleanups, f) }

// TestHoldReadAfterReleaseDoesNotBlock: an open after release does not block: on Unix it reads an
// empty file, and on Windows, where the hold was a pipe, it finds nothing at the name.
//
// What it costs when this fails: a child that opens the hold after its test released it waits on a
// read nothing ends, and the run hangs to its deadline.
func TestHoldReadAfterReleaseDoesNotBlock(t *testing.T) {
	t.Parallel()
	path, release := HoldRead(t)
	release()
	if holdIsAPipe {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("the pipe %s after release: %v, want it gone", path, err)
		}
		return
	}
	ended(t, readAll(t, path))
}

// Where Go makes no FIFO, as on aix, illumos and solaris, which have no syscall.Mkfifo, HoldRead skips
// its test with NoHold, goes no further and fails nothing; any other hold that cannot be made still
// fails the test. Both run on a recording TB, so no skip reaches the run's report.
//
// What it costs when this fails: a test on those systems fails for want of a FIFO, or goes on with no
// hold and reads at once, or a hold that failed for another reason is reported as a skip.
func TestHoldReadWithNoFIFOSkipsWithTheOneMessage(t *testing.T) {
	t.Parallel()
	none := record(t, func(tb testing.TB) {
		tb.Helper()
		holdWith(tb, func(string) (string, func() error, error) {
			return "", nil, fmt.Errorf("no syscall.Mkfifo on this system: %w", errors.ErrUnsupported)
		})
		tb.Error("the test went on past a hold the system cannot make")
	})
	if none.skipped != NoHold || none.failed != "" {
		t.Errorf("no FIFO: HoldRead skipped with %q and failed with %q; want skipped with NoHold and not failed", none.skipped, none.failed)
	}
	refused := record(t, func(tb testing.TB) {
		tb.Helper()
		holdWith(tb, func(string) (string, func() error, error) { return "", nil, errors.New("refused") })
		tb.Error("the test went on past a hold that failed")
	})
	if refused.skipped != "" || refused.failed != "testkit: HoldRead: refused" {
		t.Errorf("a hold that failed: HoldRead skipped with %q and failed with %q; want no skip and the failure", refused.skipped, refused.failed)
	}
}
