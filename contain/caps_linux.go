package contain

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"

	"kernel.org/pub/linux/libs/security/libcap/cap"
)

// The prctl options of linux/prctl.h that cap has no call of its own for, each with its name there.
const (
	prSetDumpable     = 4  // PR_SET_DUMPABLE
	prSetNoNewPrivs   = 38 // PR_SET_NO_NEW_PRIVS
	prGetNoNewPrivs   = 39 // PR_GET_NO_NEW_PRIVS
	prCapAmbient      = 47 // PR_CAP_AMBIENT
	prCapAmbientClear = 4  // PR_CAP_AMBIENT_CLEAR_ALL
)

// mountsNoCap is the reason a ContainMounts run holds no capability, after Refused.
const mountsNoCap = "this process was not started with CAP_SYS_ADMIN: only the tests contain's init starts hold it, not a process a test starts"

// testsBits are the securebits init sets, locked, before it starts the tests: no capability for uid 0
// on execve and none kept or gained by a uid change, and, under ContainMounts, no ambient raise.
func testsBits(mounts bool) cap.Secbits {
	bits := cap.SecbitNoRoot | cap.SecbitNoRootLocked | cap.SecbitNoSetUIDFixup | cap.SecbitNoSetUIDFixupLocked
	if mounts {
		bits |= cap.SecbitNoCapAmbientRaise | cap.SecbitNoCapAmbientRaiseLocked
	}
	return bits
}

// bounding is the calling thread's bounding set, one bit per capability the kernel knows.
func bounding() (uint64, error) {
	var set uint64
	for c := range cap.Value(64) {
		in, err := cap.GetBound(c)
		if errors.Is(err, syscall.EINVAL) {
			return set, nil // past the last capability the kernel knows
		}
		if err != nil {
			return set, err
		}
		if in {
			set |= 1 << c
		}
	}
	return set, nil
}

// dropForTests is what init does on the one thread it starts the tests from, since a thread's
// capabilities, securebits, bounding set and no_new_privs are its own and a child takes those of the
// thread that forks it. It runs in the callback of the cap.Launcher that starts the tests, where each
// cap call changes the launching thread alone and no other thread of init. mounts is whether
// ContainMounts was asked, and so whether the tests' exec carries CAP_SYS_ADMIN. It returns why it could
// not, or "".
func dropForTests(mounts bool) string {
	c, err := cap.GetPID(0)
	if err != nil {
		return "the namespace's capabilities could not be read: " + err.Error()
	}
	// The inheritable set is what the exec carries, which lowers every other capability from the ambient set.
	if err := inheritOnly(c, mounts); err != nil {
		return "the namespace's inheritable capabilities could not be set: " + err.Error()
	}
	if mounts {
		if err := cap.SetAmbient(true, cap.SYS_ADMIN); err != nil {
			return "CAP_SYS_ADMIN could not be raised for the tests: " + err.Error()
		}
	} else if _, err := cap.Prctlw(prCapAmbient, prCapAmbientClear, 0); err != nil {
		return "the namespace's capabilities could not be dropped: " + err.Error()
	}
	// The securebits, after the raise, while init still holds CAP_SETPCAP, which setting them takes.
	bits := testsBits(mounts)
	if err := (cap.GetSecbits() | bits).Set(); err != nil {
		return "the securebits the tests are started under could not be set: " + err.Error()
	}
	if why := narrowExec(mounts); why != "" {
		return why
	}
	// Init's own sets to what the exec carries: CAP_SETPCAP, and every capability of a uid-0 init, go.
	last, err := adminOf(c, mounts, cap.Effective, cap.Permitted, cap.Inheritable)
	if err == nil {
		err = last.SetProc()
	}
	if err != nil {
		return "the namespace's capabilities could not be dropped: " + err.Error()
	}
	return heldForTests(mounts, bits)
}

// inheritOnly sets c's inheritable set to CAP_SYS_ADMIN alone when keep, and to nothing when not, and
// sets the calling thread's sets to c.
func inheritOnly(c *cap.Set, keep bool) error {
	if err := c.ClearFlag(cap.Inheritable); err != nil {
		return err
	}
	if keep {
		if err := c.SetFlag(cap.Inheritable, true, cap.SYS_ADMIN); err != nil {
			return err
		}
	}
	return c.SetProc()
}

// adminOf is a set that holds CAP_SYS_ADMIN in each of flags where c holds it and keep is set, and
// nothing else.
func adminOf(c *cap.Set, keep bool, flags ...cap.Flag) (*cap.Set, error) {
	out := cap.NewSet()
	for _, f := range flags {
		had, err := c.GetFlag(f, cap.SYS_ADMIN)
		if err != nil {
			return nil, err
		}
		if err := out.SetFlag(f, had && keep, cap.SYS_ADMIN); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// narrowExec empties the calling thread's bounding set, while init still holds CAP_SETPCAP: it masks
// what a program's file capabilities give on execve, and not the ambient set, so CAP_SYS_ADMIN still
// reaches the tests. Under ContainMounts it also sets no_new_privs, so no execve in the run gains from
// file capabilities or set-id bits; plain Contain leaves it as the run has it, which a row reads of a
// program a test starts. It returns why it could not, or "".
func narrowExec(mounts bool) string {
	held, err := bounding()
	if err != nil {
		return "the bounding set could not be read: " + err.Error()
	}
	for c := cap.Value(0); held>>c != 0; c++ {
		if held>>c&1 == 0 {
			continue
		}
		if err := cap.DropBound(c); err != nil {
			return fmt.Sprintf("capability %d could not be dropped from the bounding set: %v", c, err)
		}
	}
	if mounts {
		if _, err := cap.Prctlw(prSetNoNewPrivs, 1); err != nil {
			return "no_new_privs could not be set for the tests: " + err.Error()
		}
	}
	return ""
}

// heldForTests is why the calling thread is not as dropForTests left it, the securebits bits set, the
// bounding set empty, no_new_privs set under ContainMounts, and CAP_SYS_ADMIN ambient under ContainMounts
// and no other capability, or "".
func heldForTests(mounts bool, bits cap.Secbits) string {
	if got := cap.GetSecbits(); got&bits != bits {
		return fmt.Sprintf("the securebits the tests are started under did not hold: %#x, want %#x set", uint(got), uint(bits))
	}
	if got, err := bounding(); err != nil || got != 0 {
		return fmt.Sprintf("the bounding set the tests are started with did not hold: %#x (%v), want it empty", got, err)
	}
	if mounts {
		if got, err := cap.Prctl(prGetNoNewPrivs); err != nil || got != 1 {
			return fmt.Sprintf("no_new_privs the tests are started with did not hold: %d (%v), want 1", got, err)
		}
	}
	for _, c := range []struct {
		value cap.Value
		want  bool
	}{{cap.SYS_ADMIN, mounts}, {cap.SETPCAP, false}} {
		if got, err := cap.GetAmbient(c.value); err != nil || got != c.want {
			return fmt.Sprintf("the ambient set the tests are started with did not hold: capability %d is %t, want %t", c.value, got, c.want)
		}
	}
	return ""
}

// init runs keepMounts in the tests of a ContainMounts run, from contain's initialization: before any
// package that imports contain, and so before TestMain, starts a program that would inherit what init
// passed the tests. A run that holds a capability it cannot keep from what the tests start ends here.
func init() { //nolint:gochecknoinits // the ambient clear runs before the code of any package that imports contain
	mode, arg, _ := strings.Cut(lookup(os.Environ(), containVar), ":")
	if mode != partMounts || !entered(arg) {
		return
	}
	why, err := keepMounts()
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "contain:", err) //nolint:forbidigo // the one line of a run that ends before its TestMain
		os.Exit(1)                                      //nolint:forbidigo // fails closed, as keepMounts does, before any test or TestMain runs
	}
	keptAtInit.why, keptAtInit.ran = why, true
}

// keepMounts is what contain's initialization does in the tests of a ContainMounts run: it keeps
// CAP_SYS_ADMIN effective and permitted, and nothing else, and empties the inheritable and ambient sets on
// every thread, so nothing a test starts inherits it. cap sets them on every thread of the process, in a
// binary that links cgo too (a -race build does), where Go's own call for every thread refuses. It
// returns why the run holds no capability, after Refused, or "" when it holds it, and an error when it
// holds one it cannot keep from what the tests start, and no test may run.
func keepMounts() (string, error) {
	c, err := cap.GetPID(0)
	if err != nil {
		return "", fmt.Errorf("the tests could not read their capabilities, so none ran: %w", err)
	}
	diff, err := c.Cf(cap.NewSet())
	if err != nil {
		return "", fmt.Errorf("the tests could not read their capabilities, so none ran: %w", err)
	}
	held := diff.Has(cap.Effective) || diff.Has(cap.Permitted)
	kept, err := adminOf(c, true, cap.Effective, cap.Permitted)
	if err != nil {
		return "", fmt.Errorf("the tests could not read their capabilities, so none ran: %w", err)
	}
	if err := kept.SetProc(); err != nil {
		if held {
			return "", fmt.Errorf("the tests hold a capability that could not be kept from what they start (%w), so none ran", err)
		}
		return Refused + "the tests' capabilities could not be set on every thread: " + err.Error(), nil
	}
	if _, err := cap.Prctlw(prCapAmbient, prCapAmbientClear, 0); err != nil {
		return "", fmt.Errorf("the tests' ambient capabilities could not be cleared on every thread (%w), so none ran", err)
	}
	if amb, err := cap.GetAmbient(cap.SYS_ADMIN); err != nil || amb {
		return "", errors.New("the tests' ambient set still holds CAP_SYS_ADMIN, so none ran")
	}
	for _, f := range []cap.Flag{cap.Effective, cap.Permitted} {
		if has, err := kept.GetFlag(f, cap.SYS_ADMIN); err != nil || !has {
			return Refused + mountsNoCap, nil
		}
	}
	return "", nil
}

// mountsHere is true: Linux gives a ContainMounts run the mounts of its own namespace.
const mountsHere = true
