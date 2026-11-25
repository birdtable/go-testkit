//go:build !linux

package contain

import "testing"

// capsMain does nothing off Linux, where no row sets a file's capabilities or reads no_new_privs.
func capsMain() (int, bool) { return 0, false }

// TestContainedMountsSkipsWhereThereIsNoNamespace: off Linux, where ContainMounts is Contain, ContainedMounts
// skips with NoContainment's words, in a run Contain contained (a Job Object on Windows) or not, and fails
// nothing. Planted failure: mountsHere true here (on Windows the skip says the run was given Contain
// alone). Risk: a test that mounts runs where there is no namespace whose mounts it could have, or skips
// for a reason that sends its reader to the wrong option.
func TestContainedMountsSkipsWhereThereIsNoNamespace(t *testing.T) {
	t.Parallel()
	if r := record(t, ContainedMounts); r.skipped != NoContainment || r.failed != "" {
		t.Errorf("ContainedMounts skipped with %q and failed with %q, want a skip with %q", r.skipped, r.failed, NoContainment)
	}
}
