package quickstart_test

import (
	"os"
	"testing"

	"github.com/birdtable/go-testkit"
)

// TestMain runs the package's tests in an environment of their own,
// and fails the run if a test leaks a goroutine.
func TestMain(m *testing.M) {
	code, err := testkit.Main(m)
	if err != nil {
		panic(err)
	}
	os.Exit(testkit.NoLeaks(code))
}

// Each test sees a home, a temp folder and git settings of the run's
// own, and none of the tokens or settings of the person who ran it.
func TestTheEnvironmentIsTheRunsOwn(t *testing.T) {
	t.Parallel()
	testkit.Isolated(t)
}
