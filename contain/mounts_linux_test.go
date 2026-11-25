package contain

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/birdtable/go-testkit"
	"kernel.org/pub/linux/libs/security/libcap/cap"
)

const tmpfsMagic = 0x01021994 // TMPFS_MAGIC of linux/magic.h, what statfs names a tmpfs by

// The lines a ContainMounts run of this binary logs, which the rows that start it read: which of its two
// cases it found itself in, and the uid its tests ran as.
const (
	mountsHeld    = "mounts: the tests hold CAP_SYS_ADMIN of the namespace"
	mountsNested  = "mounts: refused, a test of a run as uid 0 contains no binary it starts, and the tests hold no capability"
	mountsCovered = "mounts: contain's initialization found the run not entered and kept nothing, and Main kept the capability"
	uidLine       = "the tests run as uid "
	cgoLine       = "the tests' binary links cgo: "
)

// The row a ContainMounts run of this binary runs when its /proc was covered before contain's
// initialization, and the variable that asks for that.
const (
	coveredRow   = "a_run_whose_initialization_kept_nothing_keeps_the_capability_from_what_its_tests_start"
	coverProcVar = "TESTENV_TEST_COVER_PROC"
)

// What covering /proc did, in the tests of a ContainMounts run whose environment held coverProcVar: asked,
// and the mount's error. It is a variable's initialization, which Go runs before any init of the package,
// so before contain's own, as code of a package that imports no contain may run:
// contain's initialization then finds the run not entered and keeps nothing, and TestMain uncovers /proc
// before Main.
var coverAsked, coverErr = coverProc() //nolint:gochecknoglobals // set once, before contain's initialization

// coverProc covers /proc with a tmpfs, when the run asks for it.
func coverProc() (bool, error) {
	if mode, _, _ := strings.Cut(os.Getenv(containVar), ":"); mode != partMounts || os.Getenv(coverProcVar) == "" {
		return false, nil
	}
	return true, syscall.Mount("contain-cover", "/proc", "tmpfs", 0, "")
}

// The variables TestMain reads before Main, which Main's own environment then drops: a file this binary
// sets file capabilities on and ends, the program with them that a ContainMounts run's tests start, and
// no_new_privs as the run had it before Contain.
const (
	setFileCapsVar = "TESTENV_TEST_SET_FILE_CAPS"
	fileCapsVar    = "TESTENV_TEST_FILE_CAPS"
	noNewPrivsVar  = "TESTENV_TEST_NO_NEW_PRIVS"
	openInitVar    = "TESTENV_TEST_OPEN_INIT" // a file this binary writes what initOpens gives to, and ends
)

// vfsCapRevision2 is VFS_CAP_REVISION_2 of linux/capability.h, struct vfs_cap_data's revision, here with
// no VFS_CAP_FLAGS_EFFECTIVE: with it the kernel refuses the execve of a file whose permitted capabilities
// the bounding set masks, and the row would read no status at all.
const vfsCapRevision2 = 0x02000000

// The row a ContainMounts run of this binary runs when it is given a program with file capabilities.
const fileCapsRow = "a_program_with_file_capabilities_a_test_starts_gains_none"

// What TestMain read before Main, for the rows.
var (
	fileCapsProgram  string            //nolint:gochecknoglobals // set once by TestMain, before any test runs
	noNewPrivsBefore string            //nolint:gochecknoglobals // set once by TestMain, before any test runs
	beforeMain       map[string]string //nolint:gochecknoglobals // set once by TestMain, before Main, in the tests of a ContainMounts run
)

// capsMain is TestMain's part for these rows: it sets the file capabilities of the file setFileCapsVar
// names, or writes what initOpens gives to the file openInitVar names, and is done; or records what the
// rows read and goes on, starting a program before Main in the tests of a ContainMounts run.
func capsMain() (int, bool) {
	if coverAsked && coverErr == nil {
		// coverProc covered /proc before contain's initialization, which then kept nothing; it is uncovered
		// here, before Main, so Main finds the run entered.
		if err := syscall.Unmount("/proc", 0); err != nil {
			panic(fmt.Errorf("uncovering /proc before Main: %w", err))
		}
	}
	if out := os.Getenv(openInitVar); out != "" {
		lines, err := initOpens()
		if err == nil {
			err = os.WriteFile(out, []byte(strings.Join(lines, "\n")), 0o600)
		}
		if err != nil {
			panic(fmt.Errorf("opening the files of PID 1's threads: %w", err)) // the row fails with what it printed
		}
		return 0, true
	}
	if mode, _, _ := strings.Cut(os.Getenv(containVar), ":"); mode == partMounts {
		out, err := exec.CommandContext(context.Background(), "/bin/cat", "/proc/self/status").Output()
		if err != nil {
			panic(fmt.Errorf("starting /bin/cat before Main: %w", err))
		}
		beforeMain = statusOf(bufio.NewScanner(strings.NewReader(string(out))), []string{"Cap"})
	}
	if p := os.Getenv(setFileCapsVar); p != "" {
		// Every capability of the low word permitted: a program that gains them holds them in CapPrm.
		b := make([]byte, 20)
		binary.LittleEndian.PutUint32(b[0:], vfsCapRevision2)
		binary.LittleEndian.PutUint32(b[4:], 0xffffffff)
		if err := syscall.Setxattr(p, "security.capability", b, 0); err != nil {
			panic(fmt.Errorf("setting the file capabilities of %s: %w", p, err)) // runRows fails with what it printed
		}
		return 0, true
	}
	fileCapsProgram = os.Getenv(fileCapsVar)
	noNewPrivsBefore = os.Getenv(noNewPrivsVar)
	if noNewPrivsBefore == "" {
		if b, err := os.ReadFile("/proc/self/status"); err == nil {
			noNewPrivsBefore = statusOf(bufio.NewScanner(strings.NewReader(string(b))), []string{"NoNewPrivs"})["NoNewPrivs"]
		}
		if err := os.Setenv(noNewPrivsVar, noNewPrivsBefore); err != nil {
			panic(err)
		}
	}
	return 0, false
}

// The names of the rows a ContainMounts run of this binary runs, each a subtest of TestContainMounts.
var mountsRows = []string{ //nolint:gochecknoglobals // a fixed list, read by the rows that start a ContainMounts run
	"a_test_mounts_a_tmpfs_and_reads_a_file_back",
	"every_thread_holds_CAP_SYS_ADMIN_alone_and_nothing_inheritable_or_ambient",
	"a_program_a_test_starts_holds_no_capability",
	"a_test_cannot_raise_CAP_SYS_ADMIN_into_the_ambient_set",
	"a_program_TestMain_starts_before_Main_holds_no_capability",
	"a_test_and_a_program_it_starts_cannot_open_the_memory_or_maps_of_init",
	"the_tests_start_under_every_securebit_of_the_ContainMounts_drop",
}

// initOpens is what opening the memory, read-write, and the maps of each thread of PID 1 gives, a line
// per file: its path, a tab, and "refused", "opened", or the error the open gave.
func initOpens() ([]string, error) {
	tasks, err := os.ReadDir("/proc/1/task")
	if err != nil {
		return nil, err
	}
	var lines []string
	for _, task := range tasks {
		for name, flag := range map[string]int{"mem": os.O_RDWR, "maps": os.O_RDONLY} {
			p := filepath.Join("/proc/1/task", task.Name(), name)
			f, err := os.OpenFile(p, flag, 0)
			word := "refused"
			switch {
			case err == nil:
				word = "opened"
				_ = f.Close()
			case !errors.Is(err, fs.ErrPermission):
				word = err.Error()
			}
			lines = append(lines, p+"\t"+word)
		}
	}
	return lines, nil
}

// wantInitRefused fails t unless a test, and a program it starts, are refused the memory and the maps of
// every thread of PID 1.
func wantInitRefused(t *testing.T) {
	t.Helper()
	lines, err := initOpens()
	if err != nil {
		t.Fatalf("the threads of PID 1: %v", err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "opens")
	cmd := exec.CommandContext(t.Context(), self, "-test.run=^$") //nolint:gosec // G204: the test binary itself, started as a program a test starts
	cmd.Env = append(os.Environ(), openInitVar+"="+out)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the program a test started: %v\n%s", err, b)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	for who, ls := range map[string][]string{"a test": lines, "a program a test started": strings.Split(string(b), "\n")} {
		if len(ls) < 2 {
			t.Errorf("%s opened %d files of PID 1's threads, want two for each", who, len(ls))
		}
		for _, l := range ls {
			if p, word, _ := strings.Cut(l, "\t"); word != "refused" {
				t.Errorf("%s: %s is %s, want refused", who, p, word)
			}
		}
	}
}

// status is the fields of a /proc status file whose names start with one of the prefixes.
func status(t *testing.T, path string, prefixes ...string) map[string]string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	return statusOf(bufio.NewScanner(f), prefixes)
}

func statusOf(sc *bufio.Scanner, prefixes []string) map[string]string {
	seen := map[string]string{}
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		for _, p := range prefixes {
			if strings.HasPrefix(k, p) {
				seen[k] = strings.TrimSpace(v)
			}
		}
	}
	return seen
}

// wantCaps fails t unless each set of seen is want, a hexadecimal mask; where names what was read.
func wantCaps(t *testing.T, where string, seen map[string]string, want map[string]uint64) {
	t.Helper()
	for k, w := range want {
		v, ok := seen[k]
		var got uint64
		if _, err := fmt.Sscanf(v, "%x", &got); !ok || err != nil || got != w {
			t.Errorf("%s: %s is %q, want %#x", where, k, v, w)
		}
	}
}

// programStatus is the capability and no_new_privs fields of the program cat, a copy of /bin/cat or the
// file itself, started by this test to print its own /proc status.
func programStatus(t *testing.T, cat string) map[string]string {
	t.Helper()
	out, err := exec.CommandContext(t.Context(), cat, "/proc/self/status").Output()
	if err != nil {
		t.Fatalf("starting %s: %v", cat, err)
	}
	return statusOf(bufio.NewScanner(strings.NewReader(string(out))), []string{"Cap", "NoNewPrivs"})
}

// mountTmpfs mounts a tmpfs on a folder of t's, unmounted when t ends, and is the folder and the mount's error.
func mountTmpfs(t *testing.T) (string, error) {
	t.Helper()
	dir := t.TempDir()
	if err := syscall.Mount("tmpfs", dir, "tmpfs", syscall.MS_NOSUID|syscall.MS_NODEV, "size=1m"); err != nil {
		return dir, err
	}
	t.Cleanup(func() {
		if err := syscall.Unmount(dir, 0); err != nil {
			t.Errorf("the tmpfs on %s could not be unmounted: %v", dir, err)
		}
	})
	return dir, nil
}

// TestContainMountsKeepsTheMountCapabilityAndNothingTheTestsStartInheritsIt: this binary, built with
// CGO_ENABLED=0, run again with ContainMounts, runs each of mountsRows and passes them, as the uid this
// run has; and this binary itself, run again so, passes them too, whether it links cgo (as a default build
// and a -race build do, by libcap/psx) or not: libcap/cap sets a capability on every thread in both, so
// the cgo run that held none in go-proc's testenv holds it here. The run says which it
// was (cgoLine). Planted failures, each red here: keepMounts setting
// its sets and clearing the ambient set on its own thread alone (the other threads hold CAP_SYS_ADMIN in
// the ambient and inheritable sets); keepMounts leaving the inheritable set as init passed it (every
// thread, and a program a test starts, holds CAP_SYS_ADMIN inheritable); init not setting
// SECBIT_NO_CAP_AMBIENT_RAISE and its lock (the row the_tests_start_under_every_securebit_of_the_ContainMounts_drop
// reads 0xf, not 0xcf; the row a_test_cannot_raise_CAP_SYS_ADMIN_into_the_ambient_set stays green then, since
// the empty bounding set refuses the inheritable bit the raise needs first, so it shows the raise out of
// reach and not the securebit); init leaving
// the bounding set as it was (CapBnd is the namespace's full set, on every thread and in a program a test
// starts); init not setting no_new_privs (NoNewPrivs is 0 there); keepMounts run by Main in place of
// contain's initialization (a program TestMain starts before Main holds CAP_SYS_ADMIN effective, permitted
// and ambient); init left dumpable (a test opens the memory of the thread init started it from); Main
// taking contain's initialization as having kept the capability when it ran no keepMounts (in a run whose
// /proc a variable's initialization covered then, the tests hold CAP_SYS_ADMIN inheritable and ambient, and a program a test
// starts holds it in all four sets). In a run
// that is uid 0 already, both builds, started as uid 0 by a test that holds no capability, are refused the
// containment with EPERM, as ContainMounts' doc says, and each wants its tests holding nothing and
// mounting nothing. Risk: a test that mounts runs empty, or a program a test starts holds CAP_SYS_ADMIN of
// the namespace.
func TestContainMountsKeepsTheMountCapabilityAndNothingTheTestsStartInheritsIt(t *testing.T) {
	if mountsRun {
		mountsInside(t)
		return
	}
	t.Parallel()
	Contained(t)
	bothMountsRuns(t, false)
}

// bothMountsRuns runs the CGO_ENABLED=0 build and this binary with ContainMounts, as uid 0 of a user
// namespace of their own when uid0, and fails t unless each is in its case and passes it. The uid-0 runs
// are also given a program with file capabilities, which only they can gain from: Linux honours a file's
// capabilities only where the uid 0 they were written by is uid 0 of the namespace that execs it or of one
// above, and the tests of a run as uid 0 are that uid.
func bothMountsRuns(t *testing.T, uid0 bool) {
	t.Helper()
	uid, caps := os.Getuid(), ""
	if uid0 {
		uid, caps = 0, fileCapsCat(t)
	}
	held := mountsHeld
	if uid == 0 && !uid0 {
		held = mountsNested // a test of a run as uid 0 contains no binary it starts: the refusal ContainMounts' doc names
	}
	bin := noCgoTestBinary(t)
	out := runMounts(t, bin, uid0, caps)
	wantMountsRun(t, "the CGO_ENABLED=0 build", out, held, uid, caps != "")
	if !uid0 && held == mountsHeld {
		// The same build, its /proc covered before contain's initialization and uncovered by TestMain before
		// Main: Main runs keepMounts itself.
		out = runRows(t, bin, "^TestContainMountsKeepsTheMountCapabilityAndNothingTheTestsStartInheritsIt$", append(os.Environ(), mountsVar+"=1", coverProcVar+"=1"), false)
		wantMountsRun(t, "the CGO_ENABLED=0 build, /proc covered at contain's initialization", out, mountsCovered, uid, false)
		if !strings.Contains(out, "--- PASS: TestContainMountsKeepsTheMountCapabilityAndNothingTheTestsStartInheritsIt/"+coveredRow+" ") {
			t.Errorf("the run whose /proc was covered: the row %s did not pass:\n%s", coveredRow, out)
		}
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	out = runMounts(t, self, uid0, caps)
	if held == mountsNested {
		wantMountsRun(t, "this binary", out, mountsNested, uid, false)
	} else {
		// The cgo row: a build that links cgo, as a -race build does, holds the capability too.
		wantMountsRun(t, "this binary, which links cgo: "+fmt.Sprint(linksCgo), out, mountsHeld, uid, caps != "")
		if !strings.Contains(out, cgoLine+fmt.Sprint(linksCgo)+"\n") {
			t.Errorf("this binary: the run does not say %q:\n%s", cgoLine+fmt.Sprint(linksCgo), out)
		}
	}
	if !uid0 && held == mountsHeld {
		// This binary too, its /proc covered at contain's initialization, as the CGO_ENABLED=0 build's above:
		// where it links cgo (a -race build does), Main's keepMounts sets every thread through libcap/psx's C,
		// which reads /proc/<pid>/task, and the run must hold the capability there as well.
		out = runRows(t, self, "^TestContainMountsKeepsTheMountCapabilityAndNothingTheTestsStartInheritsIt$", append(os.Environ(), mountsVar+"=1", coverProcVar+"=1"), false)
		which := "this binary, which links cgo: " + fmt.Sprint(linksCgo) + ", /proc covered at contain's initialization"
		wantMountsRun(t, which, out, mountsCovered, uid, false)
		if !strings.Contains(out, "--- PASS: TestContainMountsKeepsTheMountCapabilityAndNothingTheTestsStartInheritsIt/"+coveredRow+" ") {
			t.Errorf("%s: the row %s did not pass:\n%s", which, coveredRow, out)
		}
	}
}

// fileCapsCat is a copy of /bin/cat in a folder of t's with every capability of the low word as its file
// capabilities, permitted, set by this binary as uid 0 of a user namespace of its own, which holds
// CAP_SETFCAP over a file its uid owns.
func fileCapsCat(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("/bin/cat")
	if err != nil {
		t.Fatal(err)
	}
	cat := filepath.Join(t.TempDir(), "cat")
	if err := os.WriteFile(cat, b, 0o700); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	runRows(t, self, "^$", append(os.Environ(), setFileCapsVar+"="+cat), true)
	if n, err := syscall.Getxattr(cat, "security.capability", nil); err != nil || n == 0 {
		t.Fatalf("the copy of cat has no file capabilities: %d bytes, %v", n, err)
	}
	return cat
}

// noCgoTestBinary is this package's test binary built with CGO_ENABLED=0 and without -race, in the clean
// environment the test has, as Build compiles.
func noCgoTestBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "contain.test")
	cmd := exec.CommandContext(t.Context(), "go", "test", "-c", "-o", bin, ".") //nolint:gosec // G204: go, building this package into a folder of the test's
	cmd.Env = append(testkit.Clean(os.Environ()), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building the test binary with CGO_ENABLED=0: %v\n%s", err, out)
	}
	return bin
}

// runMounts runs the test binary bin with ContainMounts, its one row TestContainMounts, and is what it
// wrote; caps, if not "", is the program with file capabilities its tests start.
func runMounts(t *testing.T, bin string, uid0 bool, caps string) string {
	t.Helper()
	env := append(os.Environ(), mountsVar+"=1")
	if caps != "" {
		env = append(env, fileCapsVar+"="+caps)
	}
	return runRows(t, bin, "^TestContainMountsKeepsTheMountCapabilityAndNothingTheTestsStartInheritsIt$", env, uid0)
}

// runRows runs the test binary bin, the rows the pattern names, in the environment env, and is what it
// wrote; when uid0, as uid 0 of a user namespace of its own that maps it to this run's uid, unprivileged, as
// unshare --user --map-root-user runs a program. Linux lets a process map uid 0 of the namespace it makes
// to uid 0 of its own only with CAP_SETFCAP, so a test of a run as uid 0, which holds no capability,
// cannot contain a binary it starts: these runs are started from here, by a test of a uid that is not 0,
// and a run that is uid 0 already wants that refusal instead (TestAsUID0, bothMountsRuns).
func runRows(t *testing.T, bin, pattern string, env []string, uid0 bool) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), bin, "-test.run", pattern, "-test.v", "-test.count=1") //nolint:gosec // G204: this package's own test binary, this one or the build beside it
	cmd.Env = env
	if uid0 {
		cmd.SysProcAttr = asUID0()
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the run of %s, %s, failed: %v\n%s", bin, pattern, err, out)
	}
	return string(out)
}

// asUID0 starts a process as uid 0 of a user namespace of its own, mapped to this run's uid and gid.
func asUID0() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		Cloneflags:  syscall.CLONE_NEWUSER,
		UidMappings: []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Getuid(), Size: 1}},
		GidMappings: []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Getgid(), Size: 1}},
	}
}

// wantMountsRun fails t unless out is a ContainMounts run that found itself in the case want, ran as uid,
// passed every row of that case, fileCapsRow too when caps, and skipped and failed none.
func wantMountsRun(t *testing.T, which, out, want string, uid int, caps bool) {
	t.Helper()
	if !strings.Contains(out, want) {
		t.Errorf("%s: the run does not say %q:\n%s", which, want, out)
	}
	if u := fmt.Sprintf("%s%d\n", uidLine, uid); !strings.Contains(out, u) {
		t.Errorf("%s: the run's tests did not run as uid %d:\n%s", which, uid, out)
	}
	if strings.Contains(out, "--- SKIP") || strings.Contains(out, "--- FAIL") {
		t.Errorf("%s: a row skipped or failed:\n%s", which, out)
	}
	if want != mountsHeld {
		return
	}
	rows := mountsRows
	if caps {
		rows = append(slices.Clone(rows), fileCapsRow)
	}
	for _, row := range rows {
		if !strings.Contains(out, "--- PASS: TestContainMountsKeepsTheMountCapabilityAndNothingTheTestsStartInheritsIt/"+row+" ") {
			t.Errorf("%s: the row %s did not pass:\n%s", which, row, out)
		}
	}
}

// mountsInside is TestContainMounts in a ContainMounts run of this binary, its body: the lines it fails on
// are its own, so it is no helper.
func mountsInside(t *testing.T) { //nolint:thelper // the test's own body, run in the other mode
	t.Logf("%s%d", uidLine, os.Getuid())
	t.Logf("%s%t", cgoLine, linksCgo)
	r := record(t, ContainedMounts)
	if r.failed != "" {
		t.Fatalf("ContainedMounts failed: %s", r.failed)
	}
	if os.Getuid() == 0 && strings.HasPrefix(r.skipped, Refused) && strings.HasSuffix(r.skipped, syscall.EPERM.Error()) {
		// Started as uid 0 by a test of a contained run, which holds no capability: Linux maps uid 0 of the
		// namespace this run would make to uid 0 only with CAP_SETFCAP, so the containment is refused, as
		// ContainMounts' doc says, and the run holds nothing and mounts nothing.
		wantCaps(t, "the tests of a refused uid-0 run", status(t, "/proc/self/status", "Cap"), map[string]uint64{"CapEff": 0, "CapPrm": 0, "CapAmb": 0, "CapInh": 0, "CapBnd": 0})
		if _, err := mountTmpfs(t); !errors.Is(err, syscall.EPERM) {
			t.Fatalf("a refused uid-0 run mounted a tmpfs: %v, want EPERM", err)
		}
		t.Log(mountsNested)
		return
	}
	if r.skipped != "" {
		t.Fatalf("ContainedMounts skipped a run that can keep the capability: %s", r.skipped)
	}
	if coverAsked {
		t.Run(coveredRow, coveredInside)
		t.Log(mountsCovered)
		return
	}
	t.Run(mountsRows[0], func(t *testing.T) {
		dir, err := mountTmpfs(t)
		if err != nil {
			t.Fatalf("mounting a tmpfs on %s: %v", dir, err)
		}
		var fs syscall.Statfs_t
		if err := syscall.Statfs(dir, &fs); err != nil || fs.Type != tmpfsMagic {
			t.Fatalf("%s is no tmpfs: type %#x, %v", dir, fs.Type, err)
		}
		f := filepath.Join(dir, "file")
		if err := os.WriteFile(f, []byte("mounted\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if b, err := os.ReadFile(f); err != nil || string(b) != "mounted\n" {
			t.Fatalf("the file read back is %q, %v", b, err)
		}
	})
	t.Run(mountsRows[1], func(t *testing.T) {
		tasks, err := os.ReadDir("/proc/self/task")
		if err != nil || len(tasks) < 2 {
			t.Fatalf("the threads of the tests: %d, %v; want more than one", len(tasks), err)
		}
		for _, task := range tasks {
			p := filepath.Join("/proc/self/task", task.Name(), "status")
			wantCaps(t, "thread "+task.Name(), status(t, p, "Cap", "NoNewPrivs"), map[string]uint64{"CapEff": uint64(sysAdmin), "CapPrm": uint64(sysAdmin), "CapAmb": 0, "CapInh": 0, "CapBnd": 0, "NoNewPrivs": 1})
		}
	})
	t.Run(mountsRows[2], func(t *testing.T) {
		wantCaps(t, "a program a test started", programStatus(t, "/bin/cat"), map[string]uint64{"CapEff": 0, "CapPrm": 0, "CapAmb": 0, "CapInh": 0, "CapBnd": 0, "NoNewPrivs": 1})
	})
	if fileCapsProgram != "" {
		t.Run(fileCapsRow, func(t *testing.T) {
			wantCaps(t, "a program with file capabilities a test started", programStatus(t, fileCapsProgram), map[string]uint64{"CapEff": 0, "CapPrm": 0, "CapAmb": 0, "CapInh": 0, "NoNewPrivs": 1})
		})
	}
	t.Run(mountsRows[3], func(t *testing.T) {
		raised := make(chan error)
		go func() {
			// Locked and never unlocked: the thread's sets are changed, and it ends with this goroutine.
			runtime.LockOSThread()
			d, err := threadCaps()
			if err == nil {
				// Refused, in the tests of a ContainMounts run: the bounding set is empty on every thread, and capset
				// takes no new inheritable bit outside it (security/commoncap.c), so the raise below, which needs the
				// bit inheritable, is out of reach. Were the bounding set left full, capset would pass and the raise
				// would meet SECBIT_NO_CAP_AMBIENT_RAISE, which the row mountsRows[6] holds by itself.
				d[0].inheritable |= sysAdmin
				err = setThreadCaps(d)
			}
			if err != nil {
				raised <- fmt.Errorf("the inheritable set: %w", err)
				return
			}
			_, err = prctl(prCapAmbient, prCapAmbientRaise, capSysAdmin)
			raised <- err
		}()
		if err := <-raised; !errors.Is(err, syscall.EPERM) {
			t.Fatalf("raising CAP_SYS_ADMIN into the ambient set gave %v, want EPERM", err)
		}
	})
	t.Run(mountsRows[4], func(t *testing.T) {
		if beforeMain == nil {
			t.Fatal("TestMain started no program before Main")
		}
		wantCaps(t, "a program TestMain started before Main", beforeMain, map[string]uint64{"CapEff": 0, "CapPrm": 0, "CapAmb": 0})
	})
	t.Run(mountsRows[5], wantInitRefused)
	t.Run(mountsRows[6], func(t *testing.T) {
		// The six bits named here, not testsBits(true), so a bit that testsBits leaves out is red.
		want := uintptr(cap.SecbitNoRoot | cap.SecbitNoRootLocked | cap.SecbitNoSetUIDFixup | cap.SecbitNoSetUIDFixupLocked |
			cap.SecbitNoCapAmbientRaise | cap.SecbitNoCapAmbientRaiseLocked)
		if got, err := prctl(prGetSecurebits, 0, 0); err != nil || got&want != want {
			t.Errorf("the tests' securebits are %#x (%v), want %#x set", got, err, want)
		}
	})
	t.Log(mountsHeld)
}

// coveredInside is coveredRow, in a ContainMounts run whose /proc coverProc covered before contain's
// initialization, so that it found the run not entered and ran no keepMounts: Main ran it, so no thread of
// the tests holds the capability inheritable or ambient, and a program a test starts holds none.
func coveredInside(t *testing.T) {
	if coverErr != nil {
		t.Fatalf("covering /proc before contain's initialization: %v", coverErr)
	}
	if keptAtInit.ran {
		t.Fatal("contain's initialization ran keepMounts with /proc covered, so the row tests nothing")
	}
	tasks, err := os.ReadDir("/proc/self/task")
	if err != nil || len(tasks) < 2 {
		t.Fatalf("the threads of the tests: %d, %v; want more than one", len(tasks), err)
	}
	for _, task := range tasks {
		p := filepath.Join("/proc/self/task", task.Name(), "status")
		wantCaps(t, "thread "+task.Name(), status(t, p, "Cap"), map[string]uint64{"CapAmb": 0, "CapInh": 0})
	}
	wantCaps(t, "a program a test started", programStatus(t, "/bin/cat"), map[string]uint64{"CapEff": 0, "CapPrm": 0, "CapAmb": 0, "CapInh": 0})
}

// TestATestOfAContainedRunCannotMount: under Contain, without ContainMounts, a test that mounts a tmpfs on
// a folder of its own is refused with EPERM, and ContainedMounts skips it, saying Main was not given
// ContainMounts. Planted failure: init passing CAP_SYS_ADMIN to the tests whatever the option (the mount
// succeeds). Risk: Contain's default, a run
// whose tests hold no capability, quietly becoming one whose tests can mount.
func TestATestOfAContainedRunCannotMount(t *testing.T) {
	t.Parallel()
	Contained(t)
	if dir, err := mountTmpfs(t); !errors.Is(err, syscall.EPERM) {
		t.Fatalf("a test of a contained run mounted a tmpfs on %s: %v, want EPERM", dir, err)
	}
	r := record(t, ContainedMounts)
	if r.skipped != notAskedMounts || r.failed != "" {
		t.Errorf("ContainedMounts skipped with %q and failed with %q, want a skip with %q", r.skipped, r.failed, notAskedMounts)
	}
}

// TestInitStartsTheTestsFromTheThreadItDroppedItsCapabilitiesOn: the tests start under the securebits init
// set and locked, SECBIT_NOROOT and SECBIT_NO_SETUID_FIXUP, and with an empty inheritable set, which only
// the thread init set them on carries. Planted failure: the launcher's callback dropping nothing (the tests
// hold init's ambient CAP_SYS_ADMIN and CAP_SETPCAP); the securebits not set. Risk: the tests started from a
// thread of init that still holds CAP_SYS_ADMIN in its ambient set, which they and every child inherit.
func TestInitStartsTheTestsFromTheThreadItDroppedItsCapabilitiesOn(t *testing.T) {
	t.Parallel()
	Contained(t)
	got, err := prctl(prGetSecurebits, 0, 0)
	bits := uintptr(testsBits(false))
	if err != nil || got&bits != bits {
		t.Errorf("the tests' securebits are %#x (%v), want %#x set", got, err, bits)
	}
	wantCaps(t, "the tests", status(t, "/proc/self/status", "Cap"), map[string]uint64{"CapInh": 0, "CapAmb": 0, "CapBnd": 0})
}

// TestUnderContainAProgramATestStartsHasTheNoNewPrivsTheTestSet: plain Contain leaves no_new_privs alone, so
// a program a test starts reads the run's own, as it was before Contain, and, started from a thread on which
// the test set no_new_privs, reads 1. Planted failure: init setting no_new_privs under plain Contain too (the
// program reads 1 where the run had 0). Risk: proc's own rows, which read a program's no_new_privs, passing
// vacuously in every contained run.
func TestUnderContainAProgramATestStartsHasTheNoNewPrivsTheTestSet(t *testing.T) {
	t.Parallel()
	Contained(t)
	if noNewPrivsBefore == "" {
		t.Fatal("TestMain recorded no no_new_privs of the run before Contain")
	}
	if got := programStatus(t, "/bin/cat")["NoNewPrivs"]; got != noNewPrivsBefore {
		t.Errorf("a program a test started reads NoNewPrivs %q, want the run's own %q", got, noNewPrivsBefore)
	}
	seen := make(chan map[string]string, 1)
	go func() {
		// Locked and never unlocked: no_new_privs, once set, stays on the thread, which ends with this goroutine.
		runtime.LockOSThread()
		if _, err := prctl(prSetNoNewPrivs, 1, 0); err != nil {
			t.Errorf("setting no_new_privs: %v", err)
			seen <- nil
			return
		}
		out, err := exec.CommandContext(t.Context(), "/bin/cat", "/proc/self/status").Output()
		if err != nil {
			t.Errorf("starting /bin/cat: %v", err)
			seen <- nil
			return
		}
		seen <- statusOf(bufio.NewScanner(strings.NewReader(string(out))), []string{"NoNewPrivs"})
	}()
	if s := <-seen; s != nil && s["NoNewPrivs"] != "1" {
		t.Errorf("a program started from a thread the test set no_new_privs on reads NoNewPrivs %q, want 1", s["NoNewPrivs"])
	}
}

// TestNoTestOfAContainedRunNorWhatItStartsOpensTheMemoryOfInit: under Contain, a test, and a program it
// starts, are refused the memory (read-write) and the maps of every thread of PID 1, whose other threads
// hold what the tests do not. Planted failure: init left dumpable (a test opens, read-write, the memory of
// the thread init started it from, whose sets are the tests' own, and a program it starts reads its maps).
// Risk: a test that writes init's memory steers a thread that holds the namespace's capabilities.
func TestNoTestOfAContainedRunNorWhatItStartsOpensTheMemoryOfInit(t *testing.T) {
	t.Parallel()
	Contained(t)
	wantInitRefused(t)
}

// TestAsUID0TheContainedTestsAndTheirChildrenHoldNoCapability: as uid 0 of a user namespace of their own,
// unprivileged, as unshare --user --map-root-user runs a program, so Contain maps uid 0 to itself and the
// tests are uid 0: this binary passes TestTheTestsInAContainedRunHoldNoCapability (the tests' own sets
// under Contain) and TestNoTestOfAContainedRunNorWhatItStartsOpensTheMemoryOfInit, and the ContainMounts
// runs of TestContainMounts pass, a program their tests start holding nothing, a copy of cat with every
// capability as its file capabilities among them. Planted failures: init not setting SECBIT_NOROOT and
// leaving the bounding set as it was (the uid-0 tests, and every program they start, regain the
// namespace's full set on execve; with the bounding set empty, it masks that by itself); init leaving
// the bounding set as it was (the copy of cat holds CAP_SYS_ADMIN permitted, the most no_new_privs lets
// an exec keep); init left dumpable (as uid 0, a test opens the memory of init's thread). In a run that is
// uid 0 already (as root, or under unshare --user --map-root-user), whose rows run as uid 0 by themselves,
// it wants the refusal the docs name instead: starting this binary as uid 0 of a namespace of its own is
// EPERM, since the tests hold no CAP_SETFCAP (planted failure: init not setting SECBIT_NOROOT and leaving
// the bounding set as it was, which gives the uid-0 tests the namespace's full set back, so the start
// succeeds). Risk: a test binary run as root
// in a container, whose tests and their children hold every capability of Contain's namespace.
func TestAsUID0TheContainedTestsAndTheirChildrenHoldNoCapability(t *testing.T) {
	t.Parallel()
	Contained(t)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if os.Getuid() == 0 {
		// This run is uid 0 already, so the rows run here as uid 0. Its tests hold no capability, and Linux
		// maps uid 0 of a new user namespace to uid 0 only with CAP_SETFCAP: the refusal the docs name.
		cmd := exec.CommandContext(t.Context(), self, "-test.run=^$") //nolint:gosec // G204: this package's own test binary
		cmd.SysProcAttr = asUID0()
		if err := cmd.Run(); !errors.Is(err, syscall.EPERM) {
			t.Fatalf("a test of a run as uid 0 started a binary as uid 0 of a namespace of its own: %v, want EPERM", err)
		}
		return
	}
	rows := []string{"TestTheTestsInAContainedRunHoldNoCapability", "TestNoTestOfAContainedRunNorWhatItStartsOpensTheMemoryOfInit"}
	out := runRows(t, self, "^("+strings.Join(rows, "|")+")$", os.Environ(), true)
	for _, row := range rows {
		if !strings.Contains(out, "--- PASS: "+row+" ") || !strings.Contains(out, uidLine+"0\n") || strings.Contains(out, "--- SKIP") {
			t.Errorf("as uid 0, %s did not pass, ran as another uid, or skipped:\n%s", row, out)
		}
	}
	bothMountsRuns(t, true)
}
