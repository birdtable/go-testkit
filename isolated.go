package testkit

import (
	"os"
	"strings"
	"testing"
)

// Isolated fails t unless the test runs in the environment this process's own Main or Isolate made,
// the check each of the four private internal/testmain copies calls Hold: an Isolate is
// in force, and its folder is the one this process made, not one a parent run handed down to a child
// that runs no Main of its own; each name that Isolate set holds the value it gave, the homes, git's
// global configuration, the settings of pinned and, under GoOffline, those of offline; t.TempDir is
// beneath its folder; and the environment holds nothing Clean would drop, so no name beyond the fixed
// kept list, the run's Keep and what Isolate set. A nameless entry, as Windows keeps for each drive's
// current folder, is the system's, and is read past as Clean drops it. Each test binary makes a folder
// of its own and Isolated asks for that one, so no name per package is needed to tell them apart.
func Isolated(t testing.TB) {
	t.Helper()
	in := inForce()
	switch {
	case in.set == nil:
		t.Fatalf("no Isolate is in force: HOME is %q, and want the home of a folder Main or Isolate made", os.Getenv("HOME"))
	case made == "":
		t.Fatalf("the environment is that of an Isolate of %s that this process did not make: a parent run handed it down", in.own)
	case in.own != made:
		t.Fatalf("the Isolate in force is of %s, and this process made %s", in.own, made)
	}
	checked := 0
	for k, want := range in.set {
		if got, ok := os.LookupEnv(k); !ok || got != want {
			t.Errorf("%s is %q (set: %v), and want %q, what Isolate set", k, got, ok, want)
		}
		checked++
	}
	if checked < len(homes()) {
		t.Fatalf("checked %d names Isolate set, fewer than the %d homes it points into its folder", checked, len(homes()))
	}
	if dir := t.TempDir(); !beneath(dir, made) {
		t.Errorf("t.TempDir is %s, outside %s", dir, made)
	}
	read := 0
	for _, kv := range os.Environ() {
		k, v, _ := strings.Cut(kv, "=")
		if k == "" {
			continue
		}
		if !in.passes(k, v) {
			t.Errorf("%s is set, and Clean drops it: only the fixed kept list, the run's Keep and what Isolate set may be", k)
		}
		read++
	}
	if read < checked {
		t.Fatalf("read %d variables, fewer than the %d Isolate set", read, checked)
	}
}
