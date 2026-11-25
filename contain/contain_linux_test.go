package contain

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestAContainedRunIsInitsChildAndInitReapsWhatItOrphans: in a contained run the test process is a
// child of PID 1, which is the binary, and a process a test orphans is reaped there, not left a
// zombie. Planted failure: a run with no init, where the orphan stays until the system's own init takes it.
// Risk: a signalling test whose orphans pile up, or whose signal reaches a process outside the run.
func TestAContainedRunIsInitsChildAndInitReapsWhatItOrphans(t *testing.T) {
	t.Parallel()
	Contained(t)
	if got := os.Getppid(); got != 1 {
		t.Fatalf("the parent of the tests is process %d, want PID 1, the binary as the namespace's init", got)
	}
	if got := os.Getpid(); got == 1 {
		t.Fatal("the tests are PID 1, which reaps nothing a test's Wait does not")
	}
	out, err := exec.CommandContext(t.Context(), "/bin/sh", "-c", "sleep 0.3 & echo $!").Output()
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatalf("the orphan's id: %v", err)
	}
	status := "/proc/" + strconv.Itoa(pid) + "/status"
	if b, err := os.ReadFile(status); err == nil && !strings.Contains(string(b), "PPid:\t1\n") {
		t.Fatalf("the orphan's parent is not PID 1: %s", b)
	}
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for deadline := time.After(10 * time.Second); ; {
		if _, err := os.Stat(status); err != nil {
			return
		}
		select {
		case <-tick.C:
		case <-deadline:
			b, _ := os.ReadFile(status)
			t.Fatalf("the orphan %d was never reaped by PID 1: %s", pid, b)
		}
	}
}

// TestARefusedNamespaceSkipsAndFailsNothing: where the system refuses the namespace Contain asks for, the
// run goes on uncontained, and the test that needs the containment skips with the system's own words.
// Planted failure: Main ending on the refusal. Risk: a host that refuses user namespaces, as CI
// containers do, fails every package that asks for Contain.
func TestARefusedNamespaceSkipsAndFailsNothing(t *testing.T) { //nolint:paralleltest // serial: it sets the flags the namespace is made with, and the containment in force, which every Contained reads
	was, flags := inside.Load(), cloneflags
	t.Cleanup(func() { inside.Store(was); cloneflags = flags })
	cloneflags = syscall.CLONE_THREAD // a flag no clone takes alone: the system answers it with a refusal, as it does a user namespace it will not make
	code, done, err := contain(nil, false)
	if done || err != nil || code != 0 {
		t.Fatalf("a refused namespace gave code %d, done %v, error %v; want the tests to run here", code, done, err)
	}
	why := notContainedBy(inside.Load())
	if !strings.HasPrefix(why, Refused) || len(why) == len(Refused) {
		t.Fatalf("the reason is %q, want %q and the system's words", why, Refused)
	}
	r := record(t, Contained)
	if r.skipped != why || r.failed != "" {
		t.Errorf("Contained skipped with %q and failed with %q, want a skip with %q", r.skipped, r.failed, why)
	}
}

// TestAContainedRunFindsByItselfThatItIsInTheNamespace: the run finds that it is in the namespace by its own PID
// namespace and by PID 1's name on the namespace's own /proc, and holds that to what init told it: a stray
// TESTENV_CONTAINED=run, a namespace that is not the one named, and a PID 1 of another program are none
// of them a contained run. Planted failure: entered true on the variable alone, or no fresh /proc, where
// PID 1 is the system's init. Risk: a stray value in a shell or CI environment runs every signalling test
// uncontained.
func TestAContainedRunFindsByItselfThatItIsInTheNamespace(t *testing.T) {
	t.Parallel()
	Contained(t)
	c := inside.Load()
	ns, err := os.Readlink("/proc/self/ns/pid")
	if err != nil {
		t.Fatal(err)
	}
	if c.arg != ns {
		t.Fatalf("init gave the namespace %q, and this run is in %q", c.arg, ns)
	}
	if !enteredBy(ns, "/proc/self/ns/pid", "/proc/1/stat") {
		t.Error("a contained run does not find that it is in the namespace")
	}
	other := filepath.Join(t.TempDir(), "stat")
	if err := os.WriteFile(other, []byte("1 (systemd) S 0 1 1 0 -1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, got := range map[string]bool{
		"no namespace named, a stray value": enteredBy("", "/proc/self/ns/pid", "/proc/1/stat"),
		"another namespace named":           enteredBy("pid:[1]", "/proc/self/ns/pid", "/proc/1/stat"),
		"a PID 1 of another program":        enteredBy(ns, "/proc/self/ns/pid", other),
	} {
		if got {
			t.Errorf("%s: found to be in the namespace", name)
		}
	}
}

// TestEnteredIsFalseForANamespaceThisRunIsNotIn: entered itself, on the real /proc, is false for a value that
// is not the link of this run's PID namespace: another namespace's link, this one's with a suffix, an empty value
// and a value that is no link at all. It does not need a containment, so it runs in a contained run and outside
// one. Planted failure: entered true on the variable alone, or on any non-empty value. Risk: a stray
// TESTENV_CONTAINED=run:<link> in a shell or a CI environment runs every signalling test uncontained.
func TestEnteredIsFalseForANamespaceThisRunIsNotIn(t *testing.T) {
	t.Parallel()
	ns, err := os.Readlink("/proc/self/ns/pid")
	if err != nil {
		t.Fatal(err)
	}
	for name, arg := range map[string]string{
		"another namespace's link":     "pid:[1]",
		"this namespace's link, extra": ns + "x",
		"empty":                        "",
		"no link at all":               "run",
	} {
		if entered(arg) {
			t.Errorf("%s (%q): found to be in the namespace %q", name, arg, ns)
		}
	}
}

// TestTheTestsInAContainedRunHoldNoCapability: the namespace's init is given CAP_SYS_ADMIN to mount /proc and
// drops it, with CAP_SETPCAP, before it starts the tests, so the tests' effective, permitted, inheritable
// and ambient sets are all empty, and their bounding set too, as uid 0 too
// (TestAsUID0TheContainedTestsAndTheirChildrenHoldNoCapability runs this row so). Planted failures: init's
// inheritable set left as it was (the tests hold CAP_SYS_ADMIN and CAP_SETPCAP inheritable); init passing
// CAP_SYS_ADMIN whatever the option (the tests hold it effective); init leaving the bounding set as it was
// (CapBnd is the namespace's full set). Risk: a test that signals, mounts or changes the namespace as an
// administrator of it, or starts a program whose file capabilities make it one.
func TestTheTestsInAContainedRunHoldNoCapability(t *testing.T) {
	t.Parallel()
	Contained(t)
	t.Logf("%s%d", uidLine, os.Getuid())
	wantCaps(t, "a test of a contained run", status(t, "/proc/self/status", "Cap"), map[string]uint64{"CapEff": 0, "CapPrm": 0, "CapInh": 0, "CapAmb": 0, "CapBnd": 0})
}
