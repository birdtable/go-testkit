package testkit

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

// progWaits sets its handler for os.Interrupt, writes "ready", then dials the address it is given, so
// a test that accepts the dial knows the program can take its interrupt and that "ready" is already
// written, and then waits for it. "ready" comes before the dial: written after it, a Kill sent as the
// dial was accepted could land before the write, and the program's Stdout was empty.
const progWaits = `package main

import (
	"net"
	"os"
	"os/signal"
)

func main() {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	os.Stdout.WriteString("ready")
	conn, err := net.Dial("tcp", os.Args[1])
	if err != nil {
		os.Stderr.WriteString(err.Error())
		os.Exit(2)
	}
	s := <-c
	conn.Close()
	os.Stderr.WriteString(s.String())
	os.Exit(7)
}
`

// startWaiting starts progWaits and returns once it can take its interrupt: once its dial is in.
// The listener closes when the program ends, so an Accept the program never dials returns.
func startWaiting(t *testing.T) *Running {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Hour)
	t.Cleanup(cancel)
	return startWaitingIn(t, ctx)
}

// startWaitingIn is startWaiting under ctx.
func startWaitingIn(t *testing.T, ctx context.Context) *Running {
	t.Helper()
	var lc net.ListenConfig
	l, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	r, err := Build(plantProgram(t, progWaits)).Start(ctx, l.Addr().String())
	if err != nil {
		_ = l.Close()
		t.Fatal(err)
	}
	go func() {
		r.Wait()
		_ = l.Close()
	}()
	c, err := l.Accept()
	if err != nil {
		t.Fatalf("the program ended before it dialled: %+v", r.Wait())
	}
	t.Cleanup(func() { _ = c.Close() })
	return r
}

// TestStartInterruptEndsTheProgramByItsHandler: Interrupt reaches a started program as os.Interrupt,
// on every system, and Wait gives the code and the streams of the end its handler chose.
//
// What it costs when this fails: a test of a product's interrupt passes on Unix and cannot be
// written on Windows, where Process.Signal(os.Interrupt) is not implemented, or it reads the wrong
// end.
func TestStartInterruptEndsTheProgramByItsHandler(t *testing.T) {
	t.Parallel()
	r := startWaiting(t)
	if err := r.Interrupt(); err != nil {
		t.Fatal(err)
	}
	if got := r.Wait(); got.Code != 7 || got.Stdout != "ready" || got.Stderr != "interrupt" {
		t.Fatalf("an interrupted program's Wait is %+v, want code 7, ready and interrupt", got)
	}
}

// TestStartKillReportsTheProgramKilled: Kill ends a program that would wait for ever, and Wait has
// code -1 on every system, though Windows gives a killed program code 1.
//
// What it costs when this fails: a test of a crash reads a kill as the program's own failure on
// Windows, or a program that never ends holds its test to the deadline.
func TestStartKillReportsTheProgramKilled(t *testing.T) {
	t.Parallel()
	r := startWaiting(t)
	if err := r.Kill(); err != nil {
		t.Fatal(err)
	}
	if got := r.Wait(); got.Code != -1 || got.Stdout != "ready" {
		t.Fatalf("a killed program's Wait is %+v, want code -1 and ready", got)
	}
}

// TestStartSendsNothingOnceTheProgramEnded: after the end, Interrupt, Kill and Terminate send nothing and are
// os.ErrProcessDone, and a second Wait gives the same Result.
//
// What it costs when this fails: an interrupt or a kill sent late reaches a later process that took
// the program's id, or a test's second Wait hangs.
func TestStartSendsNothingOnceTheProgramEnded(t *testing.T) {
	t.Parallel()
	r := startWaiting(t)
	if err := r.Kill(); err != nil {
		t.Fatal(err)
	}
	first := r.Wait()
	if err := r.Interrupt(); !errors.Is(err, os.ErrProcessDone) {
		t.Errorf("Interrupt after the end: %v, want os.ErrProcessDone", err)
	}
	if err := r.Kill(); !errors.Is(err, os.ErrProcessDone) {
		t.Errorf("Kill after the end: %v, want os.ErrProcessDone", err)
	}
	if err := r.Terminate(); !errors.Is(err, os.ErrProcessDone) {
		t.Errorf("Terminate after the end: %v, want os.ErrProcessDone", err)
	}
	if again := r.Wait(); again != first {
		t.Errorf("a second Wait is %+v, want the first's %+v", again, first)
	}
}

// TestStartKillsTheProgramWhenItsContextEnds: a program whose context ends is killed, and Wait says
// so, with code -1 and the cause last on Stderr.
//
// What it costs when this fails: a program outlives the test that started it, or its end reads as
// one it chose.
func TestStartKillsTheProgramWhenItsContextEnds(t *testing.T) {
	t.Parallel()
	parent, end := context.WithCancelCause(t.Context())
	ctx, cancel := context.WithTimeout(parent, time.Hour)
	defer cancel()
	r := startWaitingIn(t, ctx)
	end(errors.New("the test's end"))
	if got := r.Wait(); got.Code != -1 || !strings.Contains(got.Stderr, "its context ended: the test's end") {
		t.Fatalf("a program whose context ended has %+v, want code -1 and the context named", got)
	}
}

// TestStartRefusesAContextWithoutADeadlineAndABuildThatFailed: Start starts nothing under a context
// with no deadline, which is ErrRefused, nor for a refused Build, which is its error.
//
// What it costs when this fails: a program a test forgets to wait for runs past the test and the
// run, or a refused package reaches go.
func TestStartRefusesAContextWithoutADeadlineAndABuildThatFailed(t *testing.T) {
	t.Parallel()
	if r, err := Build(plantProgram(t, progWaits)).Start(t.Context()); !errors.Is(err, ErrRefused) || r != nil {
		t.Errorf("Start under a context with no deadline: %v, %v, want nil and ErrRefused", r, err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Hour)
	defer cancel()
	if r, err := Build("-refused").Start(ctx); err == nil || r != nil || !strings.Contains(err.Error(), "go build would read as a flag") {
		t.Errorf("Start of a refused Build: %v, %v, want nil and Build's refusal", r, err)
	}
}

// progReady sets its handler for os.Interrupt and SIGTERM, writes "waiting" to Stderr and then
// "ready" to Stdout, each a line, and waits for either signal. It writes the signal's name to Stdout
// and ends with code 7 for an interrupt and 8 for SIGTERM, so a test reads which signal came. A
// test that reads "ready" from Stdout knows the program can take either.
const progReady = `package main

import (
	"os"
	"os/signal"
	"syscall"
)

func main() {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	os.Stderr.WriteString("waiting\n")
	os.Stdout.WriteString("ready\n")
	s := <-c
	os.Stdout.WriteString(s.String())
	if s == syscall.SIGTERM {
		os.Exit(8)
	}
	os.Exit(7)
}
`

// startReady starts progReady and returns once it read the program's "waiting" from Stderr and its
// "ready" from Stdout, before Wait: then the program can take either signal. It gives the two
// readers, read past those lines.
func startReady(t *testing.T) (r *Running, out, errs *bufio.Reader) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Hour)
	t.Cleanup(cancel)
	r, err := Build(plantProgram(t, progReady)).Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	out, errs = bufio.NewReader(r.Stdout()), bufio.NewReader(r.Stderr())
	if line, err := out.ReadString('\n'); line != "ready\n" || err != nil {
		_ = r.Kill()
		t.Fatalf("Stdout before the end gave %q, %v, want the line ready; the program's Wait: %+v", line, err, r.Wait())
	}
	if line, err := errs.ReadString('\n'); line != "waiting\n" || err != nil {
		_ = r.Kill()
		t.Fatalf("Stderr before the end gave %q, %v, want the line waiting; the program's Wait: %+v", line, err, r.Wait())
	}
	return r, out, errs
}

// endsBy sends the program started by startReady a signal by send, and checks that it ended by that
// signal, which it names as name with code code: the same readers reach io.EOF at the end, and what
// each gave, and a new reader after Wait, equal Wait's streams.
func endsBy(t *testing.T, send func(*Running) error, name string, code int) {
	t.Helper()
	r, out, errs := startReady(t)
	if err := send(r); err != nil {
		t.Fatal(err)
	}
	if rest, err := io.ReadAll(out); err != nil || string(rest) != name {
		t.Errorf("Stdout after the signal gave %q, %v, want %q and its end", rest, err, name)
	}
	if rest, err := io.ReadAll(errs); err != nil || len(rest) != 0 {
		t.Errorf("Stderr after its line gave %q, %v, want nothing more and its end", rest, err)
	}
	got := r.Wait()
	if got.Code != code || got.Stdout != "ready\n"+name || got.Stderr != "waiting\n" {
		t.Fatalf("the program's Wait is %+v, want code %d, ready then %s, and waiting", got, code, name)
	}
	if again, err := io.ReadAll(r.Stdout()); err != nil || string(again) != got.Stdout {
		t.Errorf("a new Stdout after Wait gave %q, %v, want all of it from its start, %q", again, err, got.Stdout)
	}
	if again, err := io.ReadAll(r.Stderr()); err != nil || string(again) != got.Stderr {
		t.Errorf("a new Stderr after Wait gave %q, %v, want all of it from its start, %q", again, err, got.Stderr)
	}
}

// TestStartStreamsAreReadWhileTheProgramRuns: a test reads the program's readiness line from Stdout
// before Wait, sends Interrupt, and the program ends by it, on every system; the readers then reach
// io.EOF, and a new reader after Wait gives all of each stream from its start.
//
// What it costs when this fails: a test of a server waits on a dial or a clock to know it is up, or
// reads its output only after it ended.
func TestStartStreamsAreReadWhileTheProgramRuns(t *testing.T) {
	t.Parallel()
	endsBy(t, (*Running).Interrupt, "interrupt", 7)
}

// progFloods writes more than a pipe holds to each stream, 1 MiB of a's to Stdout and of b's to
// Stderr, and ends with code 0.
const progFloods = `package main

import (
	"bytes"
	"os"
)

func main() {
	os.Stdout.Write(bytes.Repeat([]byte("a"), 1<<20))
	os.Stderr.Write(bytes.Repeat([]byte("b"), 1<<20))
}
`

// TestStartKeepsOutputNobodyReads: a program that writes past a pipe's size, with no test reading
// Stdout or Stderr, ends by itself, and Wait has all of both streams.
//
// What it costs when this fails: a program blocks on a full pipe until its test's deadline kills it,
// and the test reads that as the program hanging.
func TestStartKeepsOutputNobodyReads(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), time.Hour)
	defer cancel()
	r, err := Build(plantProgram(t, progFloods)).Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := r.Wait()
	if got.Code != 0 || got.Stdout != strings.Repeat("a", 1<<20) || got.Stderr != strings.Repeat("b", 1<<20) {
		t.Fatalf("a program that wrote 1 MiB to each stream has code %d, %d bytes of Stdout and %d of Stderr, want code 0 and all of each: %.200s",
			got.Code, len(got.Stdout), len(got.Stderr), got.Stderr)
	}
}
