package contain

import "testing"

// TestContainedSkipsWithTheReasonItWasNotContainedFor: a run with no containment, one with none on its
// system, and one whose system refused it each skip the test that asks, with their own reason, and a
// contained run skips nothing. Planted failure: a reason that is not the run's, or a skip in a contained
// run, fails it. Risk: a test that signals a process runs where its signal can reach another process.
func TestContainedSkipsWithTheReasonItWasNotContainedFor(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		by   *containment
		skip string
	}{
		{"Main was not given Contain", nil, NotAsked},
		{"the system has no containment", &containment{skip: NoContainment}, NoContainment},
		{"the system refused it", &containment{skip: Refused + "operation not permitted"}, Refused + "operation not permitted"},
		{"a contained run", &containment{}, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := notContainedBy(c.by); got != c.skip {
				t.Errorf("the reason is %q, want %q", got, c.skip)
			}
			r := record(t, func(tb testing.TB) {
				tb.Helper()
				if why := notContainedBy(c.by); why != "" {
					tb.Skip(why)
				}
			})
			if r.skipped != c.skip {
				t.Errorf("the skip says %q, want %q", r.skipped, c.skip)
			}
		})
	}
}

// TestContainedMountsSkipsWithTheReasonTheRunHoldsNoMountCapability: a run on a system that gives a
// containment no mounts, whatever it holds, one with no containment, one with none on its system or
// refused, one given Contain alone and one whose ContainMounts was refused each give their own reason, and
// a run that holds the capability gives none. Planted failures: noMountsBy taking a run given Contain alone
// for one that holds the capability; noMountsBy leaving out the system (a Job Object run reads as given
// Contain alone). Risk: a test that mounts runs where it cannot, and fails, or reads as run when it skipped
// for a reason that sends its reader to the wrong option.
func TestContainedMountsSkipsWithTheReasonTheRunHoldsNoMountCapability(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		here bool
		by   *containment
		skip string
	}{
		{"a system with no namespace, contained", false, &containment{}, NoContainment},
		{"a system with no namespace, Main given ContainMounts", false, &containment{mounts: true}, NoContainment},
		{"Main was not given Contain", true, nil, NotAsked},
		{"the system has no containment", true, &containment{skip: NoContainment}, NoContainment},
		{"the system refused the containment", true, &containment{skip: Refused + "operation not permitted"}, Refused + "operation not permitted"},
		{"Main was given Contain, not ContainMounts", true, &containment{}, notAskedMounts},
		{"the capability was refused", true, &containment{mountsWhy: Refused + "a reason"}, Refused + "a reason"},
		{"the run holds the capability", true, &containment{mounts: true}, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := noMountsBy(c.here, c.by); got != c.skip {
				t.Errorf("the reason is %q, want %q", got, c.skip)
			}
		})
	}
}
