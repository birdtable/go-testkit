package testkit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec" //nolint:depguard // testkit imports none of birdtable's libraries, so not the proc runner, and Rerun gives its child a deadline and an output cap itself
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
)

// HelperTest is the test Rerun runs in the child. A package that reruns its own test binary has one test
// of this name: when Helper says this process is a child, it does what it was given; in a run that go
// test started, it checks it was given nothing.
const HelperTest = "TestTheHelperRunsOnlyWhenRerun"

// helperVar carries what Rerun gives the child. It is on the kept list, so the child's Main keeps it
// by name; Helper reads it only in a run of HelperTest alone.
const helperVar = "TESTKIT_HELPER"

// helperRun is the -test.run of a child of Rerun: Helper answers true only under it.
const helperRun = "^" + HelperTest + "$"

// ErrEnded is the error of a Rerun whose child was ended by its deadline or its context.
var ErrEnded = errors.New("the rerun child was ended by its deadline or its context")

// ErrRefused is the error of a Rerun that started no child because its bounds were not the caller's
// to leave out: a ctx with no deadline, or an output cap below one byte.
var ErrRefused = errors.New("the rerun child was not started")

// ErrOutputCap is the error of a Rerun whose child wrote more than the caller's output cap to one of its
// streams, and was ended for it.
var ErrOutputCap = errors.New("the rerun child was ended for writing past its output cap")

// ErrNotInterrupted is the error of a RerunInterrupted whose child was not interrupted: it ended before it
// said it was ready, or the interrupt could not be sent, and then it was ended.
var ErrNotInterrupted = errors.New("the rerun child was not interrupted")

// childGrace is how long past its caller's deadline a child of Rerun runs by its own clock: the child is
// started with a -test.timeout of what is left of the deadline and childGrace, so a parent that runs ends
// its child first, at the deadline, and a child whose parent is gone before it could end it, killed by a
// timeout or a cancelled push, ends by itself, by go test's own timeout.
const childGrace = 10 * time.Second

// readyLine is the line a child of RerunInterrupted writes, by Ready, when it can take its interrupt.
const readyLine = "testkit: the rerun child is ready for its interrupt"

// Rerun runs this test binary again, as a child that runs only HelperTest, in the environment Clean
// gives, with args for it to read with Helper, and waits for it, at most until ctx ends. Both bounds are
// the caller's: ctx must have a deadline, and outputCap, the bytes kept of each of the child's stdout and
// stderr, must be 1 or more; otherwise Rerun starts nothing and its error is ErrRefused. It returns the
// child's exit code and what it printed, stdout then stderr, at most outputCap bytes of each. A child
// that failed is no error here. A child that a signal killed has code -1 and, as its err, its own
// *exec.ExitError, from which Signal reads the signal. A child ended by its deadline or ctx has code -1
// and an error that is ErrEnded; one that wrote past outputCap is ended, with code -1, what it wrote up
// to the cap, and an error that is ErrOutputCap; any other error is a child that could not be started.
//
// The child is this binary, as the system names it to os.Executable, and runs one test of it, never
// what a caller names: every other argument is a literal, and args go by the environment. Its
// -test.timeout is what is left of ctx's deadline and 10 s more, so a child whose parent is gone before it
// could end it ends by itself, by go test's timeout, with code 2 and a goroutine dump.
func Rerun(ctx context.Context, outputCap int, args ...string) (code int, out []byte, err error) {
	return rerun(ctx, outputCap, nil, nil, childGrace, args)
}

// RerunVia runs the child as Rerun does, under the same caller-given deadline and output cap, behind
// wrapper: wrapper[0] is started with wrapper[1:] and then the child's own command line, this binary
// and its literal arguments, so a wrapper that confines a program, a sandbox say, runs the child
// inside it. wrapper[0] is a path, never looked up: an empty wrapper, or one whose program is no
// absolute path, starts nothing, and its error is ErrRefused. The wrapper is handed the environment
// Rerun gives the child, runs the command line it is given and passes its exit on; what it does
// besides is its own. Its results are Rerun's, read from the wrapper: its code, what it and the child
// printed, and a deadline or cap that ends the wrapper. The deadline ends the wrapper alone: one that
// does not pass the kill on leaves the child running past it, until the child's own timeout ends it 10 s
// later, and RerunVia still returns a second after the deadline, when the pipes are closed. The wrapper
// owns its child's end.
func RerunVia(ctx context.Context, outputCap int, wrapper []string, args ...string) (code int, out []byte, err error) {
	if len(wrapper) == 0 || !filepath.IsAbs(wrapper[0]) {
		return -1, nil, fmt.Errorf("%w: the wrapper %q names no program by an absolute path, and a wrapper is never looked up", ErrRefused, wrapper)
	}
	return rerun(ctx, outputCap, wrapper, nil, childGrace, args)
}

// RerunInterrupted runs the child as Rerun does, under the same caller-given deadline and output cap, and
// sends it the system's interrupt once it says it is ready, by a line Ready writes. On Unix the interrupt is
// SIGINT; on Windows the child is started in a process group of its own and sent CTRL_BREAK_EVENT. Go's
// os/signal hands either to the child as os.Interrupt, so a child that waits on
// signal.NotifyContext(ctx, os.Interrupt) and calls Ready after it ends by the interrupt, its own way.
// Interrupted does both, in that order, and is the usual way.
//
// Its results are Rerun's. One more error is possible: a child that ended before it said it was ready, or
// that could not be sent its interrupt, has an error that is ErrNotInterrupted, with its code and what it
// printed; one the interrupt could not reach is ended first, with code -1. A child that does not catch the
// interrupt is ended by it, as the system ends a program for one: on Unix it is a signal death, which
// Signal reads; on Windows a code of the system's own.
func RerunInterrupted(ctx context.Context, outputCap int, args ...string) (code int, out []byte, err error) {
	return rerun(ctx, outputCap, nil, func(p *os.Process) (holder, error) { return hold(p) }, childGrace, args)
}

// Ready writes the line by which a child of RerunInterrupted says it can take its interrupt, so it is
// called once the child catches os.Interrupt; Interrupted calls it for a child that catches the interrupt
// alone. In a process that is no child of Rerun it writes nothing. Its
// error is the write's: a child whose line was not written is never interrupted.
func Ready() error {
	return readyTo(os.Stdout) //nolint:forbidigo // the ready line is the child's word to its parent, on the stream the parent reads
}

// Interrupted is the usual way a child of RerunInterrupted takes its interrupt: it returns a context
// that ends with ctx or at os.Interrupt, and its stop, and says the child is ready. It installs the
// handler, as signal.NotifyContext(ctx, os.Interrupt) does, and only then writes Ready's line. That order
// is its promise: the parent interrupts the child as soon as it reads the line, and an interrupt that
// came before the handler would end the child by the system's default action, never by its context.
// The caller defers stop, which releases the handler, so an interrupt after it ends the process the
// system's way.
//
// In a process that is no child of Rerun it returns ctx and a stop that does nothing, installs no
// handler and writes nothing, so a test that calls it in a run go test started keeps the default
// interrupt. Its error is Ready's, the write's: then it has released the handler itself and returns ctx
// and a stop that does nothing, which the caller may still defer. A child that catches more than the
// interrupt installs its own handler and calls Ready after it.
func Interrupted(ctx context.Context) (context.Context, func(), error) {
	return interruptedTo(ctx, os.Stdout) //nolint:forbidigo // the ready line is the child's word to its parent, on the stream the parent reads
}

// interruptedTo is Interrupted, writing the ready line to w.
func interruptedTo(ctx context.Context, w io.Writer) (context.Context, func(), error) {
	if _, ok := Helper(); !ok {
		return ctx, func() {}, nil
	}
	caught, stop := signal.NotifyContext(ctx, os.Interrupt)
	if err := readyTo(w); err != nil {
		stop()
		return ctx, func() {}, err
	}
	return caught, stop, nil
}

// readyTo writes the ready line to w, in a child of Rerun alone.
func readyTo(w io.Writer) error {
	if _, ok := Helper(); !ok {
		return nil
	}
	_, err := fmt.Fprintf(w, "\n%s\n", readyLine)
	return err
}

// rerun is Rerun, RerunVia when via is set, and RerunInterrupted when holdBy is set: via is the
// wrapper's command line, which the child's follows, and holdBy holds the child for its interrupt. The
// child's -test.timeout is what is left of ctx's deadline and grace, childGrace but in this package's
// own row of it.
func rerun(ctx context.Context, outputCap int, via []string, holdBy func(*os.Process) (holder, error), grace time.Duration, args []string) (code int, out []byte, err error) {
	deadline, ok := ctx.Deadline()
	if !ok {
		return -1, nil, fmt.Errorf("%w: its context has no deadline, and a child is never left to run unbounded", ErrRefused)
	}
	if outputCap < 1 {
		return -1, nil, fmt.Errorf("%w: an output cap of %d bytes, and a cap is 1 or more", ErrRefused, outputCap)
	}
	self, err := os.Executable()
	if err != nil {
		return -1, nil, fmt.Errorf("this system names no running test binary, so it cannot be rerun: %w", err)
	}
	given, err := json.Marshal(args)
	if err != nil {
		return -1, nil, err
	}
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	// A -test.timeout of 0 or less is none, so the child's is grace at the least: a deadline already
	// past ends the child before it starts, through ctx.
	timeout := max(time.Until(deadline), 0) + grace
	argv := append(slices.Clone(via), self, "-test.run="+helperRun, "-test.count=1", "-test.v", "-test.timeout="+timeout.String())
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...) //nolint:gosec // this test binary itself, with literal arguments, or behind the caller's wrapper named by an absolute path
	cmd.Env, err = childEnv(inForce(), os.Environ(), string(given))
	if err != nil {
		return -1, nil, err
	}
	cmd.WaitDelay = time.Second
	stdout, stderr := &capped{limit: outputCap, end: cancel}, &capped{limit: outputCap, end: cancel}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	said := &ready{seen: make(chan struct{})}
	if holdBy != nil {
		cmd.Stdout = io.MultiWriter(stdout, said)
		ownGroup(cmd)
	}
	if err := cmd.Start(); err != nil {
		return -1, nil, err
	}
	waited := make(chan struct{})
	var sent bool
	var wg sync.WaitGroup
	if holdBy != nil {
		// Held here, before Wait can reap the child and drop os.Process's own hold on its id, so the hold
		// is of this child and of no later process that took its id.
		h, herr := holdBy(cmd.Process)
		if herr != nil {
			cancel(fmt.Errorf("%w: it could not be held for its interrupt: %w", ErrNotInterrupted, herr))
		} else {
			defer func() { err = errors.Join(err, h.release()) }()
			wg.Go(func() { sent = interruptWhenReady(ctx, cancel, h, said.seen, waited) })
		}
	}
	err = cmd.Wait()
	close(waited)
	wg.Wait()
	out = append(stdout.b, stderr.b...)
	switch cause := context.Cause(ctx); {
	case errors.Is(cause, ErrOutputCap):
		return -1, out, fmt.Errorf("%w of %d bytes", ErrOutputCap, outputCap)
	case errors.Is(cause, ErrNotInterrupted):
		return -1, out, cause
	case ctx.Err() != nil:
		return -1, out, fmt.Errorf("%w: %w", ErrEnded, cause)
	}
	code, err = exited(cmd, err)
	if holdBy != nil && !sent && err == nil {
		return code, out, fmt.Errorf("%w: it ended before it said it was ready", ErrNotInterrupted)
	}
	return code, out, err
}

// childEnv is the environment of a child of Rerun under the isolation in, from environ, the test
// process's own: Clean's entries, then each NAME=value of each RerunEnv of in, called now, less an
// entry with no name before an =, then helperVar with given, last. os/exec keeps the last of two
// entries of one name, so no entry of a RerunEnv's replaces what Helper reads. An entry naming what
// the isolation set (a home, git's global configuration, a pinned setting, a go folder, or GoOffline's
// four), in any case, as Windows reads names, is refused with ErrRefused, since it would come after
// Clean's value and win.
func childEnv(in isolated, environ []string, given string) ([]string, error) {
	env := in.clean(environ)
	for _, f := range in.conf.rerunEnv {
		for _, kv := range f() {
			k, _, ok := strings.Cut(kv, "=")
			if !ok || k == "" {
				continue
			}
			if _, pinned := in.set[strings.ToUpper(k)]; pinned {
				return nil, fmt.Errorf("%w: a RerunEnv gives %s, which the isolation sets, and a child keeps the isolation's value", ErrRefused, k)
			}
			env = append(env, kv)
		}
	}
	return append(env, helperVar+"="+given), nil
}

// exited is the code and error of a child that ended by itself: a failure is no error, and a signal
// death is code -1 and its own *exec.ExitError.
func exited(cmd *exec.Cmd, err error) (int, error) {
	if exit, ok := errors.AsType[*exec.ExitError](err); ok && exit.ExitCode() >= 0 {
		return exit.ExitCode(), nil
	}
	return cmd.ProcessState.ExitCode(), err
}

// interruptWhenReady sends the child h holds the system's interrupt once seen closes, and reports whether
// it sent it. It sends nothing once the child was waited for, or ctx ended. When the send fails, it ends
// the child with ErrNotInterrupted.
func interruptWhenReady(ctx context.Context, end context.CancelCauseFunc, h holder, seen, waited <-chan struct{}) bool {
	select {
	case <-seen:
	case <-waited:
		return false
	case <-ctx.Done():
		return false
	}
	select {
	case <-waited:
		return false
	default:
	}
	if err := h.interrupt(); err != nil {
		end(fmt.Errorf("%w: its interrupt could not be sent: %w", ErrNotInterrupted, err))
		return false
	}
	return true
}

// holder holds a child of RerunInterrupted from its start until its interrupt is sent, so the interrupt
// reaches that child and never a later process that took its id; hold gives the system's.
type holder interface {
	interrupt() error
	release() error
}

// ready watches a child's stdout for the line Ready writes, and closes seen at the first one: a line
// that is readyLine alone, with or without a carriage return before its newline. It keeps no more of a
// line than readyLine and that return.
type ready struct {
	seen chan struct{}
	line []byte
	long bool
	once sync.Once
}

func (r *ready) Write(p []byte) (int, error) {
	for _, b := range p {
		if b != '\n' {
			if len(r.line) > len(readyLine) {
				r.long = true
				continue
			}
			r.line = append(r.line, b)
			continue
		}
		if !r.long && string(bytes.TrimSuffix(r.line, []byte("\r"))) == readyLine {
			r.once.Do(func() { close(r.seen) })
		}
		r.line, r.long = r.line[:0], false
	}
	return len(p), nil
}

// capped keeps the first limit bytes written to it. A write that would take it past limit is kept up to
// limit, and ends the child with ErrOutputCap as the cause; it and every write after are taken without
// error, so the copy to it does not stall while the child is ended.
type capped struct {
	b     []byte
	limit int
	end   context.CancelCauseFunc
}

func (c *capped) Write(p []byte) (int, error) {
	room := max(0, c.limit-len(c.b))
	if len(p) > room {
		c.end(ErrOutputCap)
	}
	c.b = append(c.b, p[:min(len(p), room)]...)
	return len(p), nil
}

// Signal is the signal that killed the child of a Rerun whose err is err, as an os.Signal, so a caller
// compares it with os.Kill or os.Interrupt and imports no syscall. It is nil and false
// when err is no such death, as on Windows, where a kill is no signal.
func Signal(err error) (os.Signal, bool) {
	exit, ok := errors.AsType[*exec.ExitError](err)
	if !ok {
		return nil, false
	}
	ws, ok := exit.Sys().(syscall.WaitStatus)
	if !ok || !ws.Signaled() {
		return nil, false
	}
	return ws.Signal(), true
}

// Helper is what Rerun gave this process, and whether this process is a child of Rerun at all: false
// in a run that go test or a person started. A child has the variable set and was asked to run
// HelperTest and nothing else (its last -test.run is the one Rerun passes), so a variable exported
// before go test, which Main keeps by name, makes no run a child unless that run asks for HelperTest
// alone, by Rerun's exact pattern.
func Helper() ([]string, bool) {
	given, ok := os.LookupEnv(helperVar)
	if !ok {
		return nil, false
	}
	if run, ok := testRun(os.Args[1:]); !ok || run != helperRun {
		return nil, false
	}
	var args []string
	if err := json.Unmarshal([]byte(given), &args); err != nil {
		return nil, false
	}
	return args, true
}

// testRun is the value of the last -test.run of args, as the flag package reads one (-test.run=v,
// --test.run=v, -test.run v, --test.run v), and false when args holds none before a "--".
func testRun(args []string) (string, bool) {
	run, found := "", false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			break
		}
		name := strings.TrimPrefix(strings.TrimPrefix(a, "-"), "-")
		if name == a || !strings.HasPrefix(name, "test.run") {
			continue
		}
		switch rest := strings.TrimPrefix(name, "test.run"); {
		case rest == "" && i+1 < len(args):
			i++
			run, found = args[i], true
		case strings.HasPrefix(rest, "="):
			run, found = rest[1:], true
		}
	}
	return run, found
}
