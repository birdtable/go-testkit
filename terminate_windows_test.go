package testkit

import (
	"errors"
	"testing"
)

// TestStartTerminateIsUnsupportedOnWindows: on Windows, which has no SIGTERM, Terminate of a running
// program sends nothing and is errors.ErrUnsupported, and the program runs on to its Interrupt.
//
// What it costs when this fails: a test that terminates on Windows passes having sent nothing, or
// kills where it meant to ask.
func TestStartTerminateIsUnsupportedOnWindows(t *testing.T) {
	t.Parallel()
	r, _, _ := startReady(t)
	if err := r.Terminate(); !errors.Is(err, errors.ErrUnsupported) {
		_ = r.Kill()
		t.Fatalf("Terminate on Windows: %v, want errors.ErrUnsupported; the program's Wait: %+v", err, r.Wait())
	}
	if err := r.Interrupt(); err != nil {
		t.Fatal(err)
	}
	if got := r.Wait(); got.Code != 7 || got.Stdout != "ready\ninterrupt" {
		t.Fatalf("the program's Wait after Terminate and Interrupt is %+v, want code 7, ready then interrupt", got)
	}
}
