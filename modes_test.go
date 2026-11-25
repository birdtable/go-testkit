package testkit

import (
	"fmt"
	"testing"

	"github.com/birdtable/go-testkit/internal/homevars"
)

// skipRecorder is a test's own testing.TB whose Skip is only noted, so a test reads what a skipping
// helper did without being skipped itself: the push tier fails a skip that does not say it runs in the
// release tier, and a helper's skip on a host is that host's answer, not this test's. Everything else
// is the test's own, so a probe that fails fails the test.
type skipRecorder struct {
	testing.TB

	skipped []string
}

// Skip notes the skip and returns, where a test's Skip would end it.
func (r *skipRecorder) Skip(args ...any) { r.skipped = append(r.skipped, fmt.Sprint(args...)) }

// keepsModesHere is whether this system keeps POSIX mode bits on a file, by its name: Windows keeps a
// read-only flag alone, and Linux and macOS keep the bits. The tests hold KeepsModes and Modes to it
// apart from the probe, so a probe that is wrong for the system is red.
const keepsModesHere = homevars.Here != "windows"

// TestKeepsModesIsWhetherTheSystemKeepsModeBits: KeepsModes is false on Windows and true on Linux and
// macOS, and skips nothing on either, so a test reads a mode in only the assertions that need one and
// checks the rest on Windows too.
//
// What it costs when this fails: a test that guards a mode read by KeepsModes reads a mode on Windows
// and fails for the system's sake, or reads none on Unix, where the mode is what it measures; or a
// test that only asked is skipped whole, and loses its Windows coverage.
func TestKeepsModesIsWhetherTheSystemKeepsModeBits(t *testing.T) {
	t.Parallel()
	rec := &skipRecorder{TB: t}
	if got := KeepsModes(rec); got != keepsModesHere {
		t.Errorf("KeepsModes = %t, want %t on this system: a file set to %v keeps it there", got, keepsModesHere, probeMode)
	}
	if len(rec.skipped) != 0 {
		t.Errorf("KeepsModes skipped with %q; it must act on nothing", rec.skipped)
	}
}

// TestModesSkipsWhereTheSystemKeepsNoModeBits: Modes skips a test with NoModes on Windows, which
// keeps a read-only flag alone, and lets it run on Linux and macOS.
//
// What it costs when this fails: a test that reads a file's mode fails on Windows for the system's
// sake, or is skipped on Unix, where it measures what it is for.
func TestModesSkipsWhereTheSystemKeepsNoModeBits(t *testing.T) {
	t.Parallel()
	rec := &skipRecorder{TB: t}
	Modes(rec)
	want := []string{NoModes}
	if keepsModesHere {
		want = nil
	}
	if fmt.Sprint(rec.skipped) != fmt.Sprint(want) {
		t.Errorf("Modes skipped with %q, want %q on this system", rec.skipped, want)
	}
}
