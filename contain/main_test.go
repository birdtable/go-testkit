package contain

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/birdtable/go-testkit"
)

// mountsVar makes TestMain give Main ContainMounts in place of Contain, for the rows that run this test
// binary again to see a ContainMounts run from inside. Main drops it with the rest of the environment,
// so mountsRun is how a test of that run knows it is one.
const mountsVar = "TESTKIT_CONTAIN_MOUNTS"

// mountsRun is whether TestMain gave Main ContainMounts.
var mountsRun bool //nolint:gochecknoglobals // set once by TestMain, before any test runs, and read by the rows of a ContainMounts run

// TestMain starts the tests from the environment testkit's Main makes, inside the containment Contain
// makes. GOWORK is kept as the run has it: this module requires the root at a version that the tiers
// give it by a workspace file, and the row that builds this test binary again reads the same file.
func TestMain(m *testing.M) {
	if code, done := capsMain(); done {
		os.Exit(code)
	}
	contained := Contain()
	if os.Getenv(mountsVar) != "" {
		contained, mountsRun = ContainMounts(), true
	}
	code, err := testkit.Main(m, testkit.Keep("GOWORK"), contained)
	if err != nil {
		panic(err)
	}
	os.Exit(code)
}

// containedLine begins the line the helper writes of what Contained did in it, and inLine the line of the
// containment it found itself in: the word the process that made it gave the run to check it by.
const (
	containedLine = "the rerun child's Contained skipped with: "
	inLine        = "the rerun child is in the containment: "
)

// TestTheHelperRunsOnlyWhenRerun is this package's helper, the test testkit's Rerun runs in the child:
// rerun, it does what it was given; started by go test, it was given nothing.
func TestTheHelperRunsOnlyWhenRerun(t *testing.T) {
	t.Parallel()
	args, ok := testkit.Helper()
	if !ok {
		return
	}
	if strings.Join(args, " ") == "contained" {
		r := record(t, Contained)
		if r.failed != "" {
			t.Fatalf("Contained failed in the child: %s", r.failed)
		}
		t.Logf("%s%q", containedLine, r.skipped)
		if c := inside.Load(); c != nil {
			t.Logf("%s%q", inLine, c.arg)
		}
	}
}

// recorder is a testing.TB that keeps the first skip or failure of what it runs, and ends it there as
// a real one would, so no skip reaches the run's report. Its TempDir and the rest are t's.
type recorder struct {
	testing.TB
	skipped, failed string
}

// record runs f on a recorder of t, on a goroutine of its own, which a skip or a fatal failure ends.
func record(t *testing.T, f func(testing.TB)) *recorder {
	t.Helper()
	r := &recorder{TB: t}
	done := make(chan struct{})
	go func() {
		defer close(done)
		f(r)
	}()
	<-done
	return r
}

func (r *recorder) Helper()                           {}
func (r *recorder) Skip(args ...any)                  { r.skipped = fmt.Sprint(args...); runtime.Goexit() }
func (r *recorder) Skipf(format string, args ...any)  { r.Skip(fmt.Sprintf(format, args...)) }
func (r *recorder) SkipNow()                          { r.Skip("SkipNow") }
func (r *recorder) Error(args ...any)                 { r.failed += fmt.Sprint(args...) }
func (r *recorder) Errorf(format string, args ...any) { r.failed += fmt.Sprintf(format, args...) }
func (r *recorder) Fatal(args ...any)                 { r.Error(args...); runtime.Goexit() }
func (r *recorder) Fatalf(format string, args ...any) { r.Errorf(format, args...); runtime.Goexit() }
func (r *recorder) FailNow()                          { r.Fatal("FailNow") }
