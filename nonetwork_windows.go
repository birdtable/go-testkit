//go:build windows

package testkit

import "syscall"

// probeRefusal is never read here: no guard is set, so no probe runs.
const probeRefusal = syscall.WSAEACCES

// guardHere is NoNetwork on Windows, which gives no guard: why says so, and Main fails the run unless
// the caller named windows.
func guardHere([]string) (code int, done bool, why string, err error) {
	return 0, false, "Windows gives an unprivileged process no way to refuse outbound connections and keep loopback", nil
}

// guardRerunEnv gives a child of Rerun nothing: there is no guard to hand on.
func guardRerunEnv() []string { return nil }
