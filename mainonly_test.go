package testkit

import (
	"os"
	"strings"
	"testing"
)

// TestAMainOnlyOptionGivenToIsolateBuildOrBuildAtIsRefusedNamingIt: each function but Main that takes
// options refuses one only Main acts on, NoNetwork or Before, and Build and BuildAt refuse RerunEnv,
// which only a Rerun under Main or Isolate calls: Isolate returns an error and isolates nothing, Build's
// Binary and every copy of it runs nothing and gives the refusal on Stderr, and BuildAt fails the test.
// Each refusal names the function and the option and says to give it to Main; no Before or RerunEnv
// given is called. An option inside Options, as a containment hands its Before, is refused as one given
// alone. Isolate takes a RerunEnv, which mainOnly lets through for it alone.
//
// What it costs when this fails: a NoNetwork or a containment passed where it is ignored says nothing,
// and the tests or the binary run with the network open, or outside the containment, under a call that
// reads as if they did not.
func TestAMainOnlyOptionGivenToIsolateBuildOrBuildAtIsRefusedNamingIt(t *testing.T) {
	t.Parallel()
	called := ""
	before := Before(func([]string) (int, bool, error) { called += "Before "; return 0, true, nil })
	rerunEnv := RerunEnv(func() []string { called += "RerunEnv "; return nil })
	contained := Options(Keep("TESTKIT_PLANTED"), before, rerunEnv) // shaped as contain's option is
	calls := map[string]func(*testing.T, Option) string{
		"Isolate": func(t *testing.T, o Option) string {
			t.Helper()
			home := os.Getenv("HOME")
			restore, err := Isolate(o)
			if err == nil {
				t.Errorf("Isolate took the option and isolated the process")
				if err := restore(); err != nil {
					t.Errorf("its restore: %v", err)
				}
				return ""
			}
			if restore != nil || os.Getenv("HOME") != home {
				t.Errorf("a refused Isolate returned a restore, or moved HOME from %q to %q", home, os.Getenv("HOME"))
			}
			return err.Error()
		},
		"Build": func(t *testing.T, o Option) string {
			t.Helper()
			b := Build(".", o)
			for _, c := range []*Binary{b, b.X("main.version", "1"), b.Trimpath(), b.Env("TESTKIT_A=1")} {
				if r := c.Run(); r.Code != -1 || c.Path() != "" || r.Stderr != b.Run().Stderr {
					t.Errorf("a refused Build's copy ran: code %d, path %q, stderr %q", r.Code, c.Path(), r.Stderr)
				}
			}
			return b.Run().Stderr
		},
		"BuildAt": func(t *testing.T, o Option) string {
			t.Helper()
			returned := false
			r := record(t, func(tb testing.TB) {
				tb.Helper()
				BuildAt(tb, "v0.0.0-planted", "./planted", o)
				returned = true
			})
			if returned || r.skipped != "" || !strings.Contains(r.failed, `"./planted" at "v0.0.0-planted"`) {
				t.Errorf("a refused BuildAt returned %v, skipped %q and failed %q, want it to fail naming the package and the ref", returned, r.skipped, r.failed)
			}
			return r.failed
		},
	}
	for _, tc := range []struct {
		fn, row string
		opt     Option
		want    string // the option the refusal names
	}{
		{"Isolate", "NoNetwork", NoNetwork(), "NoNetwork"},
		{"Isolate", "Before", before, "Before"},
		{"Isolate", "a containment's Options", contained, "Before"},
		{"Build", "NoNetwork", NoNetwork(hostOS()), "NoNetwork"},
		{"Build", "Before", before, "Before"},
		{"Build", "a containment's Options", contained, "Before"},
		{"Build", "RerunEnv", rerunEnv, "RerunEnv"},
		{"BuildAt", "NoNetwork", Options(NoNetwork()), "NoNetwork"},
		{"BuildAt", "Before", before, "Before"},
		{"BuildAt", "a containment's Options", contained, "Before"},
		{"BuildAt", "RerunEnv", rerunEnv, "RerunEnv"},
	} {
		got := calls[tc.fn](t, tc.opt)
		for _, w := range []string{tc.fn + " was given " + tc.want + ", an option of Main", "give it to Main"} {
			if !strings.Contains(got, w) {
				t.Errorf("%s given %s: the refusal is %q, want it to hold %q", tc.fn, tc.row, got, w)
			}
		}
	}
	if err := mainOnly("Isolate", []Option{rerunEnv, Keep("TESTKIT_PLANTED"), GoOffline()}); err != nil {
		t.Errorf("Isolate refused a RerunEnv, which it hands to a Rerun: %v", err)
	}
	if called != "" {
		t.Errorf("a refusal called the hooks it was given: %s", called)
	}
}
