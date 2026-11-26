package taskfile

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/birdtable/go-testkit"
)

// TestMain runs the package's tests in the environment testkit's Main makes, where a go they start
// fetches nothing. They start no process and read no person's folder, and Main holds them to that.
func TestMain(m *testing.M) {
	code, err := testkit.Main(m, testkit.GoOffline())
	if err != nil {
		panic(err)
	}
	os.Exit(code)
}

// TestEveryPackageOfTheModuleCallsMain: every package of the module taskfile that has tests calls
// testkit's Main from its TestMain on darwin, linux and windows, and none opts out.
//
// What it costs when this fails: a package of taskfile's runs its tests in the person's environment,
// with their home, their git settings and their tokens.
func TestEveryPackageOfTheModuleCallsMain(t *testing.T) {
	t.Parallel()
	r := &logs{TB: t}
	testkit.RequireMain(r)
	if r.unisolated {
		t.Error("RequireMain logged an opt-out, want no package of the module taskfile to opt out")
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
