// Package contain reruns a test binary inside a containment, so a test that signals a process reaches no
// process outside it: a user and PID namespace on Linux, a Job Object on Windows. It is a module of its
// own beside github.com/birdtable/go-testkit, whose Main it is given to, so that a test binary that only
// isolates links none of it: on Linux it holds the capability calls of libcap/cap, and in a build with cgo
// the C of libcap/psx.
//
// It is built on testkit's Main, and adds, on Linux, an init that reaps what the tests orphan, tests
// that hold no capability, and a skip, with the reason, of each test that needs the containment where
// there is none. The app configures Contain or ContainMounts in its TestMain, and Contained or
// ContainedMounts in each test that signals or mounts.
package contain

import (
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/birdtable/go-testkit"
)

// The three reasons Contained skips a test with. A test that signals a process calls Contained first,
// so it never runs where a signal could reach a process that is no part of the test.
const (
	// NotAsked is the reason in a run whose Main was not given Contain.
	NotAsked = "not contained: the test binary's Main was not given contain.Contain, and a test that signals a process does not run uncontained"
	// NoContainment is the reason on a system that has no containment contain makes.
	NoContainment = "not contained: this system has no containment contain makes (a PID namespace on Linux, a Job Object on Windows)"
	// Refused begins the reason in a run whose system refused the containment: the cause follows it.
	Refused = "not contained: this system refused the containment contain asked of it: "
)

// containVar carries the part of the containment a process is in, from the process that made it to the
// one it starts. It is no name testkit's Isolate keeps, so a test never sees it; testkit's Rerun hands it
// on to its child, by the RerunEnv that Contain gives Main.
const containVar = "TESTKIT_CONTAINED"

// The values of containVar. Linux: the first run starts the binary as the init of a namespace of its
// own, which starts it once more as the tests; Windows: the first run starts the binary in a Job it made.
const (
	partInit   = "init"   // the binary as PID 1 of the namespace: reaps, and runs the tests as its child
	partRun    = "run"    // the binary as the tests, inside the containment
	partMounts = "mounts" // the binary as the tests of a ContainMounts run, whose initialization keeps the capability
)

// keptAtInit is what contain's initialization found in the tests of a ContainMounts run: ran is set only once
// it ran keepMounts and keepMounts succeeded, and why is then keepMounts' reason. Its zero value is that it did
// not run, never that the run holds the capability, so Main runs keepMounts itself in a run it finds unset.
var keptAtInit initKept //nolint:gochecknoglobals // set once, by contain's own initialization, and read by Main

// initKept is what keptAtInit holds.
type initKept struct {
	ran bool
	why string
}

// containment is what a run knows of its containment: why a test that needs it is skipped, or none.
type containment struct {
	skip string // why this run is not contained, or "" when it is
	arg  string // what the process that made the containment told this run to check it by

	mounts    bool   // the tests hold CAP_SYS_ADMIN of the namespace, and nothing they start inherits it (ContainMounts)
	mountsWhy string // why they do not, when mounts is false
}

// notAskedMounts is why ContainedMounts skips in a contained run whose Main was given Contain, not ContainMounts.
const notAskedMounts = "no mount capability: the test binary's Main was given contain.Contain, not contain.ContainMounts, so the tests hold no capability and a test that mounts does not run"

// inside is the containment of the run in force, set by Main before any test runs.
var inside atomic.Pointer[containment] //nolint:gochecknoglobals // the containment of this process, which Contained reads from parallel tests; Main writes it once, before them

// Contain is an option of testkit's Main: testkit.Main(m, contain.Contain()). It makes Main rerun the
// whole test binary inside a containment before any test runs, so a test
// that signals a process reaches no process outside it. On Linux the binary reruns in a user and PID
// namespace with a private mount and a fresh /proc, as PID 1, which reaps what its tests orphan; on
// Windows, in a kill-on-close Job Object, a process group of its own and a console with no window,
// the child waiting until it is in the Job. The rerun child is given the process's arguments,
// working folder, streams and environment, and Main returns its exit code without running a test
// itself. A system that refuses the containment (a host with no user namespaces, as many CI
// containers are) runs the tests uncontained; a test that calls Contained then skips, and says why, so
// no test that needs the containment runs without it. The uncontained fallback applies to the tests
// that do not call Contained. On Linux the tests hold no capability of the namespace: its init drops
// what it held, on the one thread it starts them from, and sets and locks SECBIT_NOROOT and
// SECBIT_NO_SETUID_FIXUP first, so tests that are uid 0 in the namespace, and what they start, regain
// none on execve, and empties the bounding set, so no program they start gains one from its file
// capabilities (see ContainMounts, which keeps one); a program whose file capabilities carry the
// effective flag, as a distribution's ping or newuidmap does, is refused at execve with EPERM instead.
// Init also makes itself not dumpable, so no test and nothing it starts opens init's memory or maps, or
// ptraces it. Contain leaves no_new_privs as the run has it: a program a test starts has the test's own,
// which is what a test of a program's no_new_privs reads.
func Contain() testkit.Option { return option(false) }

// option is Contain, and ContainMounts when mounts: Main runs contain before it isolates, and each Rerun
// child of the run is given the containment the run is in.
func option(mounts bool) testkit.Option {
	return testkit.Options(
		testkit.Before(func(start []string) (int, bool, error) { return contain(start, mounts) }),
		testkit.RerunEnv(rerunEnv),
	)
}

// Contained checks that this test runs inside the containment Contain made, by the run itself and never
// by a variable of the environment a test could set, and skips t, with the reason, where it does not:
// NotAsked, NoContainment, or Refused and the system's own words.
func Contained(t testing.TB) {
	t.Helper()
	if why := notContainedBy(inside.Load()); why != "" {
		t.Skip(why)
	}
}

// ContainMounts is Contain, and on Linux the tests' process keeps CAP_SYS_ADMIN of Contain's own user
// namespace, effective and permitted, in a test binary built with cgo or without, and so under -race too,
// which links cgo: libcap/cap changes a capability on every thread of the process in both builds, so it
// is kept from what the tests start in both.
//
// It gives a test mount and unmount in Contain's private mount namespace, of any filesystem the kernel
// lets a user namespace mount (a tmpfs, a bind mount), and nothing of the host's: Contain exists only
// where the host lets an unprivileged process make a user namespace, and there any such process can
// hold CAP_SYS_ADMIN of a namespace of its own, so the capability reaches no host mount, no host
// capability and no namespace of the host's. What it adds is power over Contain's own namespace: a test
// that unmounts Contain's /proc leaves a process started after it (Rerun's child, for one) finding
// itself not contained, and that process's Contained skips. Everything else of Contain is unchanged:
// the one-account map, setgroups denied, the PID namespace, the private mounts, the fresh /proc, PID 1
// reaping. No other capability is kept, and nothing the tests start inherits this one: from contain's
// initialization, which runs before any package that imports contain and before TestMain, the ambient
// and inheritable sets are empty on every thread of the tests' process, and the
// namespace's init has set and locked SECBIT_NO_CAP_AMBIENT_RAISE, so nothing in the run raises it
// again. A package that does not import contain may initialize before it, and a program that package's
// initialization starts holds the capability. Init also sets no_new_privs before it starts the tests, so no execve in the run gains from
// file capabilities or set-id bits. Contain alone does not set it: a run that set it for every program
// would leave a test of a program's own no_new_privs reading only what the run set.
//
// Under Contain and ContainMounts alike, init sets and locks SECBIT_NOROOT and SECBIT_NO_SETUID_FIXUP
// before it starts the tests, so where the tests are uid 0 in the namespace (a test binary run as root,
// or under unshare --map-root-user, maps that uid to itself) neither they nor what they start regain
// the namespace's capabilities on execve, and it empties the bounding set, so a program with file
// capabilities gains none of them, and one whose file capabilities carry the effective flag is refused
// at execve with EPERM. Under ContainMounts, init passes CAP_SYS_ADMIN to the tests' execve in the
// ambient set, which the bounding set does not mask, so the tests start holding it, and contain's
// initialization then empties the ambient set, as above. A test of such a run cannot contain a test binary it starts,
// whose Contained skips with Refused: Linux maps uid 0 of a new user namespace to uid 0 only for a process
// with CAP_SETFCAP. Elsewhere than Linux, ContainMounts is Contain.
func ContainMounts() testkit.Option { return option(true) }

// ContainedMounts is Contained, and skips t unless this run holds the mount capability ContainMounts
// keeps, with the reason: Contained's when the run is not contained, that Main was given Contain and
// not ContainMounts, or Refused and why (a process a test started, which holds no capability). A test
// that mounts calls it first, so a run that cannot mount says so
// instead of passing empty. Elsewhere than Linux it skips with NoContainment.
func ContainedMounts(t testing.TB) {
	t.Helper()
	if why := noMountsBy(mountsHere, inside.Load()); why != "" {
		t.Skip(why)
	}
}

// noMountsBy is the reason a run with the containment c holds no mount capability, or "" when it holds it;
// here is whether the system gives a containment mounts at all.
func noMountsBy(here bool, c *containment) string {
	switch {
	case !here:
		return NoContainment
	case c == nil:
		return NotAsked
	case c.skip != "":
		return c.skip
	case c.mounts:
		return ""
	case c.mountsWhy != "":
		return c.mountsWhy
	}
	return notAskedMounts
}

// rerunEnv is what testkit's Rerun adds to the environment of each child of this run: containVar, so the
// child checks itself against the same containment and is in it, not in a new one; and nothing when this
// run is not contained.
func rerunEnv() []string {
	c := inside.Load()
	if c == nil || c.skip != "" {
		return nil
	}
	if c.arg != "" {
		return []string{containVar + "=" + partRun + ":" + c.arg}
	}
	return []string{containVar + "=" + partRun}
}

// lookup is the last value environ gives the variable named k, which is the one a process reads, with
// names matched as this system does: in any case on Windows, and exactly elsewhere.
func lookup(environ []string, k string) string {
	v := ""
	for _, kv := range environ {
		if name, value, _ := strings.Cut(kv, "="); name == k || foldNames && strings.EqualFold(name, k) {
			v = value
		}
	}
	return v
}

// notContainedBy is the reason a run with the containment c is not contained, or "" when it is.
func notContainedBy(c *containment) string {
	if c == nil {
		return NotAsked
	}
	return c.skip
}

// contain is what Main does first when it was given Contain, and mounts set when it was given
// ContainMounts: it returns the exit code of a run it made inside the containment, and done set, or sets
// inside as this run is, with the tests to run here.
func contain(start []string, mounts bool) (code int, done bool, err error) {
	mode, arg, _ := strings.Cut(lookup(start, containVar), ":")
	switch mode {
	case partRun, partMounts:
		// The variable only says what to check. The run is contained when it finds, by itself, that it is in the
		// containment: a stray value in a shell or a CI environment makes none, and the check fails.
		if entered(arg) {
			c := &containment{arg: arg}
			if mounts {
				// What the run holds is found by the run, on its own threads, never by the variable: by contain's
				// initialization in the tests init started, or here in a process a contained run started, and here too
				// in the tests whose initialization did not run it (it found the run not entered, as one whose /proc
				// was covered then does): this fails closed as keepMounts does.
				why, err := keptAtInit.why, error(nil)
				if mode != partMounts || !keptAtInit.ran {
					why, err = keepMounts()
				}
				if err != nil {
					return 1, true, err
				}
				c.mounts, c.mountsWhy = why == "", why
			}
			inside.Store(c)
			return 0, false, nil
		}
		if strayEnds {
			// The child of a Job is started to wait for its place in it, and a child that is not in one ends here.
			return 1, true, errors.New("the tests were started to run in contain's Job Object and were not put in it, so none ran")
		}
	case partInit:
		code, err = supervise(mounts)
		return code, true, err
	}
	code, refused, err := rerunContained(start) // a run that no containment started, or whose variable was no containment's word
	if refused == "" {
		return code, true, err
	}
	inside.Store(&containment{skip: refused})
	return 0, false, nil
}
