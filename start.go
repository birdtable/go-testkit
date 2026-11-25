package testkit

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec" //nolint:depguard // testkit imports none of birdtable's libraries, so not the proc runner: a built program is started here, with its own deadline
	"sync"
	"time"
)

// Running is a program a Binary started and has not yet been waited for to its end.
// Its methods may be called from any goroutine, and Wait, Stdout and Stderr more than once.
type Running struct {
	pkg       string
	cmd       *exec.Cmd
	ctx       context.Context //nolint:containedctx // the Start's own, read once its program ended, to say whether it ended the program
	out, errb *stream
	done      chan struct{}

	mu     sync.Mutex
	h      held
	ended  bool
	killed bool
	res    Result
}

// Start starts the binary with args, in the folder and the environment Run gives it, and gives a
// handle on the running program, for a test that interrupts or kills it. ctx must have a deadline,
// so no program is left to run unbounded: a ctx without one is refused with ErrRefused, and nothing
// is started. When ctx ends first the program is killed, and its Wait has code -1 with the reason
// last on Stderr. A build that failed, or Build's, X's or Env's refusal, is Start's error, as is a
// start that failed. The program runs in a process group of its own on Windows, so Interrupt
// reaches it and its group alone. Its stdin is empty.
func (b *Binary) Start(ctx context.Context, args ...string) (*Running, error) {
	if _, ok := ctx.Deadline(); !ok {
		return nil, fmt.Errorf("%w: Start of %s: its context has no deadline, and a program is never left to run unbounded", ErrRefused, b.key.pkg)
	}
	path, err := b.runnable()
	if err != nil {
		return nil, fmt.Errorf("testkit: Start of %s: %w", b.key.pkg, err)
	}
	r := &Running{pkg: b.key.pkg, ctx: ctx, out: newStream(), errb: newStream(), done: make(chan struct{})}
	r.cmd = exec.CommandContext(ctx, path, args...) //nolint:gosec // the binary this Binary built
	b.place(r.cmd)
	r.cmd.Stdout, r.cmd.Stderr = r.out, r.errb
	r.cmd.WaitDelay = time.Second
	ownGroup(r.cmd)
	if err := r.cmd.Start(); err != nil {
		return nil, fmt.Errorf("testkit: Start of %s: %w", b.key.pkg, err)
	}
	// Held here, before Wait can reap the program and drop os.Process's own hold on its id, so an
	// interrupt reaches this program and never a later process that took its id.
	if r.h, err = hold(r.cmd.Process); err != nil {
		return nil, errors.Join(fmt.Errorf("testkit: Start of %s: holding it for its interrupt: %w", b.key.pkg, err),
			r.cmd.Process.Kill(), ignoreExit(r.cmd.Wait()))
	}
	go r.wait()
	return r, nil
}

// ignoreExit is the error of a Wait that is not the program's own exit, which a Start that failed
// after its program started has asked for by killing it.
func ignoreExit(err error) error {
	if _, ok := errors.AsType[*exec.ExitError](err); ok {
		return nil
	}
	return err
}

// wait waits for the program, which copies all of its streams in, and ends them, so their readers
// reach io.EOF; then, under mu, it marks the program ended, so no signal is sent after, lets its hold
// go and writes its Result.
func (r *Running) wait() {
	err := r.cmd.Wait()
	r.out.close()
	r.errb.close()
	r.mu.Lock()
	defer r.mu.Unlock()
	defer close(r.done)
	r.ended = true
	res := Result{Code: r.cmd.ProcessState.ExitCode(), Stdout: r.out.String(), Stderr: r.errb.String()}
	_, exit := errors.AsType[*exec.ExitError](err)
	switch {
	case err != nil && r.ctx.Err() != nil:
		res.Code = -1
		res.Stderr += fmt.Sprintf("\ntestkit: %s was killed as its context ended: %v", r.pkg, context.Cause(r.ctx))
	case r.killed && killLeavesACode:
		res.Code = -1
	case err != nil && !exit:
		res.Code = -1
		res.Stderr += fmt.Sprintf("\ntestkit: waiting for %s: %v", r.pkg, err)
	}
	if err := r.h.release(); err != nil {
		res.Stderr += fmt.Sprintf("\ntestkit: letting go of %s's hold: %v", r.pkg, err)
	}
	r.res = res
}

// Interrupt sends the program the system's interrupt: SIGINT on Unix, and CTRL_BREAK_EVENT to its
// own process group on Windows, which Go's os/signal hands a Go program as os.Interrupt. The program
// ends as it handles it: a Go program that does not ends with code -1 on Unix, a death by signal,
// and 0xC000013A on Windows. Once the program ended, Interrupt sends nothing and is
// os.ErrProcessDone.
func (r *Running) Interrupt() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ended {
		return os.ErrProcessDone
	}
	return r.h.interrupt()
}

// Kill ends the program at once, and its Wait then has code -1 on every system: on Unix as a death by
// signal, and on Windows, where a killed program's code is 1, as Kill says it ended it. It kills the
// program alone, not what it started. Once the program ended, Kill does nothing and is
// os.ErrProcessDone; on Windows a Kill that meets a program just ending by itself fails, and the
// program keeps its own code.
func (r *Running) Kill() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ended {
		return os.ErrProcessDone
	}
	err := r.cmd.Process.Kill()
	r.killed = err == nil
	return err
}

// Terminate sends the program SIGTERM on Unix, which a Go program's os/signal hands it as
// syscall.SIGTERM; a program that does not handle it ends with code -1, a death by signal. Windows
// has no SIGTERM: there Terminate sends nothing, and its error wraps errors.ErrUnsupported, while
// Interrupt and Kill reach the program on every system. Once the program ended, Terminate sends
// nothing and is os.ErrProcessDone.
func (r *Running) Terminate() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ended {
		return os.ErrProcessDone
	}
	return r.h.terminate()
}

// Stdout gives the program's standard output as it comes, from its start, and may be read while the
// program runs: a Read that has read all there is waits for more, or for the program's end, after
// which it is io.EOF. The output is kept, not piped through the test, so the program never waits on
// a test that does not read, and Wait's Result has all of it. Each call gives a new reader, from the
// start.
func (r *Running) Stdout() io.Reader { return r.out.reader() }

// Stderr is Stdout for the program's standard error. It gives the program's own stream: what Wait
// adds to its Result's Stderr about the end is not in it.
func (r *Running) Stderr() io.Reader { return r.errb.reader() }

// Wait waits for the program to end and gives its code and what it wrote, the same Result each call.
func (r *Running) Wait() Result {
	<-r.done
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.res
}
