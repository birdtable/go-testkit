package testkit

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestTheHelperRunsOnlyWhenRerun is this package's helper: rerun, it does what it was given; started by
// go test, it was given nothing.
func TestTheHelperRunsOnlyWhenRerun(t *testing.T) {
	t.Parallel()
	args, ok := Helper()
	if !ok {
		if _, set := os.LookupEnv(helperVar); set {
			t.Errorf("a run go test started holds %s", helperVar)
		}
		return
	}
	// Every wait below is one its parent ends, by a kill, an interrupt or the deadline it gave its
	// Rerun. A parent killed first ends none of them, so each also ends at the helper's own limit.
	limit, stop := context.WithTimeout(t.Context(), helperLimit)
	defer stop()
	switch strings.Join(args, " ") {
	case "kill":
		self, err := os.FindProcess(os.Getpid())
		if err == nil {
			err = self.Kill()
		}
		if err != nil {
			t.Fatalf("the child was to kill itself: %v", err)
		}
		// The kill can return before it lands, as a process-wide signal may be taken by another
		// thread: a report here would race it to the exit, and its code would be read as the
		// kill. So the child waits for its kill, which nothing else ends but its limit.
		<-limit.Done()
		outlived(t)
	case "fail":
		t.Fatal("the child fails as it was told")
	case "interrupt":
		ctx, stop := signal.NotifyContext(limit, os.Interrupt)
		defer stop()
		if err := Ready(); err != nil {
			t.Fatalf("saying it is ready: %v", err)
		}
		<-ctx.Done()
		if limit.Err() != nil {
			outlived(t)
		}
		t.Log(interruptedLine)
		return
	case "interrupted", "interrupt at ready", "stopped", "ready fails", "no child":
		interruptedChild(t, limit, args[0])
		return
	case "flood":
		t.Log(strings.Repeat("x", 64<<10))
		fallthrough
	case "hang":
		<-limit.Done()
		outlived(t)
	case "leave a file open":
		dir := t.TempDir()
		NoOpenFiles(t, dir)
		var err error
		if leftOpen, err = os.Create(filepath.Join(dir, leftOpenName)); err != nil {
			t.Fatalf("making the file to leave open: %v", err)
		}
	case "leak":
		go func() { // it yields before it blocks, so it is still running when NoLeaks is called
			for range 1000 {
				runtime.Gosched()
			}
			<-make(chan int)
		}()
		t.Log("NoLeaks gave " + strconv.Itoa(NoLeaks(7)))
	case "settled":
		go func() {
			for range 1000 {
				runtime.Gosched()
			}
		}()
		t.Log("NoLeaks gave " + strconv.Itoa(NoLeaks(7)))
	case "spinning":
		go func() { // it never blocks and never ends: the profile alone reads it as live, never leaked
			for {
				runtime.Gosched()
			}
		}()
		t.Log("NoLeaks gave " + strconv.Itoa(NoLeaks(7)))
	}
	if os.Getenv("GITHUB_TOKEN") != "" {
		t.Fatal("the child inherited a leak")
	}
	t.Log("given " + strings.Join(args, "|"))
}

// A Rerun child sees the args it was given, spaces and all, runs only the helper, and inherits no leak.
//
// What it costs when this fails: a helper reads other args than its caller gave, another test runs in
// the child, or a token reaches it.
func TestARerunChildRunsOneTestWithWhatItWasGiven(t *testing.T) {
	// serial: it sets a variable of the process, which Rerun's child would inherit
	t.Setenv("GITHUB_TOKEN", "ghp_planted")
	code, out, err := Rerun(bounded(t), testCap, "a b", "c")
	if err != nil || code != 0 {
		t.Fatalf("the child: %d, %v\n%s", code, err, out)
	}
	if !strings.Contains(string(out), "given a b|c") {
		t.Errorf("the child did not see what it was given:\n%s", out)
	}
	if strings.Contains(string(out), "TestARerunChildRunsOneTestWithWhatItWasGiven") {
		t.Errorf("the child ran another test:\n%s", out)
	}
}

// A child whose test fails has a failing code and Rerun no error; a child that kills itself has code
// -1, its own exit error and, where a kill is a signal, SIGKILL read by Signal; elsewhere a code and no
// error. The child waits for its kill to land, and one ended at its deadline instead fails the row,
// so the kill read is the one the child sent.
//
// What it costs when this fails: a test cannot tell its helper failed, or a helper that must die at a
// chosen point is read as one that exited.
func TestAFailedChildIsACodeAndAKilledOneItsSignal(t *testing.T) {
	t.Parallel()
	code, out, err := Rerun(bounded(t), testCap, "fail")
	if err != nil || code == 0 || !strings.Contains(string(out), "the child fails as it was told") {
		t.Errorf("the failing child: %d, %v\n%s", code, err, out)
	}
	code, out, err = Rerun(bounded(t), testCap, "kill")
	if errors.Is(err, ErrEnded) {
		t.Fatalf("the child that killed itself was ended at its deadline, not by its kill: code %d, %v\n%s", code, err, out)
	}
	if err == nil {
		if code == 0 || killIsSignal {
			t.Errorf("the child that killed itself: code %d and no error\n%s", code, out)
		}
		return
	}
	// os.Kill, not syscall.SIGKILL: a caller of Signal compares with std's os names and imports no
	// syscall.
	if sig, ok := Signal(err); !ok || sig != os.Kill || code != -1 {
		t.Errorf("the killed child: code %d, signal %v (%v), err %v\n%s", code, sig, ok, err, out)
	}
	if sig, ok := Signal(errors.New("no exit")); ok || sig != nil {
		t.Errorf("Signal read %v (%v) from an error that is no exit, want nil and false", sig, ok)
	}
}

// A child that hangs is ended at its deadline, and Rerun says so.
//
// What it costs when this fails: a helper that hangs holds the whole test binary until go test's own
// timeout ends it, and no test says which.
func TestAHungChildIsEndedAtItsDeadline(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("release tier: waits out a 2 s deadline")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	code, out, err := Rerun(ctx, testCap, "hang")
	if !errors.Is(err, ErrEnded) || code != -1 {
		t.Errorf("the hung child: code %d, %v; want -1 and ErrEnded\n%s", code, err, out)
	}
}

// A goroutine planted to leak, even one that has not yet reached its block when NoLeaks is called,
// fails the run with what leaked on stderr; so does one that never stops running, which the profile
// alone counts as live; a goroutine that is still running and then ends passes, and
// the run's own code comes back.
//
// What it costs when this fails: a package's leak check passes a goroutine that never ends, or fails a
// run that leaked nothing.
func TestNoLeaksFailsALeakAndPassesASettledGoroutine(t *testing.T) {
	t.Parallel()
	_, out, err := Rerun(bounded(t), testCap, "leak")
	if err != nil || !strings.Contains(string(out), "NoLeaks gave 1") || !strings.Contains(string(out), "a goroutine leaked") {
		t.Errorf("the planted leak: %v\n%s", err, out)
	}
	_, out, err = Rerun(bounded(t), testCap, "spinning")
	if err != nil || !strings.Contains(string(out), "NoLeaks gave 1") || !strings.Contains(string(out), "were still not blocked") {
		t.Errorf("the goroutine that never stops: %v\n%s", err, out)
	}
	_, out, err = Rerun(bounded(t), testCap, "settled")
	if err != nil || !strings.Contains(string(out), "NoLeaks gave 7") {
		t.Errorf("the settled goroutine: %v\n%s", err, out)
	}
}

// The plant: a test binary started with the helper's variable set and a -test.run that
// is not HelperTest's is no child, so the variable alone makes no helper do what it was given. The
// probe below is red when Helper answers true, and its PASS line is read, so a run of no test is no
// green.
//
// What it costs when this fails: a variable exported before go test makes every package's helper test
// fail, kill itself or hang.
func TestAStrayVariableMakesNoRunAChild(t *testing.T) {
	t.Parallel()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	const probe = "TestAHelperVariableIsNotAChildOutsideItsRun"
	cmd := exec.CommandContext(t.Context(), self, "-test.run=^"+probe+"$", "-test.count=1", "-test.v") //nolint:gosec // this test binary itself
	cmd.Env = append(Clean(os.Environ()), helperVar+`=["fail"]`)
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "--- PASS: "+probe) {
		t.Errorf("the probe, with the variable set: %v\n%s", err, out)
	}
}

// TestAHelperVariableIsNotAChildOutsideItsRun is the probe of TestAStrayVariableMakesNoRunAChild.
func TestAHelperVariableIsNotAChildOutsideItsRun(t *testing.T) {
	t.Parallel()
	if args, ok := Helper(); ok {
		t.Fatalf("the variable alone made this run a child, given %q", args)
	}
}

// Every spelling of -test.run is read, the last of several winning, and none when there is none.
//
// What it costs when this fails: a child's run is misread and a stray variable makes a run a child.
func TestTestRunReadsTheLastRunFlagAsTheFlagPackageDoes(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		args []string
		want string
		ok   bool
	}{
		{[]string{"-test.v", "-test.count=1"}, "", false},
		{[]string{"-test.run=^A$"}, "^A$", true},
		{[]string{"--test.run=^A$"}, "^A$", true},
		{[]string{"-test.run", "^A$"}, "^A$", true},
		{[]string{"--test.run", "^A$"}, "^A$", true},
		{[]string{"-test.run=^A$", "-test.v", "-test.run=^B$"}, "^B$", true},
		{[]string{"-test.runner=^A$"}, "", false},
		{[]string{"--", "-test.run=^A$"}, "", false},
		{[]string{"-test.run"}, "", false},
	} {
		if got, ok := testRun(c.args); got != c.want || ok != c.ok {
			t.Errorf("testRun(%q) = %q, %v; want %q, %v", c.args, got, ok, c.want, c.ok)
		}
	}
}

// Link plants a link whose text is its target, and a file read through it is the file behind it.
//
// What it costs when this fails: a helper that plants nothing, and every test on it passes without the
// link it reads.
func TestALinkIsPlanted(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "target"), []byte("behind"), 0o600); err != nil {
		t.Fatal(err)
	}
	Link(t, "target", filepath.Join(dir, "link"))
	if got, err := os.Readlink(filepath.Join(dir, "link")); err != nil || got != "target" {
		t.Errorf("the link's text = %q, %v; want target", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "link")); err != nil || string(got) != "behind" {
		t.Errorf("read through the link = %q, %v; want behind", got, err)
	}
}

// With the probe refused, a link and a table of links each skip with NoLinks, go no further and
// plant nothing; with it answered, Links goes on. The refused cases run on a recording TB, not as
// subtests: a subtest that skips is a skip in the run's report, which the no-skip tiers fail.
//
// What it costs when this fails: a test on a system that makes no link goes on without its link and
// passes, or a table of link cases is reported green with nothing measured.
func TestARefusedProbeSkipsWithTheOneMessageAndPlantsNothing(t *testing.T) {
	t.Parallel()
	refused, dir := errors.New("refused"), t.TempDir()
	link := record(t, func(tb testing.TB) {
		tb.Helper()
		plant(tb, refused, "target", filepath.Join(dir, "link"))
		tb.Error("the test went on past a refused probe")
	})
	table := record(t, func(tb testing.TB) {
		tb.Helper()
		skipRefused(tb, refused)
		tb.Error("the table went on past a refused probe")
	})
	if link.skipped != NoLinks || table.skipped != NoLinks || link.failed != "" || table.failed != "" {
		t.Errorf("a refused probe: the link skipped with %q and failed with %q, the table %q and %q; want both skipped with NoLinks and neither failed",
			link.skipped, link.failed, table.skipped, table.failed)
	}
	if _, err := os.Lstat(filepath.Join(dir, "link")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a refused probe planted a link: %v", err)
	}
	if err := probeLinks(t.TempDir()); err != nil {
		if _, ok := errors.AsType[*os.LinkError](err); !ok {
			t.Fatalf("the probe was refused by %v, not by the system", err)
		}
		return
	}
	if answered := record(t, Links); answered.skipped != "" || answered.failed != "" {
		t.Errorf("Links on a system that makes links: skipped with %q, failed with %q", answered.skipped, answered.failed)
	}
}

// recorder is a testing.TB that keeps the first skip or failure of what it runs, and ends it there as
// a real one would, so no skip reaches the run's report. It keeps each line logged too, and passes it
// to t. Its TempDir and the rest are t's.
type recorder struct {
	testing.TB
	skipped, failed, logged string
}

// record runs f on a recorder of t, on a goroutine of its own, which a skip or a fatal failure ends.
func record(t *testing.T, f func(testing.TB)) *recorder {
	t.Helper()
	r := &recorder{TB: t}
	done := make(chan struct{})
	go func() {
		defer close(done)
		f(r)
	}()
	<-done
	return r
}

func (r *recorder) Helper()                           {}
func (r *recorder) Skip(args ...any)                  { r.skipped = fmt.Sprint(args...); runtime.Goexit() }
func (r *recorder) Skipf(format string, args ...any)  { r.Skip(fmt.Sprintf(format, args...)) }
func (r *recorder) SkipNow()                          { r.Skip("SkipNow") }
func (r *recorder) Error(args ...any)                 { r.failed += fmt.Sprint(args...) }
func (r *recorder) Errorf(format string, args ...any) { r.failed += fmt.Sprintf(format, args...) }
func (r *recorder) Fatal(args ...any)                 { r.Error(args...); runtime.Goexit() }
func (r *recorder) Fatalf(format string, args ...any) { r.Errorf(format, args...); runtime.Goexit() }
func (r *recorder) FailNow()                          { r.Fatal("FailNow") }
func (r *recorder) Log(args ...any)                   { r.Logf("%s", fmt.Sprint(args...)) }
func (r *recorder) Logf(format string, args ...any) {
	r.TB.Helper()
	r.logged += fmt.Sprintf(format, args...) + "\n"
	r.TB.Logf(format, args...)
}

// What a Rerun keeps of a stream is its first outputCap bytes. A write that only reaches the cap does
// not end the child; the first byte past it ends it with ErrOutputCap, and every write is taken whole.
//
// What it costs when this fails: a helper that floods its output fills the test's memory or runs on,
// a helper that prints exactly the cap is ended, or its writes fail and it dies of a broken pipe.
func TestARerunKeepsTheFirstBytesOfAStreamUpToItsCap(t *testing.T) {
	t.Parallel()
	var cause error
	c := capped{limit: testCap, end: func(err error) { cause = err }}
	for _, n := range []int{testCap - 3, 3} {
		if got, err := c.Write(make([]byte, n)); got != n || err != nil {
			t.Fatalf("a write of %d bytes took %d, %v", n, got, err)
		}
	}
	if cause != nil || len(c.b) != testCap {
		t.Errorf("writes that reach the cap and no further: kept %d bytes, ended with %v; want %d and not ended", len(c.b), cause, testCap)
	}
	for _, n := range []int{10, 5} {
		if got, err := c.Write(make([]byte, n)); got != n || err != nil {
			t.Fatalf("a write of %d bytes past the cap took %d, %v", n, got, err)
		}
	}
	if !errors.Is(cause, ErrOutputCap) || len(c.b) != testCap {
		t.Errorf("writes past the cap: kept %d bytes, ended with %v; want %d and ErrOutputCap", len(c.b), cause, testCap)
	}
}

// testCap is the output cap of a Rerun in these tests: 4 MiB of each stream, more than any helper
// prints but a flood.
const testCap = 4 << 20

// rerunBound is the deadline bounded gives a Rerun before Scale stretches it, and rerunStretch the
// most Scale stretches it.
const rerunBound, rerunStretch = time.Minute, 5

// helperLimit bounds each wait of the helper that only its parent ends: a minute past the longest
// deadline a test here gives its Rerun, so a parent that runs always ends its child first, and a
// child whose parent was killed before it could end it ends by itself.
const helperLimit = rerunBound*rerunStretch + time.Minute

// outlived ends the helper whose wait ran to helperLimit: the parent that was to end the wait is gone.
func outlived(t *testing.T) {
	t.Helper()
	t.Fatalf("the helper waited %s, past every deadline its parent gives: its parent is gone", helperLimit)
}

// bounded is the context of a Rerun in these tests: t's own, ended rerunBound on, stretched for -race
// at most rerunStretch times (testkit reads no load, so it passes none). Rerun takes its deadline from
// its caller and refuses a context with none.
func bounded(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), Scale(t, rerunBound, rerunStretch, 0))
	t.Cleanup(cancel)
	return ctx
}

// Rerun's bounds are its caller's, and it refuses to start a child without them: a context with no
// deadline, an output cap of zero and one below zero are each ErrRefused, with code -1 and nothing
// printed, since no child ran. A child that writes past a small cap is ended at once, with code -1,
// ErrOutputCap, not ErrEnded, and what it wrote up to the cap.
//
// What it costs when this fails: a helper that hangs or floods holds the test binary until go test's
// own timeout, or fills its memory, because a caller left a bound out and Rerun ran the child anyway.
func TestRerunsBoundsAreTheCallersAndAChildWithoutThemIsRefused(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		ctx       context.Context
		outputCap int
	}{
		"no deadline":    {t.Context(), testCap},
		"a zero cap":     {bounded(t), 0},
		"a negative cap": {bounded(t), -1},
	} {
		if code, out, err := Rerun(tc.ctx, tc.outputCap, "a"); !errors.Is(err, ErrRefused) || code != -1 || out != nil {
			t.Errorf("%s: code %d, %v, printed %q; want -1, ErrRefused and no child", name, code, err, out)
		}
	}
	const small = 1 << 10
	code, out, err := Rerun(bounded(t), small, "flood")
	if !errors.Is(err, ErrOutputCap) || errors.Is(err, ErrEnded) || code != -1 || len(out) == 0 || len(out) > 2*small {
		t.Errorf("the flood held to %d bytes: code %d, %v, printed %d bytes; want -1, ErrOutputCap and not ErrEnded, and at most %d of each stream", small, code, err, len(out), small)
	}
}

// interruptedLine is the helper's last line when the interrupt ends its wait. It then passes, with code 0,
// which neither a kill nor a failure has.
const interruptedLine = "the interrupt ended the wait"

// A RerunInterrupted child that waits on os.Interrupt and says it is ready is sent the system's interrupt
// and ends by it, its own way: its code, and its line last. A child that never says it is ready is not
// interrupted, and says so; one with no deadline is refused before it starts.
//
// What it costs when this fails: a test of what a program does when interrupted holds a Unix-only signal
// of its own, or one that sends nothing hangs until the deadline and reads as an end of the program's own.
func TestARerunChildIsInterruptedOnEverySystem(t *testing.T) {
	t.Parallel()
	code, out, err := RerunInterrupted(bounded(t), testCap, "interrupt")
	last := "" // the helper's last line: the last that its own t.Log wrote
	for line := range strings.Lines(string(out)) {
		if strings.Contains(line, "helpers_test.go:") {
			last = strings.TrimSpace(line)
		}
	}
	if err != nil || code != 0 || !strings.HasSuffix(last, interruptedLine) {
		t.Errorf("the interrupted child: code %d, %v, its last line %q; want 0, no error and %q\n%s", code, err, last, interruptedLine, out)
	}
	code, out, err = RerunInterrupted(bounded(t), testCap, "never ready")
	if !errors.Is(err, ErrNotInterrupted) || code != 0 || !strings.Contains(string(out), "given never ready") {
		t.Errorf("a child that never said it was ready: code %d, %v; want 0 and ErrNotInterrupted\n%s", code, err, out)
	}
	code, out, err = RerunInterrupted(t.Context(), testCap, "interrupt")
	if !errors.Is(err, ErrRefused) || code != -1 || out != nil {
		t.Errorf("no deadline: code %d, %v, printed %q; want -1, ErrRefused and no child", code, err, out)
	}
}

// heldBy is a hold of the test's around the system's: its interrupt fails with send when that is set,
// and its release with unreleased, and released records that it was released.
type heldBy struct {
	holder
	send, unreleased error
	released         *bool
}

func (h heldBy) interrupt() error {
	if h.send != nil {
		return h.send
	}
	return h.holder.interrupt()
}

func (h heldBy) release() error {
	*h.released = true
	return errors.Join(h.holder.release(), h.unreleased)
}

// A RerunInterrupted child that cannot be held, or whose interrupt cannot be sent, is ended at once, with
// code -1 and an error that is ErrNotInterrupted and says which; a hold whose release fails gives its
// error with the child's own end; the hold is taken before the child can be waited for, so a child that
// ends at once, never ready, is held still and ends its own way.
//
// What it costs when this fails: a child the interrupt cannot reach runs to its deadline and reads as one
// that hung, or a child that ended by itself reads as one that could not be held; on Windows a hold taken
// after the wait opens a pid that is gone, or a later process that took it.
func TestAHoldThatFailsEndsTheChildAndSaysSo(t *testing.T) {
	t.Parallel()
	planted := errors.New("planted")
	wrapped := func(send, release error, released *bool) func(*os.Process) (holder, error) {
		return func(p *os.Process) (holder, error) {
			h, err := hold(p)
			if err != nil {
				return nil, err
			}
			return heldBy{holder: h, send: send, unreleased: release, released: released}, nil
		}
	}
	unreaped := func(p *os.Process) (holder, error) { // a hold of a child already waited for fails, as Windows' would
		if err := p.Signal(syscall.Signal(0)); errors.Is(err, os.ErrProcessDone) {
			return nil, err
		}
		return hold(p)
	}
	t.Run("no hold", func(t *testing.T) {
		t.Parallel()
		code, out, err := rerun(bounded(t), testCap, nil, func(*os.Process) (holder, error) { return nil, planted }, childGrace, []string{"interrupt"})
		if !errors.Is(err, ErrNotInterrupted) || !errors.Is(err, planted) || errors.Is(err, ErrEnded) || code != -1 || !strings.Contains(fmt.Sprint(err), "could not be held") {
			t.Errorf("a child that could not be held: code %d, %v; want -1, ErrNotInterrupted that it could not be held, and not ErrEnded\n%s", code, err, out)
		}
	})
	t.Run("no send", func(t *testing.T) {
		t.Parallel()
		released := false
		code, out, err := rerun(bounded(t), testCap, nil, wrapped(planted, nil, &released), childGrace, []string{"interrupt"})
		if !errors.Is(err, ErrNotInterrupted) || !errors.Is(err, planted) || errors.Is(err, ErrEnded) || code != -1 || !strings.Contains(fmt.Sprint(err), "could not be sent") || !released {
			t.Errorf("a child whose interrupt could not be sent: code %d, %v, released %v; want -1, ErrNotInterrupted that it could not be sent, not ErrEnded, and released\n%s", code, err, released, out)
		}
	})
	t.Run("no release", func(t *testing.T) {
		t.Parallel()
		released := false
		code, out, err := rerun(bounded(t), testCap, nil, wrapped(nil, planted, &released), childGrace, []string{"interrupt"})
		if !errors.Is(err, planted) || errors.Is(err, ErrNotInterrupted) || code != 0 || !released {
			t.Errorf("a child whose hold's release failed: code %d, %v, released %v; want 0 and the release's error alone\n%s", code, err, released, out)
		}
	})
	t.Run("held before the wait", func(t *testing.T) {
		t.Parallel()
		code, out, err := rerun(bounded(t), testCap, nil, unreaped, childGrace, []string{"never ready"})
		if !errors.Is(err, ErrNotInterrupted) || code != 0 || !strings.Contains(fmt.Sprint(err), "ended before it said it was ready") {
			t.Errorf("a child that ended never ready: code %d, %v; want 0 and ErrNotInterrupted that it ended before it said it was ready\n%s", code, err, out)
		}
	})
}

// The ready line is a line of its own, however the stream is cut into writes; text around it, or a line
// that only holds it, is not it.
//
// What it costs when this fails: a child is interrupted before it can take the interrupt, and dies of
// it, or never is, and waits out its deadline.
func TestTheReadyLineIsALineOfItsOwn(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name   string
		writes []string
		want   bool
	}{
		{"alone", []string{"\n" + readyLine + "\n"}, true},
		{"after output", []string{"=== RUN x\n", readyLine + "\n"}, true},
		{"a byte a write", strings.Split(readyLine+"\n", ""), true},
		{"a carriage return", []string{readyLine + "\r\n"}, true},
		{"twice", []string{readyLine + "\n" + readyLine + "\n"}, true},
		{"no newline yet", []string{readyLine}, false},
		{"in a longer line", []string{"say " + readyLine + "\n"}, false},
		{"before more", []string{readyLine + " soon\n"}, false},
		{"after a long line", []string{strings.Repeat("y", 2*len(readyLine)) + readyLine + "\n"}, false},
		{"a part of it", []string{readyLine[:len(readyLine)-1] + "\n"}, false},
		{"a return inside a longer line", []string{readyLine + "\rmore\n"}, false},
		{"a long line not ended", []string{strings.Repeat("y", 2*len(readyLine)), strings.Repeat("y", 3)}, false},
	} {
		r := &ready{seen: make(chan struct{})}
		for _, w := range row.writes {
			if n, err := r.Write([]byte(w)); n != len(w) || err != nil {
				t.Fatalf("%s: a write took %d of %d bytes, %v", row.name, n, len(w), err)
			}
			if len(r.line) > len(readyLine)+1 {
				t.Errorf("%s: %d bytes of a line kept, more than the ready line and a return", row.name, len(r.line))
			}
		}
		select {
		case <-r.seen:
			if !row.want {
				t.Errorf("%s: read as the ready line", row.name)
			}
		default:
			if row.want {
				t.Errorf("%s: not read as the ready line", row.name)
			}
		}
	}
}

// Ready writes its line in a child of Rerun alone; in a run that go test started it writes nothing.
//
// What it costs when this fails: every test that calls Ready outside a helper prints a line to the run's
// output that only a parent waiting on it should read.
func TestReadyWritesNothingOutsideAChild(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	if err := readyTo(&b); err != nil || b.Len() != 0 {
		t.Errorf("Ready in a run go test started: %v, wrote %q; want nothing", err, b.String())
	}
}
