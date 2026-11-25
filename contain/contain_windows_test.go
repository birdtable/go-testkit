package contain

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestKillOnCloseJobRefusesANameAlreadyAJobs: a Job's name is one process's to kill on close. Where
// CreateJobObjectW opens a Job the name already has (ERROR_ALREADY_EXISTS) instead of making a new one,
// killOnCloseJob closes that handle and refuses, so rerunContained reports it as Refused and never hands
// the run's processes a Job another process made and may close. The break that drops the
// ERROR_ALREADY_EXISTS check turns this row red.
//
// What it costs when this fails: a contained run's processes join a Job some other process owns, set to
// kill on close by nobody this run controls, so its tree outlives the run or dies before it.
func TestKillOnCloseJobRefusesANameAlreadyAJobs(t *testing.T) {
	t.Parallel()
	name := fmt.Sprintf("Local\\testkit-dup-%d-%d", os.Getpid(), time.Now().UnixNano())
	first, err := killOnCloseJob(name)
	if err != nil {
		t.Fatalf("the first Job of the name: %v", err)
	}
	t.Cleanup(func() { _ = syscall.CloseHandle(first) })
	second, err := killOnCloseJob(name)
	if err == nil {
		_ = syscall.CloseHandle(second)
		t.Fatal("killOnCloseJob took a name a Job already has; want it refused")
	}
	if !strings.Contains(err.Error(), "already") {
		t.Errorf("the refusal %q does not say the name is a Job's already", err)
	}
}

// TestInJobAsksTheNamedJobNotAnyJob: a contained run is in contain's own Job, so inJob of a different
// named Job, one the row makes and never puts this process in, is false. It asks IsProcessInJob of that
// named Job alone; a check of "any Job" (IsProcessInJob with a nil handle) would see contain's Job and
// wrongly say yes. The break that returns inJob to IsProcessInJob(self, 0) turns this row red.
//
// What it costs when this fails: a contained test that checks it is inside contain's containment could
// pass while in some other Job, a runner's or a terminal's, so a test that needs the containment runs
// without it and its isolation is a fiction.
func TestInJobAsksTheNamedJobNotAnyJob(t *testing.T) {
	t.Parallel()
	Contained(t)
	name := fmt.Sprintf("Local\\testkit-row-%d-%d", os.Getpid(), time.Now().UnixNano())
	job, err := killOnCloseJob(name)
	if err != nil {
		t.Fatalf("the row's own Job could not be made: %v", err)
	}
	t.Cleanup(func() { _ = syscall.CloseHandle(job) })
	if inJobBy(name, 200*time.Millisecond) {
		t.Errorf("inJob(%q) is true for a Job this process was never put in; it must ask the named Job, not any Job (IsProcessInJob with a nil handle)", name)
	}
}

// TestAContainedRunIsInAJobAndItsTreeEndsWhenTheJobCloses: in a contained run the test process is in a
// Job Object, and a Job made to kill on close ends the tree of a process put in it when its handle closes.
// Planted failure: a Job made with no kill on close, whose process outlives the handle. Risk: a signalling
// test whose tree outlives the run, or whose signal reaches a process outside it.
func TestAContainedRunIsInAJobAndItsTreeEndsWhenTheJobCloses(t *testing.T) {
	t.Parallel()
	Contained(t)
	c := inside.Load()
	if c == nil || !inJob(c.arg) {
		t.Fatal("the tests of a contained run are not in the Job Object contain made")
	}
	job, err := killOnCloseJob(fmt.Sprintf("Local\\testkit-test-%d", os.Getpid()))
	if err != nil {
		t.Fatalf("a Job Object of its own could not be made inside the run's: %v", err)
	}
	open := true
	t.Cleanup(func() {
		if open {
			_ = syscall.CloseHandle(job)
		}
	})
	cmd := exec.CommandContext(t.Context(), "cmd.exe", "/c", "ping -n 60 127.0.0.1")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	h, err := syscall.OpenProcess(processSetQuota|processTerminate, false, uint32(cmd.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	if err := assign(job, h); err != nil {
		t.Fatalf("the child could not be put in its Job: %v", err)
	}
	_ = syscall.CloseHandle(h)
	ended := make(chan error, 1)
	go func() { ended <- cmd.Wait() }()
	open = false
	if err := syscall.CloseHandle(job); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ended:
	case <-time.After(20 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("the process in the Job was still running 20 s after the Job's handle closed")
	}
}
