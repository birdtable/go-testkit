package testkit

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"time"
)

// NoNetwork makes Main hold the test process, and every process it starts, to loopback: a connection to
// any address that is not loopback fails with an error that names the address, and a name lookup through
// the system's resolver fails. Loopback stays open, so an httptest server works. Main sets the guard
// before any test runs, and then proves it: a TCP connect to 192.0.2.1:9 (TEST-NET-1, which is never
// routed) must fail at once with the guard's own error, and a connect to a listener on 127.0.0.1 must
// succeed. Isolate runs no Before, so Isolate given NoNetwork returns an error and isolates nothing.
//
// On macOS Main calls the system sandbox's sandbox_init on the test process itself, with a profile that
// denies every outbound connection but to localhost and to a Unix socket, and denies the resolver's
// socket and service; the sandbox holds every thread of the process and everything it starts. On Linux
// Main reruns the test binary in a user and network namespace of its own, where only loopback is up: its
// first child, root of that namespace, brings loopback up, and runs the tests as its own child in a
// nested user namespace, with the caller's uid and gid. Main returns their exit code. A caller that is not
// uid 0 holds no capability there. A uid 0 caller's tests hold every capability of that nested user
// namespace and none over the network namespace, which the root's owns, so the guard holds. Beside the
// contain module, NoNetwork goes first: Main(m, NoNetwork(), contain.Contain()). As uid 0 the other
// order is refused, and Main's error says to put NoNetwork first.
// The tests' environment keeps the guard's marker, set as the tests checked their namespace, before the
// probe, and RerunEnv hands it to a child of Rerun, so a start of the test binary inside the guard, a
// fuzz worker or a helper child started by os/exec too, checks itself and probes again, and does not nest. The rerun's GODEBUG ends with netdns=go, after the
// settings the caller gave it, so the test binary and a Go program it starts with that environment look
// a name up with Go's own resolver, which fails there.
//
// Where the guard cannot be set, or the probe fails, Main returns 1 and an error that names the system
// and the reason, and no test runs. Windows gives an unprivileged process no way to refuse outbound
// connections and keep loopback, and every system but macOS and Linux is the same here. On Linux a host
// that refuses an unprivileged user namespace, or gives it no capability to bring loopback up (Ubuntu's
// kernel.apparmor_restrict_unprivileged_userns=1), refuses the guard; on macOS a test process already
// inside a sandbox (sandbox-exec, or an agent's sandboxed shell) does, as macOS lets it add none.
// unguardedOn names, by GOOS, each system where the caller accepts that: there Main writes one line to
// stderr, saying the tests run with the network open and why, and runs them. A name that is no GOOS is
// Main's error, so a misspelt name never opens the network.
//
// What it does not promise: that a test goes red where the code under test swallows the error, as
// nothing counts the refused connections; that a daemon the test reaches over a Unix socket (Docker's)
// or, on macOS, a mach service (nsurlsessiond, for a background URL session) stays off the network, as
// the daemon connects for itself, outside the guard; on Linux, that a program a test starts that is not
// Go looks no name up: the C library's resolver may reach nscd or systemd-resolved over a Unix socket
// and so carry the name out, though the program's connections still fail; and on macOS, that a connect
// to one of the host's own addresses fails: the sandbox's localhost is every address of the host, and
// its profile names no narrower one, so a service the host runs on 0.0.0.0 is reachable through any
// address of the host, as through loopback, and no packet of it leaves the machine. GoOffline still
// helps beside it: a go a test starts then fails with go's own message, not a refused connection. The
// guard is tested on macOS arm64 and Linux amd64; on macOS amd64 it is built and vetted only. Where Go
// stops allowing the libSystem call by name (see nonetwork_darwin.go), the build fails; the way out is a
// Before that runs the test binary under /usr/bin/sandbox-exec -p with the same profile.
func NoNetwork(unguardedOn ...string) Option {
	unguarded := slices.Clone(unguardedOn)
	return Options(
		Before(func(start []string) (int, bool, error) { return noNetwork(start, unguarded, guardHere) }),
		RerunEnv(guardRerunEnv),
		Keep(guardVar), // isolate keeps the guard's own value alone, once the tests checked in (guardVar)
		func(c *conf) { c.guard = true },
	)
}

// guardVar carries the part of the guard a process is, on Linux, from the process that made the
// namespaces to the one it starts. It is testkit's own, never a name a caller sets. Its value is
// "init:<uid>:<gid>:<net>" for the root of the new namespaces, or "run:<net>" for the tests, net being
// the link of the network namespace the run started in (/proc/self/ns/net, "net:[<inode>]"). Main keeps
// "run:<net>" in the tests' environment once the tests checked their namespace, before the probe, and no
// other value of it on any system, so each start of the test binary inside the guard (a fuzz worker, a
// helper child) checks itself and probes again, and does not nest; RerunEnv hands it to a child of Rerun too. A stray value never opens the network: a run
// is guarded only in a network namespace other than that one where every interface that is up is
// loopback, and only once the probe passes there; a run in another namespace where one is up that is not
// loopback refuses, naming it, and never starts the namespaces again; any other run starts them itself.
const guardVar = "TESTKIT_NONETWORK"

// withoutGuardVar is environ with guardVar left out.
func withoutGuardVar(environ []string) []string {
	out := make([]string, 0, len(environ))
	for _, kv := range environ {
		if !strings.HasPrefix(kv, guardVar+"=") {
			out = append(out, kv)
		}
	}
	return out
}

// knownOS is every GOOS that go tool dist list names: a name of unguardedOn is one of them.
var knownOS = []string{"aix", "android", "darwin", "dragonfly", "freebsd", "illumos", "ios", "js", "linux", "netbsd", "openbsd", "plan9", "solaris", "wasip1", "windows"} //nolint:gochecknoglobals // a fixed list, read only

// hostOS is the running system's GOOS: NoNetwork's caller names systems so, and its errors do too.
func hostOS() string {
	return runtime.GOOS //nolint:forbidigo // arch.ports.1: the API names a system by GOOS, so the name is matched, not branched on
}

// noNetwork is NoNetwork's Before: it sets the guard on this system by step, which is guardHere, and
// probes it, or, where it cannot, fails the run, unless unguarded names this system. An error of step
// ends the run whatever its done says, with 1 where its code is 0, so no test runs unguarded past it.
func noNetwork(start, unguarded []string, step func([]string) (int, bool, string, error)) (int, bool, error) {
	for _, name := range unguarded {
		if !slices.Contains(knownOS, name) {
			return 1, true, fmt.Errorf("NoNetwork was given %q, which is no GOOS, so no test ran: name each system as GOOS does (%s)", name, strings.Join(knownOS, ", "))
		}
	}
	code, done, why, err := step(start)
	if err != nil && code == 0 {
		code = 1
	}
	if done || err != nil {
		return code, true, err
	}
	if why == "" {
		if why = probe(probeTarget, probeRefusal); why == "" {
			return 0, false, nil
		}
	}
	if slices.Contains(unguarded, hostOS()) {
		fmt.Fprintf(os.Stderr, "testkit: no network guard on %s: %s; the tests run with the network open, as NoNetwork(%q) allows\n", hostOS(), why, hostOS()) //nolint:forbidigo // cli.errors.3: Main has no writer to hand, and the run's log is where a person reads that the guard is off
		return 0, false, nil
	}
	return 1, true, fmt.Errorf("no network guard on %s: %s; so no test ran, and NoNetwork(%q) would run them with the network open", hostOS(), why, hostOS())
}

// probeTarget is the address the probe connects to, which the guard must refuse: TEST-NET-1 of RFC
// 5737, which no network routes, so a guard that holds nothing sends one SYN to a documentation address.
const probeTarget = "192.0.2.1:9"

// probeWait bounds each connect of the probe. A guard refuses at once; the bound only ends a probe
// whose connect went out, which is already its failure.
const probeWait = 3 * time.Second

// probe proves the guard: a TCP connect to target must fail with refusal, the guard's own error, and a
// connect to a listener on 127.0.0.1 must succeed. It is "" when both hold, and else what failed.
func probe(target string, refusal syscall.Errno) string {
	// Main runs before any test, with no context to hand: probeWait is each connect's bound.
	ctx := context.Background() //nolint:forbidigo // go.concurrency.1: a Before takes no context, as Main runs it before any test; probeWait bounds each connect
	d := net.Dialer{Timeout: probeWait}
	c, err := d.DialContext(ctx, "tcp", target)
	if err == nil {
		_ = c.Close() //nolint:errcheck // the probe's failed connection, closed as it is reported
		return "the probe's connection to " + target + " was made, so the guard holds nothing"
	}
	if !errors.Is(err, refusal) {
		return fmt.Sprintf("the probe's connection to %s failed, but not with the guard's %s: %v", target, refusal.Error(), err)
	}
	var lc net.ListenConfig
	l, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return "the probe could not listen on loopback: " + err.Error()
	}
	defer func() { _ = l.Close() }() //nolint:errcheck // the probe's own listener
	c, err = d.DialContext(ctx, "tcp", l.Addr().String())
	if err != nil {
		return "the probe could not reach its listener on loopback: " + err.Error()
	}
	_ = c.Close() //nolint:errcheck // the probe's loopback connection, which did its work by being made
	return ""
}
