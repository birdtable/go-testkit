package testkit

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestARerunChildWhoseParentIsGoneEndsAtItsOwnTimeout: a child of Rerun is started with a -test.timeout
// of what is left of its caller's deadline and a grace, so a child whose parent is gone before it could
// end it ends by itself. Here a wrapper starts the child and exits at once, leaving it behind as a killed
// test binary does; the child, the helper's hang, waits on an end no parent will send. It must end by go
// test's own timeout, a second or two on, and not at the helper's own limit, which no consumer's helper
// has. Rerun's own grace is childGrace; this row gives a second,
// so it does not wait ten.
//
// The child's -test.timeout is rerun's own sum, from a deadline a second on, but the wrapper is ended
// only at bounded's deadline: a -race wrapper starting under load can take more than that second to
// start the child and exit, and was ended at it, red with "context deadline exceeded", on Windows.
// A quiet run waits as long as before.
//
// What it costs when this fails: a consumer's helper that waits on its parent runs for ever after a
// parent killed by a timeout or a cancelled push, as an orphan once did.
func TestARerunChildWhoseParentIsGoneEndsAtItsOwnTimeout(t *testing.T) {
	t.Parallel()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "child.out")
	ctx := childClock{Context: bounded(t), at: time.Now().Add(time.Second)}
	if code, said, err := rerun(ctx, testCap, []string{self, abandonWord, out}, nil, time.Second, []string{"hang"}); err != nil || code != 0 {
		t.Fatalf("the wrapper that leaves its child: %d, %v\n%s", code, err, said)
	}
	// The child's end is its output, read until go test's timeout line is in it: no process of this
	// test waits for it, since its parent is gone.
	limit := time.Now().Add(Scale(t, 30*time.Second, rerunStretch, 0))
	for {
		got, err := os.ReadFile(out)
		if err == nil && strings.Contains(string(got), "panic: test timed out after") {
			return
		}
		if time.Now().After(limit) {
			t.Fatalf("the child its parent left is still running past the deadline it was given and its grace (read %v):\n%s", err, got)
		}
		<-time.After(50 * time.Millisecond)
	}
}

// childClock is a Context whose Deadline is at, for the -test.timeout rerun gives its child, and whose
// end is its own Context's, for what rerun waits on: the two are one clock in a caller's context, and
// this row needs a short one for the child and a long one for the wrapper.
type childClock struct {
	context.Context
	at time.Time
}

// Deadline is at.
func (c childClock) Deadline() (time.Time, bool) { return c.at, true }
