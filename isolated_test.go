package testkit

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/birdtable/go-testkit/internal/homevars"
)

// recordTB is t with each failure kept, Fatalf ending its goroutine there as testing ends a test's,
// and TempDir given as temp when it is set.
type recordTB struct {
	testing.TB
	temp string
	msgs []string
}

func (*recordTB) Helper() {}

func (r *recordTB) Errorf(format string, args ...any) {
	r.msgs = append(r.msgs, fmt.Sprintf(format, args...))
}

func (r *recordTB) Fatalf(format string, args ...any) {
	r.Errorf(format, args...)
	runtime.Goexit()
}

func (r *recordTB) TempDir() string {
	if r.temp != "" {
		return r.temp
	}
	return r.TB.TempDir()
}

// isolatedSays is what Isolated failed tb with, one failure to a line, or "" when it passed.
func isolatedSays(tb *recordTB) string {
	var wg sync.WaitGroup
	wg.Go(func() { Isolated(tb) })
	wg.Wait()
	return strings.Join(tb.msgs, "\n")
}

// TestIsolatedPassesOnlyTheEnvironmentThisProcessMade: under Main, Isolated passes the environment as
// Main made it, with a name the run kept, and under GoOffline; and fails it with a name Clean drops, a
// name Isolate set holding another value, a setting of offline changed under GoOffline, t.TempDir
// outside the folder, no Isolate in force, an Isolate's environment this process did not make, as a
// child that runs no Main inherits it from its parent run, and the environment of another Isolate's
// folder. The planted red is Isolated with no check of the folder this process made, reading the
// environment alone as inForce does: it passes another Isolate's folder, and fails the handed-down
// row on t.TempDir alone, not as handed down.
//
// What it costs when this fails: a package's test of its own isolation passes while a test reads the
// person's home or a variable of the run's, or while the tests run in a folder a parent made and
// removes.
func TestIsolatedPassesOnlyTheEnvironmentThisProcessMade(t *testing.T) {
	// serial: run clears the process's environment, which every test reads; its restore puts it back.
	// Each row is a subtest, so its t.TempDir is made beneath the folder of that row's run.
	setenv := func(k, v string) func(*testing.T) {
		return func(t *testing.T) {
			t.Helper()
			if err := os.Setenv(k, v); err != nil {
				t.Fatal(err)
			}
		}
	}
	// elsewhere is an absolute path outside every folder of the run's, on Windows on the temp folder's volume.
	elsewhere := filepath.Join(filepath.VolumeName(os.TempDir()), string(filepath.Separator), "elsewhere")
	start := append(planted(), "GOWORK=off")
	ran := 0
	for row, tc := range map[string]struct {
		opts   []Option
		change func(*testing.T)
		temp   string
		want   string
	}{
		"as Main made it":                     {nil, nil, "", ""},
		"a name the run kept":                 {[]Option{Keep("GOWORK")}, nil, "", ""},
		"under GoOffline":                     {[]Option{GoOffline()}, nil, "", ""},
		"a name Clean drops":                  {nil, setenv("AGENT_TOKEN", "planted"), "", "AGENT_TOKEN is set, and Clean drops it"},
		"git's configuration elsewhere":       {nil, setenv(gitConfig, filepath.Join(elsewhere, "gitconfig")), "", gitConfig + " is"},
		"GOPROXY changed under GoOffline":     {[]Option{GoOffline()}, setenv("GOPROXY", "https://proxy.example"), "", "GOPROXY is"},
		"t.TempDir outside the folder":        {nil, nil, elsewhere, "t.TempDir is " + elsewhere + ", outside"},
		"no Isolate in force":                 {nil, setenv("HOME", filepath.Join(elsewhere, "home")), "", "no Isolate is in force"},
		"an Isolate a parent run handed down": {nil, func(*testing.T) { made = "" }, "", "that this process did not make"},
		"another Isolate's folder": {nil, func(t *testing.T) {
			t.Helper()
			other := filepath.Join(elsewhere, ownPrefix+"other")
			for k, v := range ownSet(other) {
				setenv(k, v)(t)
			}
		}, "", "and this process made"},
	} {
		ran++
		t.Run(row, func(t *testing.T) {
			var says string
			code, err := run(runner(func() int {
				if tc.change != nil {
					tc.change(t)
				}
				says = isolatedSays(&recordTB{TB: t, temp: tc.temp})
				return 0
			}), start, tc.opts)
			if err != nil || code != 0 {
				t.Fatalf("run: code %d, %v", code, err)
			}
			if tc.want == "" && says != "" || !strings.Contains(says, tc.want) {
				t.Errorf("Isolated says %q, want %q", says, tc.want)
			}
		})
	}
	if ran != 10 {
		t.Fatalf("ran %d rows, want 10", ran)
	}
}

// TestTheTestmainCopiesKeepNothingTheFixedListsLack: what the four private internal/testmain copies
// keep and point away needs no option of testkit's. go-run's Kept is each on the fixed kept list,
// and its Homes are homevars' for Windows, the unix ones for every other system; the go tool's folders
// go-files and go-console hand their tests are pinned by Isolate (goDirs); and go-socket's
// after-check, a goroutine leak once the tests passed, is NoLeaks after Main, which
// TestNoLeaksFailsALeakAndPassesASettledGoroutine holds.
//
// What it costs when this fails: a package moved off its copy drops a variable its tests read, or
// leaves a folder of the person's where its copy pointed it at the tests'.
func TestTheTestmainCopiesKeepNothingTheFixedListsLack(t *testing.T) {
	t.Parallel()
	goRunKept := []string{"PATH", "GORACE", "GOTRACEBACK", "GODEBUG", "GOCOVERDIR", "SYSTEMROOT", "COMSPEC"}
	unix := []string{"HOME", "TMPDIR", "TMP", "TEMP", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_RUNTIME_DIR"}
	goRunHomes := append(slices.Clone(unix), "USERPROFILE", "APPDATA", "LOCALAPPDATA")
	for _, k := range goRunKept {
		if !slices.Contains(kept(), k) {
			t.Errorf("go-run keeps %s, and the fixed kept list %v lacks it", k, kept())
		}
	}
	for _, k := range []string{"GOCACHE", "GOMODCACHE"} {
		if !slices.Contains(goDirs(), k) {
			t.Errorf("go-files and go-console hand their tests %s, and Isolate pins only %v", k, goDirs())
		}
	}
	checked := 0
	for goos, want := range map[string][]string{"windows": goRunHomes, "linux": unix, "darwin": unix, "freebsd": unix} {
		got := homevars.Folders(goos)
		if !slices.Equal(slices.Sorted(slices.Values(got)), slices.Sorted(slices.Values(want))) {
			t.Errorf("on %s homevars points %v away, and go-run %v", goos, got, want)
		}
		checked++
	}
	if checked != 4 {
		t.Fatalf("checked %d systems, want 4", checked)
	}
}
