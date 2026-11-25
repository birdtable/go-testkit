package testkit

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

// TestTheFirstBeforeThatIsDoneEndsMainAndNoTestRuns: Main runs each Before in the order given, an
// Options' among them in its place, on the environment the process started in; the first whose done
// is true ends it with its code and error, no later Before is called and no test runs. A Before that
// is not done goes on, whatever its code and error say, and a nil one adds nothing.
//
// What it costs when this fails: a containment's rerun is followed by a second run of every test
// outside it, or a run that should have been contained runs uncontained, or two containments start.
func TestTheFirstBeforeThatIsDoneEndsMainAndNoTestRuns(t *testing.T) {
	t.Parallel()
	start := []string{"PATH=/planted/bin", "HOME=/planted/home"}
	errDone := errors.New("the containment's rerun ended")
	var order []string
	saw := map[string][]string{}
	hook := func(name string, code int, done bool, err error) Option {
		return Before(func(s []string) (int, bool, error) {
			order = append(order, name)
			saw[name] = s
			return code, done, err
		})
	}
	ran := false
	code, err := run(runner(func() int { ran = true; return 0 }), start, []Option{
		hook("first", 9, false, errors.New("not done, so not Main's")),
		Options(Before(nil), hook("second", 7, true, errDone)),
		hook("third", 0, true, nil),
	})
	if code != 7 || !errors.Is(err, errDone) || ran {
		t.Errorf("run: code %d, %v, the tests ran %v; want 7, the second Before's error, and no test run", code, err, ran)
	}
	if !slices.Equal(order, []string{"first", "second"}) {
		t.Errorf("the Befores ran %q, want first then second, and not third", order)
	}
	for name, s := range saw {
		if !slices.Equal(s, start) {
			t.Errorf("the %s Before was given %q, want the start %q", name, s, start)
		}
	}
	if c := confOf([]Option{Before(nil), RerunEnv(nil), Options()}); len(c.before) != 0 || len(c.rerunEnv) != 0 {
		t.Errorf("a nil Before or RerunEnv, or an empty Options, added %d Befores and %d RerunEnvs", len(c.before), len(c.rerunEnv))
	}
}

// TestARerunChildIsGivenEachRerunEnvEntryAfterCleansAndItsArgsLast: a Rerun child's environment is
// Clean's, then each entry of each RerunEnv in the order given, called once a start, less one with no
// name before an =, then the variable Helper reads, last, so an entry that names it replaces nothing;
// with no isolation in force no RerunEnv is called.
//
// What it costs when this fails: a containment's child is not told it is inside one and makes a
// second, a hook hands a child a variable with no name, or a hook changes what Helper gives the child.
func TestARerunChildIsGivenEachRerunEnvEntryAfterCleansAndItsArgsLast(t *testing.T) {
	t.Parallel()
	calls := 0
	first := func() []string {
		calls++
		return []string{"TESTKIT_A=1", "=nameless", "no-equals", helperVar + "=forged", "TESTKIT_B="}
	}
	second := func() []string { calls++; return []string{"TESTKIT_C=3"} }
	in := isolated{conf: confOf([]Option{RerunEnv(first), Options(RerunEnv(nil), RerunEnv(second))})}
	got, err := childEnv(in, []string{"PATH=/planted/bin", "GITHUB_TOKEN=planted"}, `["a"]`)
	want := []string{"PATH=/planted/bin", "TESTKIT_A=1", helperVar + "=forged", "TESTKIT_B=", "TESTKIT_C=3", helperVar + `=["a"]`}
	if err != nil || !slices.Equal(got, want) || calls != 2 {
		t.Errorf("the child's environment is %q (%v) after %d calls, want %q after 2", got, err, calls, want)
	}
	if got, err := childEnv(isolated{}, []string{"PATH=/planted/bin"}, `["a"]`); err != nil || !slices.Equal(got, []string{"PATH=/planted/bin", helperVar + `=["a"]`}) {
		t.Errorf("with no isolation in force the child's environment is %q (%v)", got, err)
	}
}

// A RerunEnv entry naming what the isolation set, GoOffline's GOPROXY or a home, in any case, is
// refused with ErrRefused and the name, and gives no environment; one naming nothing it set passes.
//
// What it costs when this fails: a hook gives a Rerun child a proxy, a home or a git configuration of
// its own after Clean's, os/exec keeps the hook's, and the child reaches the network or the person's
// files under an isolation that says it cannot.
func TestARerunEnvEntryNamingWhatTheIsolationSetIsRefused(t *testing.T) {
	t.Parallel()
	set := map[string]string{"GOPROXY": "off", "HOME": "/own/home", gitConfig: "/own/gitconfig"}
	for _, entry := range []string{"GOPROXY=https://proxy.example", "goproxy=https://proxy.example", "HOME=/elsewhere", gitConfig + "=/elsewhere/gitconfig"} {
		in := isolated{set: set, conf: confOf([]Option{RerunEnv(func() []string { return []string{"TESTKIT_OK=1", entry} })})}
		name, _, _ := strings.Cut(entry, "=")
		got, err := childEnv(in, []string{"PATH=/planted/bin"}, `["a"]`)
		if !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), "a RerunEnv gives "+name+",") || got != nil {
			t.Errorf("a RerunEnv giving %q: the environment %q and %v, want ErrRefused naming %s", entry, got, err, name)
		}
	}
	in := isolated{set: set, conf: confOf([]Option{RerunEnv(func() []string { return []string{"TESTKIT_CONTAINED=1"} })})}
	if got, err := childEnv(in, nil, `["a"]`); err != nil || !slices.Contains(got, "TESTKIT_CONTAINED=1") {
		t.Errorf("a RerunEnv giving a name the isolation did not set: %q and %v, want it passed", got, err)
	}
}
