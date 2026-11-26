//go:build darwin || linux

package testkit

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// The parts guardedRole names.
const (
	guardedFuzz   = "fuzz"   // the fuzzing run and each of its workers
	guardedParent = "parent" // the test that starts a helper child of its own
	guardedChild  = "child"  // that helper, started by os/exec with its parent's environment, not by Rerun
)

// runSelf starts this test binary with args and env after this process's environment, bounded, and is
// its exit code and its output, stdout and stderr together.
func runSelf(t *testing.T, env []string, args ...string) (int, string) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), Scale(t, 2*time.Minute, 4, 0))
	defer cancel()
	cmd := exec.CommandContext(ctx, self, args...) //nolint:gosec // this test binary
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	cmd.Env = append(os.Environ(), env...)
	code := 0
	if err := cmd.Run(); err != nil {
		exit, ok := err.(*exec.ExitError) //nolint:errorlint // Run's own error type, never wrapped
		if !ok {
			t.Fatalf("this test binary could not run: %v", err)
		}
		code = exit.ExitCode()
	}
	return code, out.String()
}

// FuzzUnderNoNetwork is the fuzz target TestAFuzzTargetUnderNoNetworkRunsInsideTheGuard fuzzes: under
// the guard, each input dials out and the dial must fail. Under the run's own Main, with no guard, its
// seed does nothing.
func FuzzUnderNoNetwork(f *testing.F) {
	f.Add([]byte("x"))
	f.Fuzz(func(t *testing.T, _ []byte) {
		if os.Getenv(guardedRole) != guardedFuzz {
			return
		}
		if err := dialOnce(probeTarget); err == nil {
			t.Fatalf("a dial of %s from a fuzz worker under NoNetwork was made", probeTarget)
		}
	})
}

// Planted failure: a fuzz worker, the test binary started again by the fuzzing run, that finds no
// marker of the guard starts the namespaces again, and its pipe on fd 3 meets the worker's own, so
// the fuzzing fails. Risk: none past the guard's own; the fuzzing is bounded by a count.
func TestAFuzzTargetUnderNoNetworkRunsInsideTheGuard(t *testing.T) {
	t.Parallel()
	code, out := runSelf(t, []string{guardedRole + "=" + guardedFuzz},
		"-test.run=^$", "-test.fuzz=^FuzzUnderNoNetwork$", "-test.fuzztime=20x", "-test.parallel=1",
		"-test.fuzzcachedir="+t.TempDir())
	if code != 0 || !strings.Contains(out, "fuzz: elapsed") {
		t.Fatalf("fuzzing under NoNetwork ended %d, want 0 and the fuzzing run:\n%s", code, out)
	}
}

// Planted failure: a helper child, the test binary started by os/exec with its parent's environment,
// that finds no marker of the guard makes a network namespace of its own, where its parent's httptest
// listener is not, so its request fails. Risk: none past the guard's own; the request is to loopback.
func TestUnderNoNetworkAHelperChildReachesItsParentsListener(t *testing.T) {
	t.Parallel()
	const run = "-test.run=^TestUnderNoNetworkAHelperChildReachesItsParentsListener$"
	switch os.Getenv(guardedRole) {
	case "":
		code, out := runSelf(t, []string{guardedRole + "=" + guardedParent}, run, "-test.v")
		if code != 0 || !strings.Contains(out, "--- PASS: TestUnderNoNetworkAHelperChildReachesItsParentsListener") {
			t.Fatalf("the guarded parent ended %d, want 0 and its test passed:\n%s", code, out)
		}
	case guardedParent:
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }))
		defer srv.Close()
		if code, out := runSelf(t, []string{guardedRole + "=" + guardedChild, guardedURL + "=" + srv.URL}, run, "-test.v"); code != 0 {
			t.Fatalf("the helper child ended %d, want 0, its request to its parent's listener %s made:\n%s", code, srv.URL, out)
		}
	case guardedChild:
		r, err := (&http.Client{Timeout: 10 * time.Second}).Get(os.Getenv(guardedURL)) //nolint:noctx,gosec // a bounded request; G704: the parent's own listener on loopback, which the test gave
		if err != nil {
			t.Fatalf("the helper child could not reach its parent's listener: %v", err)
		}
		_ = r.Body.Close()
	}
}
