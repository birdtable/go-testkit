package testkit

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

// deferredLine is the line a helper's deferred call writes once Interrupted's context ended by the
// interrupt: the child's own end, which neither a kill nor a failure gives.
const deferredLine = "the deferred call ran after the interrupt ended the context"

// interruptedChild is the helper's part for Interrupted, given one of its cases. Each wait ends by its
// parent's interrupt, or at limit, as the helper's other waits do.
func interruptedChild(t *testing.T, limit context.Context, given string) {
	t.Helper()
	switch given {
	case "interrupted":
		ctx, stop, err := Interrupted(limit)
		if err != nil {
			t.Fatalf("Interrupted: %v", err)
		}
		defer stop()
		defer func() { t.Log(deferredLine) }()
		<-ctx.Done()
		if limit.Err() != nil {
			outlived(t)
		}
	case "interrupt at ready":
		self, err := os.FindProcess(os.Getpid())
		if err != nil {
			t.Fatal(err)
		}
		h, err := hold(self)
		if err != nil {
			t.Fatalf("holding itself for its interrupt: %v", err)
		}
		defer func() { _ = h.release() }()
		ctx, stop, err := interruptedTo(limit, interruptAtWrite{h})
		if err != nil {
			t.Fatalf("Interrupted: %v", err)
		}
		defer stop()
		defer func() { t.Log(deferredLine) }()
		<-ctx.Done()
		if limit.Err() != nil {
			outlived(t)
		}
	case "stopped":
		_, stop, err := interruptedTo(limit, io.Discard)
		if err != nil {
			t.Fatalf("Interrupted: %v", err)
		}
		stop()
		readyThenWait(t, limit)
	case "ready fails":
		planted := errors.New("planted")
		ctx, stop, err := interruptedTo(limit, failingWriter{planted})
		stop()
		if !errors.Is(err, planted) || ctx != limit {
			t.Fatalf("Interrupted whose ready line failed: %v, its own context %v; want the planted error and the context it was given", err, ctx == limit)
		}
		readyThenWait(t, limit)
	case "no child":
		was := os.Getenv(helperVar)
		if err := os.Unsetenv(helperVar); err != nil { //nolint:usetesting // the helper is the child's one test, and its variable comes back below
			t.Fatal(err)
		}
		ctx, stop, err := Interrupted(limit)
		if err := os.Setenv(helperVar, was); err != nil { //nolint:usetesting // as above
			t.Fatal(err)
		}
		defer stop()
		if err != nil || ctx != limit || ctx.Err() != nil {
			t.Fatalf("Interrupted in no child of Rerun: %v, its own context %v, ended %v; want no error and the open context it was given", err, ctx == limit, ctx.Err())
		}
		readyThenWait(t, limit)
	}
}

// failingWriter fails every write with err.
type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }

// interruptAtWrite sends the process h holds its interrupt in place of the ready line, and writes
// nothing: a child that writes through it is interrupted at the moment its line would go out, and its
// parent never reads a ready line, so never sends one of its own.
type interruptAtWrite struct{ h holder }

func (w interruptAtWrite) Write(p []byte) (int, error) {
	if err := w.h.interrupt(); err != nil {
		return 0, err
	}
	return len(p), nil
}

// readyThenWait says the helper is ready, by Ready alone, and waits for what its parent sends: a child
// that holds no handler is ended by the interrupt there, the system's way.
func readyThenWait(t *testing.T, limit context.Context) {
	t.Helper()
	if err := Ready(); err != nil {
		t.Fatalf("saying it is ready: %v", err)
	}
	<-limit.Done()
	outlived(t)
}

// readyLines counts the ready lines in out.
func readyLines(out []byte) int {
	n := 0
	for line := range strings.Lines(string(out)) {
		if strings.TrimRight(line, "\r\n") == readyLine {
			n++
		}
	}
	return n
}

// A child of RerunInterrupted that calls Interrupted sees its context end at the interrupt and its
// deferred call run, and an interrupt sent as the ready line goes out is caught too, as the handler is
// installed first; after stop it holds no handler and is ended by the interrupt the system's way; and
// in a process that is no child of Rerun, Interrupted returns the context it was given, still open,
// installs no handler and writes no ready line.
//
// What it costs when this fails: a child that calls Interrupted is interrupted before it can take the
// interrupt and dies of it, or keeps a handler its caller thought released, or a test in an ordinary go
// test run loses its default interrupt or prints a line only a parent should read.
func TestInterruptedEndsItsContextAtTheInterruptInAChildOfRerunAlone(t *testing.T) {
	t.Parallel()
	t.Run("in a child", func(t *testing.T) {
		t.Parallel()
		code, out, err := RerunInterrupted(bounded(t), testCap, "interrupted")
		last := "" // the helper's last line: the last that its own t.Log wrote
		for line := range strings.Lines(string(out)) {
			if strings.Contains(line, "interrupted_test.go:") {
				last = strings.TrimSpace(line)
			}
		}
		if err != nil || code != 0 || !strings.HasSuffix(last, deferredLine) || readyLines(out) != 1 {
			t.Errorf("a child that called Interrupted: code %d, %v, its last line %q, %d ready lines; want 0, no error, %q and one\n%s", code, err, last, readyLines(out), deferredLine, out)
		}
	})
	t.Run("the handler before the line", func(t *testing.T) {
		t.Parallel()
		code, out, err := RerunInterrupted(bounded(t), testCap, "interrupt at ready")
		if !errors.Is(err, ErrNotInterrupted) || code != 0 || !strings.Contains(string(out), deferredLine) || readyLines(out) != 0 {
			t.Errorf("a child interrupted as its ready line went out: code %d, %v, %d ready lines; want 0, ErrNotInterrupted that it never said it was ready, its context ended (%q) and no ready line\n%s", code, err, readyLines(out), deferredLine, out)
		}
	})
	for _, row := range []struct{ name, args string }{
		{"after stop", "stopped"},
		{"in no child of Rerun", "no child"},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			code, out, err := RerunInterrupted(bounded(t), testCap, row.args)
			if !endedTheSystemsWay(code, err) || readyLines(out) != 1 {
				t.Errorf("%s: code %d, %v, %d ready lines; want the interrupt's end the system's way, and the one ready line Ready wrote\n%s", row.name, code, err, readyLines(out), out)
			}
		})
	}
	t.Run("in this run", func(t *testing.T) {
		t.Parallel()
		parent, cancel := context.WithCancel(t.Context())
		var b strings.Builder
		ctx, stop, err := interruptedTo(parent, &b)
		defer stop()
		if err != nil || ctx != parent || ctx.Err() != nil || b.Len() != 0 {
			t.Errorf("Interrupted in a run go test started: %v, its own context %v, ended %v, wrote %q; want no error and the open context it was given, and nothing written", err, ctx == parent, ctx.Err(), b.String())
		}
		ctx, stop2, err := Interrupted(parent)
		defer stop2()
		if err != nil || ctx != parent {
			t.Errorf("Interrupted in a run go test started: %v, its own context %v; want no error and the context it was given", err, ctx == parent)
		}
		cancel()
		if !errors.Is(ctx.Err(), context.Canceled) {
			t.Errorf("the context after its parent ended: %v, want context.Canceled", ctx.Err())
		}
	})
}

// A ready line that cannot be written is Interrupted's error, and it leaves no handler: it returns the
// context it was given and a stop that does nothing. Outside a child the line is never written, so the
// failing write is reached only in a child of Rerun, and the row runs there.
//
// What it costs when this fails: a child whose parent never reads its line waits out its deadline with a
// handler installed, or its caller defers a stop that is nil.
func TestAReadyLineThatFailsIsInterruptedsErrorAndLeavesNoHandler(t *testing.T) {
	t.Parallel()
	code, out, err := RerunInterrupted(bounded(t), testCap, "ready fails")
	if !endedTheSystemsWay(code, err) {
		t.Errorf("a child whose ready line failed in Interrupted: code %d, %v; want the interrupt's end the system's way\n%s", code, err, out)
	}
}
