//go:build linux

package testkit

import (
	"syscall"
	"testing"
)

// The refusals forceRefusal forces on Linux, beside plain, whose clone flags no clone takes.
const (
	refusalLoopback = "loopback" // an interface there is none of, so the root cannot bring it up
	refusalSameNet  = "samenet"  // a user namespace with no network namespace: a weakened guard
)

// missingLink is the interface refusalLoopback brings up, which no namespace has.
const missingLink = "testkit-none0"

// forceRefusal makes this process's guard fail as refusal says, by the seams of nonetwork_linux.go,
// each of which can only make it fail.
func forceRefusal(refusal string) {
	switch refusal {
	case refusalLoopback:
		loopbackName = missingLink
	case refusalSameNet:
		guardCloneflags = syscall.CLONE_NEWUSER
	default:
		guardCloneflags = syscall.CLONE_THREAD // a flag no clone takes alone: the system refuses it, as it does a user namespace it will not make
	}
}

// refusalWrap is what a refused run is started under on Linux: nothing, as the seams force it.
func refusalWrap(string) []string { return nil }

// refusalWords are what Main's error says of the refusal.
func refusalWords(refusal string) []string {
	switch refusal {
	case refusalLoopback:
		return []string{"loopback could not be brought up in the new network namespace", missingLink}
	case refusalSameNet:
		return []string{"the new network namespace is the one the run started in"}
	}
	return []string{"the system refused the namespace"}
}

// Planted failure: a root that starts the tests with loopback down, or says nothing, makes the run print
// "ran" or Main's error miss the interface. Risk: none past the guard's own.
func TestALoopbackThatWillNotComeUpRefusesTheGuard(t *testing.T) {
	t.Parallel()
	refused(t, refusalLoopback)
}

// Planted failure: a guard whose namespace is the run's own network namespace, as clone flags without
// CLONE_NEWNET give, runs the tests open or nests without end. The seam can only weaken the guard this
// way, and this test holds it to a refusal. Risk: as above.
func TestAGuardLeftInTheRunsNetworkNamespaceRefuses(t *testing.T) {
	t.Parallel()
	refused(t, refusalSameNet)
}

// guardedAddr is the address the guarded run dials on Linux: one of this host's own, which the new
// network namespace does not have, so the guard refuses it and no packet leaves the machine.
func guardedAddr(t *testing.T) string {
	t.Helper()
	return localAddr(t)
}

// guardedHolds checks what the guard holds on Linux beyond the dials: a child given RerunEnv's guard is
// in its parent's network namespace, and did not nest again. A lookup is not checked: the C library's
// resolver may reach a daemon over a Unix socket, which the guard leaves open (NoNetwork's doc).
func guardedHolds(t *testing.T, on map[string]string) {
	t.Helper()
	if on["netns"] == "" || on["inner netns"] != on["netns"] {
		t.Errorf("a child given RerunEnv's guard is in network namespace %q, want its parent's %q: it nested again", on["inner netns"], on["netns"])
	}
}
