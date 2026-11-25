package testkit

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
)

// progStreams writes to both streams, echoes its stdin to stdout and exits 3, so a test reads a
// code, a stdout and a stderr that are each its own.
const progStreams = `package main

import (
	"io"
	"os"
)

func main() {
	io.WriteString(os.Stdout, "to out")
	io.WriteString(os.Stderr, "to err")
	b, _ := io.ReadAll(os.Stdin)
	os.Stdout.Write(b)
	os.Exit(3)
}
`

// progHomes prints the home, the configuration folder and the cache folder a child reads, each on its
// own line, so a test holds each under the tests' own folder.
const progHomes = `package main

import (
	"fmt"
	"os"
)

func main() {
	for _, read := range []func() (string, error){os.UserHomeDir, os.UserConfigDir, os.UserCacheDir} {
		dir, err := read()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println(dir)
	}
}
`

// progEnv prints its environment, one NAME=value a line, so a test reads what a child was handed.
const progEnv = `package main

import (
	"os"
	"strings"
)

func main() {
	os.Stdout.WriteString(strings.Join(os.Environ(), "\n"))
}
`

// childHas is whether the child's printed environment holds the variable name.
func childHas(out, name string) bool {
	for line := range strings.SplitSeq(out, "\n") {
		if n, _, ok := strings.Cut(line, "="); ok && n == name {
			return true
		}
	}
	return false
}

// plantProgram writes a one-file main package with src into a folder of the test's own and returns
// the folder, which go build takes as the package.
func plantProgram(t *testing.T, src string) string {
	t.Helper()
	dir := t.TempDir()
	wrote := 0
	for name, body := range map[string]string{
		"go.mod":  "module example.invalid/prog\n\ngo 1.27\n",
		"main.go": src,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		wrote++
	}
	if wrote != 2 {
		t.Fatalf("wrote %d of the program's 2 files", wrote)
	}
	return dir
}

// TestBuildCompilesOncePerPackageAndRunsItWithItsCodeAndStreams: Build compiles a package once, so a
// second Build of it returns the same path; Run gives the code, the stdout and the stderr of a program
// that exits 3 and writes to both; and RunInput hands the program its stdin.
//
// What it costs when this fails: every test that builds the program compiles it again, each run reads
// the wrong stream or code, or a per-request command never reaches the binary under test.
func TestBuildCompilesOncePerPackageAndRunsItWithItsCodeAndStreams(t *testing.T) {
	t.Parallel()
	pkg := plantProgram(t, progStreams)
	b := Build(pkg)
	path := b.Path()
	if path == "" || !filepath.IsAbs(path) {
		t.Fatalf("Build(%s).Path() = %q, want an absolute path", pkg, path)
	}
	if again := Build(pkg).Path(); again != path {
		t.Errorf("a second Build of the package gave %q, want the one path %q", again, path)
	}
	if r := b.Run(); r.Code != 3 || r.Stdout != "to out" || r.Stderr != "to err" {
		t.Errorf("Run gave %+v, want code 3, stdout %q, stderr %q", r, "to out", "to err")
	}
	if r := b.RunInput("fed in"); r.Code != 3 || r.Stdout != "to outfed in" || r.Stderr != "to err" {
		t.Errorf("RunInput gave %+v, want code 3, stdout %q, stderr %q", r, "to outfed in", "to err")
	}
}

// TestAChildReadsItsHomeConfigAndCacheUnderTheTestsFolder is the native home-variable proof:
// a child run through the clean environment reads os.UserHomeDir, os.UserConfigDir and
// os.UserCacheDir without error, and each is under the folder Isolate made. On Windows these read
// USERPROFILE, APPDATA and LOCALAPPDATA, which homes() now sets, where Unix reads HOME and the XDG
// names.
//
// What it costs when this fails: a test's child reads or writes the person's real home, settings or
// cache, and passes or fails on what the machine holds.
func TestAChildReadsItsHomeConfigAndCacheUnderTheTestsFolder(t *testing.T) {
	t.Parallel()
	r := Build(plantProgram(t, progHomes)).Run()
	if r.Code != 0 {
		t.Fatalf("the child: code %d, stdout %q, stderr %q", r.Code, r.Stdout, r.Stderr)
	}
	dir := own(t)
	lines := strings.Split(strings.TrimSpace(r.Stdout), "\n")
	if len(lines) != 3 {
		t.Fatalf("the child printed %q, want the home, the configuration folder and the cache folder", r.Stdout)
	}
	read := 0
	for i, name := range []string{"home", "configuration folder", "cache folder"} {
		read++
		got := strings.TrimSpace(lines[i])
		if !filepath.IsAbs(got) || !beneath(got, dir) {
			t.Errorf("the child's %s is %q, want a folder beneath the tests' %s", name, got, dir)
		}
	}
	if read != 3 {
		t.Fatalf("read %d of the 3 folders", read)
	}
}

// TestDropRemovesMoreAndKeepLetsOneThrough: a run given Drop("X_") and Keep("X_Y") drops every X_ name
// but X_Y, and a child it starts sees no name an agent or git set either.
//
// What it costs when this fails: Drop lets a name through that a project meant to strip, or Keep drops
// one a test needs, so a child runs with the wrong environment.
func TestDropRemovesMoreAndKeepLetsOneThrough(t *testing.T) {
	// serial: run clears the process's environment, which every test reads; its restore puts it back.
	start := append(planted(), "X_A=a", "X_Y=y", "AGENT_SESSION_ID=s")
	var got map[string]string
	code, err := run(runner(func() int {
		got = child(t, Clean(os.Environ()))
		return 0
	}), start, []Option{Drop("X_"), Keep("X_Y")})
	if err != nil || code != 0 {
		t.Fatalf("run: code %d, %v", code, err)
	}
	if _, ok := got["X_A"]; ok {
		t.Error(`X_A reached the child, and Drop("X_") must drop it`)
	}
	if got["X_Y"] != "y" {
		t.Errorf(`X_Y is %q, and Keep("X_Y") must let it through`, got["X_Y"])
	}
	for _, name := range []string{"AGENT_SESSION_ID", "GIT_DIR", "GITHUB_TOKEN"} {
		if _, ok := got[name]; ok {
			t.Errorf("%s reached the child, and the run never set it to pass", name)
		}
	}
}

// TestARunSeesTheCleanEnvironmentNotTheRunsOwn: a token-like name and a git name a test sets reach no
// child a Binary runs, because Run gives it the clean environment, not the test process's own.
//
// What it costs when this fails: a built child reads a token or a git variable a test set, as the
// README promises it does not, and hands it to whatever it runs.
func TestARunSeesTheCleanEnvironmentNotTheRunsOwn(t *testing.T) {
	// serial: t.Setenv forbids t.Parallel, and the names it sets every other test's child reads.
	t.Setenv("GITHUB_TOKEN", "ghp_planted")
	t.Setenv("GIT_DIR", "/planted/repo")
	out := Build(plantProgram(t, progEnv)).Run().Out()
	for _, name := range []string{"GITHUB_TOKEN", "GIT_DIR"} {
		if childHas(out, name) {
			t.Errorf("%s reached the child, and Run must give it the clean environment, not the run's own", name)
		}
	}
}

// TestBuildsOwnOptionsChangeTheChildsEnvironment: a Binary's own Keep lets a dropped name through to
// its child, and its own Drop takes a kept name away; a plain Build does neither.
//
// What it costs when this fails: a Binary's Keep or Drop is ignored, so a child runs with the same
// environment whatever the caller asked for it.
func TestBuildsOwnOptionsChangeTheChildsEnvironment(t *testing.T) {
	// serial: t.Setenv forbids t.Parallel, and the name it sets every other test's child reads.
	t.Setenv("X_LET", "v")
	prog := plantProgram(t, progEnv)
	if out := Build(prog, Keep("X_LET")).Run().Out(); !childHas(out, "X_LET") {
		t.Error(`Build(Keep("X_LET")) did not let X_LET through to the child, which the run drops`)
	}
	if out := Build(prog).Run().Out(); childHas(out, "X_LET") {
		t.Error("a plain Build let X_LET through, and the run drops it")
	}
	if out := Build(prog, Drop("PATH")).Run().Out(); childHas(out, "PATH") {
		t.Error(`Build(Drop("PATH")) left PATH in the child's environment`)
	}
}

// TestInstallPlacesTheProgramUnderItsNameAndALookupOnPathFindsIt is the proof that Install
// copies a built program into a folder under the name given, with .exe on Windows, and a lookup by
// that name on a PATH of the folder alone, as a child's start by name makes, finds it there and runs it.
//
// What it costs when this fails: a test that hands its child a fake tool by PATH finds no program on
// Windows, or a program other than the one it built.
func TestInstallPlacesTheProgramUnderItsNameAndALookupOnPathFindsIt(t *testing.T) {
	// serial: t.Setenv puts the folder alone on this process's PATH, which exec.LookPath reads.
	dir := t.TempDir()
	path := Build(plantProgram(t, progStreams)).Install(t, dir, "fake-tool")
	if want := filepath.Join(dir, "fake-tool"+exeHere); path != want {
		t.Fatalf("Install gave %q, want %q", path, want)
	}
	t.Setenv("PATH", dir)
	found, err := exec.LookPath("fake-tool")
	if err != nil || found != path {
		t.Fatalf("a lookup of fake-tool on a PATH of %s found %q (%v), want %q", dir, found, err, path)
	}
	cmd := exec.Command(found) //nolint:gosec,noctx // G204: the program Build made and Install placed, which writes and exits at once
	var out strings.Builder
	cmd.Stdout = &out
	if err := cmd.Run(); err == nil || out.String() != "to out" {
		t.Errorf("the program found on PATH: %v, stdout %q, want exit 3 and %q", err, out.String(), "to out")
	}
}

// TestInstallHoldsTheForkLockWhileItsCopyIsOpen: install holds syscall.ForkLock while its copy is
// open for writing, both once the copy is open and once its bytes are written, so that no fork of
// this process starts in that time. TryLock, which a fork's own Lock would wait on,
// finds the lock taken at both points.
//
// What it costs when this fails: a child another goroutine forks while the copy is open holds it for
// writing until its own exec, and a start of the copy in that time fails `text file busy` on Linux.
func TestInstallHoldsTheForkLockWhileItsCopyIsOpen(t *testing.T) {
	// serial: a fork by a parallel test takes syscall.ForkLock too, and TryLock would find that
	// test's hold where it looks for install's.
	built := filepath.Join(t.TempDir(), "built")
	if err := os.WriteFile(built, []byte("program"), 0o600); err != nil {
		t.Fatal(err)
	}
	var held []bool
	path, err := install(built, t.TempDir(), "tool", func() {
		free := syscall.ForkLock.TryLock()
		if free {
			syscall.ForkLock.Unlock()
		}
		held = append(held, !free)
	})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if !slices.Equal(held, []bool{true, true}) {
		t.Errorf("syscall.ForkLock held once %s was open and once written: %v, want [true true]", path, held)
	}
}
