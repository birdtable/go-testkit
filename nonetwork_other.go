//go:build !darwin && !linux && !windows

package testkit

import "syscall"

// probeRefusal is never read here: no guard is set, so no probe runs.
const probeRefusal = syscall.EPERM

// guardHere is NoNetwork where testkit has no guard: why says so, and Main fails the run unless the
// caller named the system.
func guardHere([]string) (code int, done bool, why string, err error) {
	return 0, false, "testkit sets no network guard on " + hostOS(), nil
}

// guardRerunEnv gives a child of Rerun nothing: there is no guard to hand on.
func guardRerunEnv() []string { return nil }
