package testkit

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// TestARerunViaAWrapperRunsTheChildBehindIt: RerunVia starts the wrapper, which runs the child's
// command line it is given, so what the wrapper writes and what the child writes are both read, and
// the child's failing code comes back through it. A wrapper that is empty, or whose program is a
// bare name, starts nothing: its error is ErrRefused, code -1 and no output.
//
// What it costs when this fails: a test that runs its helper inside a sandbox runs it outside, and
// passes without the sandbox; or a wrapper named by a word runs whatever PATH finds first.
func TestARerunViaAWrapperRunsTheChildBehindIt(t *testing.T) {
	t.Parallel()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	code, out, err := RerunVia(bounded(t), testCap, []string{self, wrapWord}, "fail")
	if err != nil || code == 0 || !strings.Contains(string(out), wrapLine) || !strings.Contains(string(out), "the child fails as it was told") {
		t.Errorf("the failing child behind the wrapper: %d, %v, want its failing code, the wrapper's line and the child's\n%s", code, err, out)
	}
	for _, wrapper := range [][]string{nil, {}, {"testkit-wrapper"}, {"bin/wrapper"}} {
		if code, out, err := RerunVia(bounded(t), testCap, wrapper, "fail"); !errors.Is(err, ErrRefused) || code != -1 || out != nil {
			t.Errorf("the wrapper %q: %d, %v, %q, want ErrRefused, code -1 and nothing", wrapper, code, err, out)
		}
	}
}
