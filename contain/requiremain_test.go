package contain

import (
	"fmt"
	"strings"
	"testing"

	"github.com/birdtable/go-testkit"
)

// TestEveryPackageOfTheModuleCallsMain: every package of the module contain that has tests calls
// testkit's Main from its TestMain on darwin, linux and windows, and none opts out.
//
// What it costs when this fails: a package of contain's runs its tests in the person's environment,
// outside the containment, with their home, their git settings and their tokens.
func TestEveryPackageOfTheModuleCallsMain(t *testing.T) {
	t.Parallel()
	r := &logs{TB: t}
	testkit.RequireMain(r)
	if r.unisolated {
		t.Error("RequireMain logged an opt-out, want no package of the module contain to opt out")
	}
}

// logs is t, noting whether RequireMain logged a package's opt-out.
type logs struct {
	testing.TB
	unisolated bool
}

// Logf notes an opt-out and logs the line through t.
func (l *logs) Logf(format string, args ...any) {
	l.Helper()
	l.unisolated = l.unisolated || strings.Contains(fmt.Sprintf(format, args...), "is unisolated on")
	l.TB.Logf(format, args...)
}
