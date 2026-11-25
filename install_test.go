package testkit

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// TestInstallRefusesANameThatIsNotOneFileOfTheFolder: install refuses, before it writes, a name that is
// empty, . or .., holds a separator or a :, or is a name the system keeps for a device (Windows's NUL
// and COM1), and a name already in the folder, which it leaves as it was.
//
// What it costs when this fails: a test's program lands outside its folder, over a file the test
// planted, or on a device.
func TestInstallRefusesANameThatIsNotOneFileOfTheFolder(t *testing.T) {
	t.Parallel()
	built := filepath.Join(t.TempDir(), "built")
	if err := os.WriteFile(built, []byte("program"), 0o600); err != nil {
		t.Fatal(err)
	}
	names := slices.Concat([]string{"", ".", "..", "../out", "a/b", `a\b`, "c:x", "/abs"}, keptNames)
	for _, name := range names {
		dir := t.TempDir()
		if path, err := install(built, dir, name, func() {}); err == nil {
			t.Errorf("install of the name %q gave %q, want a refusal", name, path)
		}
		if left, err := os.ReadDir(dir); err != nil || len(left) != 0 {
			t.Errorf("install of the name %q left %v (%v) in the folder, want nothing", name, left, err)
		}
	}
	dir := t.TempDir()
	there := filepath.Join(dir, "tool"+exeHere)
	if err := os.WriteFile(there, []byte("the test's own"), 0o600); err != nil {
		t.Fatal(err)
	}
	if path, err := install(built, dir, "tool", func() {}); err == nil {
		t.Errorf("install over a file already there gave %q, want a refusal", path)
	}
	if got, err := os.ReadFile(there); err != nil || string(got) != "the test's own" {
		t.Errorf("the file already there holds %q (%v) after the refusal, want it unchanged", got, err)
	}
}

// TestInstallFailsTheTestWhenThereIsNoProgram: Install of a Binary whose build was refused fails t and
// places nothing.
//
// What it costs when this fails: a test goes on with an empty path, and its child runs whatever the
// PATH finds.
func TestInstallFailsTheTestWhenThereIsNoProgram(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ft := &fatalTB{TB: t}
	var wg sync.WaitGroup
	wg.Go(func() { Build("-refused").Install(ft, dir, "tool") })
	wg.Wait()
	if !strings.Contains(ft.msg, "refuses a package that starts with -") {
		t.Errorf("Install of a refused build failed t with %q, want Build's refusal", ft.msg)
	}
	if left, err := os.ReadDir(dir); err != nil || len(left) != 0 {
		t.Errorf("Install of a refused build left %v (%v), want nothing", left, err)
	}
}

// fatalTB is t with Fatalf kept and its goroutine ended there, as testing ends a test's.
type fatalTB struct {
	testing.TB
	msg string
}

func (*fatalTB) Helper() {}

func (f *fatalTB) Fatalf(format string, args ...any) {
	f.msg = fmt.Sprintf(format, args...)
	runtime.Goexit()
}

// installRounds is how many copies TestAnInstalledCopyStartsWhileOtherGoroutinesFork installs and
// starts, the bound of the row: a count of work, not a time. The release tier's is 200;
// the push tier's, under -short, is 25, as 200 copies took 9 s of the push tier's 10 s budget on the
// darwin/arm64, at load 6.7 as at 13.5. The release tier runs the 200, so the row keeps its reach there.
func installRounds() int {
	if testing.Short() {
		return 25
	}
	return 200
}

// installers is how many goroutines of TestAnInstalledCopyStartsWhileOtherGoroutinesFork share its
// rounds. One after another, the release tier's 200 took 34.4 s of the 45 s test member on
// windows/amd64 and 8 s on darwin/arm64, alone; shared, a round's install and start wait
// on no other round's.
const installers = 8

// TestAnInstalledCopyStartsWhileOtherGoroutinesFork is the stress row: installRounds
// times, Install copies a built program into a fresh folder and the copy is started at once, by
// installers goroutines together, while four goroutines start the built program in a loop until the
// last copy has run. Every start of a copy runs it: none fails, ETXTBSY (`text file busy`) on Linux
// among the failures it would show.
//
// What it costs when this fails: a test that installs a fake tool and starts it fails now and then
// on Linux, when any other test in its binary forks while the copy is being written.
func TestAnInstalledCopyStartsWhileOtherGoroutinesFork(t *testing.T) {
	t.Parallel()
	b := Build(plantProgram(t, progStreams))
	built, err := b.resolve()
	if err != nil {
		t.Fatalf("the build of the program failed: %v", err)
	}
	// started is whether one start ran the program, which exits 3; any other error is its start's.
	started := func(path string) error {
		err := exec.CommandContext(t.Context(), path).Run()
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 3 {
			return nil
		}
		return fmt.Errorf("%s: %w, want exit 3", path, err)
	}
	var done atomic.Bool
	var forks sync.WaitGroup
	forkErrs := make([]error, 4)
	for i := range forkErrs {
		forks.Go(func() {
			for !done.Load() && forkErrs[i] == nil {
				forkErrs[i] = started(built)
			}
		})
	}
	// The rounds are shared by installers goroutines, each taking the next until all are taken, so
	// every round still installs and starts one copy, and a copy's write meets the other installers'
	// starts as well as the four loops'.
	var (
		next     atomic.Int64
		mu       sync.Mutex
		failed   []error
		installs sync.WaitGroup
	)
	rounds := installRounds()
	for range installers {
		installs.Go(func() {
			for next.Add(1) <= int64(rounds) {
				path, err := install(built, t.TempDir(), "tool", func() {})
				if err == nil {
					err = started(path)
				}
				if err != nil {
					mu.Lock()
					failed = append(failed, err)
					mu.Unlock()
				}
			}
		})
	}
	installs.Wait()
	done.Store(true)
	forks.Wait()
	if err := errors.Join(forkErrs...); err != nil {
		t.Errorf("a start of the built program beside the installs failed: %v", err)
	}
	if len(failed) > 0 {
		t.Errorf("%d of %d installed copies did not run, the first: %v", len(failed), rounds, failed[0])
	}
}
