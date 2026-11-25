package contain

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/birdtable/go-testkit"
)

// TestARerunChildOfAContainedRunIsInTheContainment: a child testkit's Rerun starts from a test of a
// contained run is given the containment the run is in, by the RerunEnv of Contain's option, finds by
// itself that it is in it, so its Contained does not skip, and is in that very containment (the word it
// checks it by is the run's own: on Linux the link of the PID namespace), not in one it made of its own.
// Planted failure, red here: option leaving RerunEnv out (the child, given no containment, makes a second
// one inside the first, whose word is another namespace's).
//
// What it costs when this fails: a helper that signals a process is not in the containment its test is
// in, or every Rerun child of a contained run starts a namespace or a Job of its own.
func TestARerunChildOfAContainedRunIsInTheContainment(t *testing.T) {
	t.Parallel()
	Contained(t)
	if got := rerunEnv(); len(got) != 1 || !strings.HasPrefix(got[0], containVar+"="+partRun) {
		t.Fatalf("a contained run gives its Rerun children %q, want one entry of %s=%s", got, containVar, partRun)
	}
	code, out, err := testkit.Rerun(bounded(t), 1<<20, "contained")
	if err != nil || code != 0 {
		t.Fatalf("the rerun child: code %d, %v\n%s", code, err, out)
	}
	if want := fmt.Sprintf("%s%q", containedLine, ""); !strings.Contains(string(out), want) {
		t.Errorf("the rerun child of a contained run does not say %s:\n%s", want, out)
	}
	if want := fmt.Sprintf("%s%q", inLine, inside.Load().arg); !strings.Contains(string(out), want) {
		t.Errorf("the rerun child of a contained run is not in the run's containment, want %s:\n%s", want, out)
	}
}

// bounded is the context of a Rerun in these tests: t's own, ended five minutes on. Rerun takes its
// deadline from its caller and refuses a context with none.
func bounded(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	t.Cleanup(cancel)
	return ctx
}
