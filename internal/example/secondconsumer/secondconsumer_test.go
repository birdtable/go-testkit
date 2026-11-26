package secondconsumer_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/birdtable/go-testkit"
	"github.com/birdtable/go-testkit/canary"
	"github.com/birdtable/go-testkit/fake"
)

// The second consumer's own values, each unlike what testkit's own tests and Taskfile use.
const (
	region      = "SC_REGION" // a variable of the consumer's, which its allow-list keeps
	regionValue = "eu-north-7"
	dropped     = "GOTRACEBACK" // a variable testkit's fixed list keeps, which the consumer drops
	rerunWithin = 40 * time.Second
	rerunCap    = 3000 // bytes of each stream a Rerun child keeps
	waitBase    = 130 * time.Millisecond
	waitLimit   = 1.7 // the most the consumer lets Scale stretch waitBase
	busyLoad    = 2.5 // a load reading of the consumer's, above waitLimit
	sweepCode   = 23  // the code the consumer's stand-in ends with
	childLimit  = 30 * time.Second
	standIn     = "./internal/example/secondconsumer/sweep" // the consumer's own stand-in, by its folder in the module
	keyName     = "sc-api-key"                              // the name the consumer plants its own key under
	badge       = "SCK_4p9Q-Zr7vW2mXk8L"                    // a planted value in the consumer's own format, which New would not make
	logPlace    = "sync log"                                // the place the consumer names its captured log
)

// say writes s to a Rerun child's stdout, which the Rerun that started it reads.
func say(s string) {
	fmt.Fprint(os.Stdout, s) //nolint:forbidigo // cli.errors.3: a Rerun child's stdout is the output its parent reads
}

// TestMain starts the tests under the consumer's own allow-list: its region kept, GOTRACEBACK
// dropped. A Rerun child sets nothing, so what it sees is what Rerun handed it.
func TestMain(m *testing.M) {
	if _, child := testkit.Helper(); !child {
		for k, v := range map[string]string{region: regionValue, dropped: "system"} {
			if err := os.Setenv(k, v); err != nil {
				panic(err)
			}
		}
	}
	code, err := testkit.Main(m, testkit.Keep(region), testkit.Drop(dropped))
	if err != nil {
		panic(err)
	}
	os.Exit(code)
}

// seen is each variable of the allow-list rows as this process has it, NAME=value, empty when unset.
func seen() []string {
	var out []string
	for _, k := range []string{region, dropped} {
		out = append(out, k+"="+os.Getenv(k)) //nolint:forbidigo // cli.config.3: the row reads what Main and Rerun handed this process
	}
	return out
}

// The tests and a Rerun child see the consumer's allow-list, and Rerun takes the consumer's deadline
// and cap, refusing a run with neither.
//
// What it costs when this fails: a consumer's tests see a variable of ours, miss one of theirs, or
// wait on a bound testkit chose for them.
func TestTheTestsAndARerunTakeTheConsumersAllowListDeadlineAndCap(t *testing.T) {
	t.Parallel()
	testkit.Isolated(t)
	want := []string{region + "=" + regionValue, dropped + "="}
	if got := seen(); !slices.Equal(got, want) {
		t.Errorf("the test process sees %q, want %q", got, want)
	}
	ctx, cancel := context.WithTimeout(t.Context(), rerunWithin)
	defer cancel()
	if _, _, err := testkit.Rerun(t.Context(), rerunCap, "seen"); !errors.Is(err, testkit.ErrRefused) {
		t.Errorf("a Rerun whose ctx has no deadline: %v, want ErrRefused", err)
	}
	if _, _, err := testkit.Rerun(ctx, 0, "seen"); !errors.Is(err, testkit.ErrRefused) {
		t.Errorf("a Rerun with no output cap: %v, want ErrRefused", err)
	}
	code, out, err := testkit.Rerun(ctx, rerunCap, "seen")
	if err != nil || code != 0 || !bytes.Contains(out, []byte("seen: "+strings.Join(want, " ")+"\n")) {
		t.Errorf("a Rerun child: %v, code %d, printed %q, want it to see %q", err, code, out, want)
	}
	if _, out, err := testkit.Rerun(ctx, rerunCap, "flood"); !errors.Is(err, testkit.ErrOutputCap) || len(out) > 2*rerunCap {
		t.Errorf("a Rerun child that writes past the consumer's cap: %v, %d bytes, want ErrOutputCap and at most %d", err, len(out), 2*rerunCap)
	}
}

// TestTheHelperRunsOnlyWhenRerun is what a Rerun child of this package runs: seen prints what it was
// handed of the allow-list rows, and flood writes past the consumer's cap.
func TestTheHelperRunsOnlyWhenRerun(t *testing.T) {
	t.Parallel()
	args, child := testkit.Helper()
	if !child {
		if len(args) > 0 {
			t.Fatalf("a run go test started was given %q", args)
		}
		return
	}
	switch args[0] {
	case "seen":
		say("seen: " + strings.Join(seen(), " ") + "\n")
	case "flood":
		say(strings.Repeat("y", 4*rerunCap))
	}
}

// Scale reads the consumer's load and stops at the consumer's limit: a load above the limit gives
// the base times the limit, and a limit of 1 gives the base whatever the load.
//
// What it costs when this fails: testkit reads a load or a ceiling of its own, and a consumer's waits
// are stretched past what it allowed, or not at all on a machine it knows is busy.
func TestScaleTakesTheConsumersLoadAndStopsAtItsLimit(t *testing.T) {
	t.Parallel()
	if got, want := testkit.Scale(t, waitBase, waitLimit, busyLoad), time.Duration(float64(waitBase)*waitLimit); got != want {
		t.Errorf("Scale of %s at load %v and limit %v is %s, want %s", waitBase, busyLoad, waitLimit, got, want)
	}
	if got := testkit.Scale(t, waitBase, 1, busyLoad); got != waitBase {
		t.Errorf("Scale of %s at load %v and limit 1 is %s, want %s", waitBase, busyLoad, got, waitBase)
	}
}

// A stand-in built from the consumer's own main package does the consumer's step among fake's
// generic ones, with the consumer's values, and ends with the code the consumer's step gives.
//
// What it costs when this fails: a consumer that needs one step of its own cannot use fake, and
// keeps its own copy of every generic step beside it.
func TestAStandInDoesTheConsumersOwnStepAmongTheGenericOnes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := fake.From(standIn).Program(t, dir, "sweep", fake.Custom("region", regionValue), fake.PrintEnv(region), fake.Exit(sweepCode))
	for _, row := range []struct {
		args []string
		code int
		out  string
	}{
		{args: []string{"--to", regionValue}, code: sweepCode, out: "region " + regionValue + ": --to " + regionValue + "\n" + regionValue},
		{args: []string{"--to", "us-east-9"}, code: 41, out: "region " + regionValue + ": --to us-east-9\n"},
	} {
		code, out := run(t, path, dir, row.args...)
		if code != row.code || out != row.out {
			t.Errorf("the consumer's stand-in, given %q: code %d and %q, want %d and %q", row.args, code, out, row.code, row.out)
		}
	}
}

// run starts the stand-in at path in dir with args, in this test's environment, and is its code and
// stdout.
func run(t *testing.T, path, dir string, args ...string) (int, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), childLimit)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...) //nolint:gosec // G204: the stand-in this row placed, by its path
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		t.Fatalf("starting %s: %v", path, err)
	}
	if ctx.Err() != nil {
		t.Fatalf("%s did not end within %s: stderr %q", path, childLimit, stderr.String())
	}
	return cmd.ProcessState.ExitCode(), stdout.String()
}

// caught is a testing.TB that keeps the lines a check fails with, so a row can plant a hit and read
// what the canary said of it without failing.
type caught struct {
	testing.TB
	lines []string
}

func (c *caught) Helper() {}
func (c *caught) Errorf(format string, args ...any) {
	c.lines = append(c.lines, fmt.Sprintf(format, args...))
}

// The canary plants the consumer's own key, in a format of its own, under the consumer's name, hands
// it back unchanged, and names the consumer's name and place on a hit, never the key.
//
// What it costs when this fails: a consumer whose program takes a key of a set format cannot plant
// one, or a hit names a value or a place of ours in place of the consumer's.
func TestTheCanaryPlantsTheConsumersOwnKeyAndNamesItsPlace(t *testing.T) {
	t.Parallel()
	c := &caught{TB: t}
	s := canary.New(c, "session")
	s.Own(keyName, badge)
	if got := s.Get(keyName); got != badge {
		t.Fatalf("Get of the consumer's key is %q, want the key it planted", got)
	}
	hits := s.Bytes(logPlace, []byte("sync: auth="+badge+"\n"))
	want := []canary.Hit{{Name: keyName, Check: canary.CheckBytes, Place: logPlace, Offset: int64(len("sync: auth=")), Encoding: canary.Raw, Count: 1}}
	if !slices.Equal(hits, want) {
		t.Errorf("the hits of the consumer's key in its log are %+v, want %+v", hits, want)
	}
	said := strings.Join(c.lines, "\n")
	if !strings.Contains(said, fmt.Sprintf("canary: the planted %q is in %q", keyName, logPlace)) || strings.Contains(said, badge) {
		t.Errorf("the canary said %q, want the consumer's name and place and never its key", said)
	}
}
