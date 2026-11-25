package testkit

import (
	"errors"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/birdtable/go-testkit/internal/homevars"
)

// childName is the variable that makes this test binary a child: it writes the environment it was
// handed, before Main changes it, to the file the variable names, and exits.
const childName = "TESTKIT_CHILD"

// wrapWord, as the first argument, makes this test binary the wrapper of RerunVia's test: it writes
// wrapLine, then runs the command line after the word with its own streams and environment, and
// exits with its code.
const wrapWord = "testkit-wrap"

// wrapLine is what the wrapper writes before it runs the child.
const wrapLine = "testkit: the wrapper runs the child"

// wrap is the wrapper: it runs argv and is its exit code, or 2 when it could not run it.
func wrap(argv []string) int {
	if _, err := os.Stdout.WriteString(wrapLine + "\n"); err != nil {
		return 2
	}
	cmd := exec.Command(argv[0], argv[1:]...) //nolint:gosec,noctx // the command line RerunVia hands its wrapper, whose own deadline ends this process
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	err := cmd.Run()
	if exit, ok := errors.AsType[*exec.ExitError](err); ok && exit.ExitCode() >= 0 {
		return exit.ExitCode()
	}
	if err != nil {
		return 2
	}
	return 0
}

// abandonWord, as the first argument and with a file path after it, makes this test binary a wrapper
// that leaves its child behind: it starts the command line after the path with its stdout and stderr
// in that file, and exits 0 at once, so the child outlives the parent that was to end it, as a child
// of a killed test binary does.
const abandonWord = "testkit-abandon"

// abandon is that wrapper: it starts argv with its output in the file at out, and is 0, or 2 when it
// could not start it.
func abandon(out string, argv []string) int {
	f, err := os.Create(out)
	if err != nil {
		return 2
	}
	// The child holds its own copy of the file, so this one is closed when the wrapper is done.
	defer f.Close()
	cmd := exec.Command(argv[0], argv[1:]...) //nolint:gosec,noctx // the command line RerunVia hands its wrapper; the child's own -test.timeout is what ends it
	cmd.Stdout, cmd.Stderr = f, f
	if cmd.Start() != nil {
		return 2
	}
	return 0
}

// TestMain starts the tests from the environment Main makes, unless the binary is a test's child.
func TestMain(m *testing.M) {
	if len(os.Args) > 2 && os.Args[1] == wrapWord {
		os.Exit(wrap(os.Args[2:]))
	}
	if len(os.Args) > 3 && os.Args[1] == abandonWord {
		os.Exit(abandon(os.Args[2], os.Args[3:]))
	}
	if len(os.Args) > 4 && os.Args[1] == guardWord {
		os.Exit(guardChild(os.Args[2], os.Args[3], os.Args[4]))
	}
	if len(os.Args) > 2 && os.Args[1] == dialWord {
		os.Exit(dialChild(os.Args[2]))
	}
	if out := os.Getenv(childName); out != "" {
		if err := os.WriteFile(out, []byte(strings.Join(os.Environ(), "\x00")), 0o600); err != nil {
			panic(err)
		}
		if exitHeld() {
			syscall.Exit(0)
		}
		os.Exit(0)
	}
	// testkit's own policy: a go a test starts reads no workspace. Isolate pins none, so it is set here and kept.
	if err := os.Setenv("GOWORK", "off"); err != nil {
		panic(err)
	}
	code, err := Main(m, Keep("GOWORK"))
	if err != nil {
		panic(err)
	}
	os.Exit(code)
}

// exitHeld is whether os.Exit would hold this child a second: one handed neither GORACE nor
// GOCOVERDIR, as the rows that Drop GO hand it. os.Exit's runtime hook runs the race detector's
// finish, which holds a process built with -race for atexit_sleep_ms, a second unless GORACE sets
// it to 0 as the tiers do. Such a child ends by syscall.Exit instead, past every exit hook: with no
// GOCOVERDIR no coverage hook has a folder to write to, and the race reports it finds are printed
// as they are found; what it gives up is exit code 66 for one found.
func exitHeld() bool {
	_, race := os.LookupEnv("GORACE")
	_, cover := os.LookupEnv("GOCOVERDIR")
	return !race && !cover
}

// child starts this test binary as a child with environ and childName after it, and is the
// environment the child was handed, by name, childName aside.
func child(t *testing.T, environ []string) map[string]string {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "environ")
	cmd := exec.CommandContext(t.Context(), self, "-test.run=^$") //nolint:gosec // the test binary itself, started as a child that writes the environment it was handed
	cmd.Env = append(slices.Clone(environ), childName+"="+out)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the child: %v\n%s", err, b)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	env := byName(strings.Split(string(b), "\x00"))
	delete(env, childName)
	return env
}

// abs is the slash-separated path p made absolute on the running OS, so a case of goSettings, which
// keeps a value only where filepath.IsAbs holds, reads the same on every system: on the POSIX systems
// a leading slash already roots it, and on Windows the volume of the temp folder, which is always
// absolute, roots it (a rooted path with no drive is not absolute there).
func abs(p string) string {
	return filepath.Join(filepath.VolumeName(os.TempDir())+string(filepath.Separator), filepath.FromSlash(p))
}

// byName is environ by name, the last value a name is given winning, as a process reads it.
func byName(environ []string) map[string]string {
	env := map[string]string{}
	for _, kv := range environ {
		if k, v, _ := strings.Cut(kv, "="); k != "" {
			env[k] = v
		}
	}
	return env
}

// leaks is what a run is planted with that no test or child of one may see: a hook's repository,
// a configuration of git's counted into the environment that names a hook folder, a token whose
// name starts with no GIT_, and a state folder given as a relative path.
func leaks() map[string]string {
	return map[string]string{
		"GIT_DIR":            "/planted/repository",
		"GIT_CONFIG_COUNT":   "1",
		"GIT_CONFIG_KEY_0":   "core.hooksPath",
		"GIT_CONFIG_VALUE_0": "/planted/hooks",
		"GITHUB_TOKEN":       "ghp_planted",
		"XDG_STATE_HOME":     "planted/state",
	}
}

// planted is the environment a run starts with: the test process's own, which Main made, with each
// of leaks set over it.
func planted() []string {
	env := os.Environ()
	for k, v := range leaks() {
		env = append(env, k+"="+v)
	}
	return env
}

// seen is each name of leaks that env gives the value it was planted with, sorted.
func seen(env map[string]string) []string {
	var out []string
	for k, v := range leaks() {
		if got, ok := env[k]; ok && got == v {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out
}

// denied is environ without each variable a deny list drops, as five projects clean a child's.
func denied(environ []string, drops func(string) bool) map[string]string {
	env := byName(environ)
	maps.DeleteFunc(env, func(k, _ string) bool { return drops(k) })
	return env
}

// firstDrops is one project's deny list, as a study of the projects read it.
func firstDrops(k string) bool {
	return strings.HasPrefix(k, "APP_") || k == "AGENT_SESSION_ID" || strings.HasPrefix(k, "GIT_")
}

// secondDrops is another project's deny list, as the same study read it.
func secondDrops(k string) bool {
	for _, p := range []string{"TOOL_", "GIT_", "XDG_"} {
		if strings.HasPrefix(k, p) {
			return true
		}
	}
	return slices.Contains([]string{"CI", "NO_COLOR", "CLICOLOR_FORCE", "COLUMNS", "LINES", "ACCESSIBLE", "TERM", "PAGER", "LESS"}, k)
}

// runner is a test binary's m for Main: Run is the tests.
type runner func() int

func (r runner) Run() int { return r() }

// TestAChildSeesNoneOfWhatTheRunWasPlantedWithThroughCleanOrMain: a run planted with GIT_DIR, a
// GIT_CONFIG_COUNT that names a hook folder, a token and a relative XDG_STATE_HOME starts a child of
// a test through Clean, and one from Main with the whole of the process's environment, and neither
// child sees any of them. The planted reds are two projects' deny lists, each red for the names
// it misses, and no list at all, red for every one.
//
// What it costs when this fails: a test's child writes to the repository a hook named, runs a hook
// program the run's git configuration counted in, or hands a token to whatever it runs.
func TestAChildSeesNoneOfWhatTheRunWasPlantedWithThroughCleanOrMain(t *testing.T) {
	// serial: run clears the process's environment, which every test reads; its restore puts it back.
	if got := seen(child(t, Clean(planted()))); len(got) != 0 {
		t.Errorf("a child through Clean sees %v", got)
	}
	var fromMain map[string]string
	code, err := run(runner(func() int {
		fromMain = child(t, os.Environ())
		return 0
	}), planted(), nil)
	if err != nil || code != 0 || len(fromMain) == 0 {
		t.Fatalf("run: code %d, %v, the child read %d variables", code, err, len(fromMain))
	}
	if got := seen(fromMain); len(got) != 0 {
		t.Errorf("a child of a test that started from Main sees %v", got)
	}
	ran := 0
	for name, tc := range map[string]struct {
		env  map[string]string
		want []string
	}{
		"one deny list":     {denied(planted(), firstDrops), []string{"GITHUB_TOKEN", "XDG_STATE_HOME"}},
		"another deny list": {denied(planted(), secondDrops), []string{"GITHUB_TOKEN"}},
		"no list":           {byName(planted()), slices.Sorted(maps.Keys(leaks()))},
		"Clean":             {byName(Clean(planted())), nil},
	} {
		ran++
		if got := seen(tc.env); !slices.Equal(got, tc.want) {
			t.Errorf("%s: a child sees %v, want %v", name, got, tc.want)
		}
	}
	if ran != 4 {
		t.Fatalf("ran %d planted lists, want 4", ran)
	}
}

// own is the folder the Isolate in force made.
func own(t *testing.T) string {
	t.Helper()
	in := inForce()
	if in.own == "" {
		t.Fatal("no Isolate is in force")
	}
	return in.own
}

// listed is every name Main may leave set: kept, homes and on Windows the halves of USERPROFILE,
// goDirs, the names of pinned, git's global configuration, and each name the Main in force was
// given to Keep.
func listed() []string {
	names := slices.Concat(kept(), homes(), homevars.Halves(homevars.Here), goDirs(), []string{gitConfig}, active.keep)
	for _, kv := range pinned() {
		k, _, _ := strings.Cut(kv, "=")
		names = append(names, k)
	}
	return names
}

// unlisted is each name of env that listed does not hold, in any case, sorted.
func unlisted(env map[string]string) []string {
	var out []string
	for k := range env {
		if !slices.Contains(listed(), strings.ToUpper(k)) {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out
}

// TestMainKeepsOnlyTheNamesItListsAndPointsTheFoldersIntoItsOwn: under Main the environment holds
// no name listed does not, each of homes is a folder of its own beneath the one Isolate made, a
// test's t.TempDir is beneath it too, git's configuration is a file there and the system's is off,
// the go tool's folders are absolute paths and its workspace is off, as this package's TestMain sets
// it and keeps it. The planted red is an
// environment with a name listed does not hold, which unlisted names.
//
// What it costs when this fails: a test reads or writes the person's own home, temp folder or
// settings, or runs with a variable of the run's, and passes or fails on what the machine holds.
func TestMainKeepsOnlyTheNamesItListsAndPointsTheFoldersIntoItsOwn(t *testing.T) {
	t.Parallel()
	env, dir := byName(os.Environ()), own(t)
	if bad := unlisted(env); len(bad) != 0 {
		t.Errorf("Main keeps %v, which it does not list", bad)
	}
	if bad := unlisted(map[string]string{"PATH": "/bin", "Path": "/bin", "LANG": "C", "GITHUB_TOKEN": "x"}); !slices.Equal(bad, []string{"GITHUB_TOKEN", "LANG"}) {
		t.Errorf("unlisted names %v of a planted environment, want GITHUB_TOKEN and LANG", bad)
	}
	homed := 0
	for _, k := range homes() {
		homed++
		want := filepath.Join(dir, strings.ToLower(k))
		if fi, err := os.Stat(want); env[k] != want || err != nil || !fi.IsDir() {
			t.Errorf("%s is %q (%v), want the folder %s", k, env[k], err, want)
		}
	}
	for _, kv := range homevars.Under(dir) {
		if k, v, _ := strings.Cut(kv, "="); env[k] != v {
			t.Errorf("%s is %q, want %q, as proc.HomeEnv gives it", k, env[k], v)
		}
	}
	if homed != len(homes()) || homed == 0 {
		t.Fatalf("%d of %d homes read", homed, len(homes()))
	}
	if !strings.HasPrefix(filepath.Base(dir), ownPrefix) {
		t.Errorf("the folder is %s, want one named %s*", dir, ownPrefix)
	}
	if tmp := t.TempDir(); !beneath(tmp, dir) {
		t.Errorf("t.TempDir is %s, want a folder beneath %s", tmp, dir)
	}
	if env[gitConfig] != filepath.Join(dir, "gitconfig") || env["GIT_CONFIG_NOSYSTEM"] != "1" || env["GOWORK"] != "off" {
		t.Errorf("git's configuration %q, the system's %q, the go workspace %q", env[gitConfig], env["GIT_CONFIG_NOSYSTEM"], env["GOWORK"])
	}
	pinnedDirs := 0
	for _, k := range goDirs() {
		pinnedDirs++
		if !filepath.IsAbs(env[k]) || beneath(env[k], dir) {
			t.Errorf("%s is %q, want the run's own folder, absolute and outside the tests'", k, env[k])
		}
	}
	if pinnedDirs != len(goDirs()) || pinnedDirs == 0 {
		t.Fatalf("%d of %d go folders read", pinnedDirs, len(goDirs()))
	}
}

// TestRunPutsTheEnvironmentBackAndRemovesItsFolderAndKeepsTheGoFolders: inside run the go tool's
// folders are the run's own, the tests' code is what run returns, and after it the environment is
// what it was, the folder it made is gone and the Isolate before it is in force again. A second
// restore changes nothing, and a nested Isolate's leaves the outer one's.
//
// What it costs when this fails: a test binary leaves a folder in the temp folder each run, runs its
// later tests in an environment a test changed, or builds every package again with no cache.
func TestRunPutsTheEnvironmentBackAndRemovesItsFolderAndKeepsTheGoFolders(t *testing.T) {
	// serial: run clears the process's environment, which every test reads; its restore puts it back.
	before, outer := byName(os.Environ()), own(t)
	start := append(planted(), "GOCACHE="+abs("planted/cache"), "GOMODCACHE="+abs("planted/mod"), "GOPATH="+abs("planted/go"))
	var inner string
	var inside map[string]string
	code, err := run(runner(func() int {
		inner, inside = own(t), byName(os.Environ())
		return 3
	}), start, nil)
	if code != 3 || err != nil {
		t.Errorf("run: code %d, %v, want 3, what the tests returned", code, err)
	}
	if inside["GOCACHE"] != abs("planted/cache") || inside["GOMODCACHE"] != abs("planted/mod") || inside["GOPATH"] != abs("planted/go") {
		t.Errorf("inside run the go folders are %q, %q, %q, want the run's", inside["GOCACHE"], inside["GOMODCACHE"], inside["GOPATH"])
	}
	if inner == outer || inside["HOME"] != filepath.Join(inner, "home") {
		t.Errorf("inside run the folder is %s and HOME %s, want a folder other than %s", inner, inside["HOME"], outer)
	}
	if _, err := os.Stat(inner); !os.IsNotExist(err) {
		t.Errorf("the folder %s is still there after run: %v", inner, err)
	}
	if after := byName(os.Environ()); !maps.Equal(after, before) {
		t.Errorf("after run the environment is\n%v\nwant\n%v", after, before)
	}
	if got := own(t); got != outer {
		t.Errorf("after run the Isolate in force made %s, want %s", got, outer)
	}
	restore, err := Isolate()
	if err != nil {
		t.Fatal(err)
	}
	if err := restore(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TESTKIT_AFTER", "1")
	if err := restore(); err != nil || os.Getenv("TESTKIT_AFTER") != "1" || own(t) != outer {
		t.Errorf("a second restore: %v, and it put the environment back again or another Isolate in force", err)
	}
}

// TestRestoreRemovesAModuleCacheGoLeftReadOnly: a module cache a test's go filled beneath Isolate's
// home, its folders and files read-only as go leaves them, is removed by restore with the folder,
// as go clean -modcache would remove it; and a symbolic link or, on Windows, a directory junction in
// it to a read-only folder outside leaves that folder's mode as it was. The planted red is restore's
// removal without the change of mode, which on Unix fails on the cache's first file, as an earlier
// run did. The folder outside is made read-only by Chmod, since Windows' Mkdir takes no mode, and
// is held read-only before the run, so the row cannot pass on a folder that never was.
//
// What it costs when this fails: a test binary whose go downloaded a toolchain or a module under
// Isolate's home ends in an error after every test passed, and go-proc's own TestMains panic; or
// restore makes a folder outside its own writable through a link.
func TestRestoreRemovesAModuleCacheGoLeftReadOnly(t *testing.T) {
	// serial: run clears the process's environment, which every test reads; its restore puts it back.
	type plantCase struct {
		name string
		link func(target, link string) error
	}
	cases := []plantCase{{"the cache alone", nil}, {"a link out of it", os.Symlink}}
	if junctions { // a junction is Windows' alone, and a tier fails on a skip, so the row is only there
		cases = append(cases, plantCase{"a junction out of it", nil})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "a junction out of it" {
				tc.link = func(target, link string) error { return junction(t.Context(), target, link) }
			}
			if tc.name == "a link out of it" {
				Links(t)
			}
			outside := ""
			if tc.link != nil {
				outside = filepath.Join(t.TempDir(), "outside")
				if err := os.Mkdir(outside, 0o700); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(outside, 0o700) }) // so t.TempDir's removal can take it on Windows
				if err := os.Chmod(outside, 0o555); err != nil {
					t.Fatal(err)
				}
				fi, err := os.Stat(outside)
				if err != nil {
					t.Fatal(err)
				}
				if mode := fi.Mode().Perm(); mode != 0o555 {
					t.Fatalf("the folder outside has mode %v before the run, want 0555", mode)
				}
			}
			var inner string
			var planted error
			code, err := run(runner(func() int {
				inner = own(t)
				planted = plantModCache(os.Getenv("HOME"), outside, tc.link)
				return 0
			}), os.Environ(), nil)
			if planted != nil {
				t.Fatalf("the module cache could not be planted: %v", planted)
			}
			if code != 0 || err != nil {
				t.Errorf("run: code %d, %v, want 0 and no error", code, err)
			}
			if _, err := os.Stat(inner); !os.IsNotExist(err) {
				t.Errorf("the folder %s is still there after run: %v", inner, err)
			}
			if tc.link == nil {
				return
			}
			fi, err := os.Stat(outside)
			if err != nil {
				t.Fatalf("the folder the link names, after run: %v", err)
			}
			if mode := fi.Mode().Perm(); mode != 0o555 {
				t.Errorf("the folder the link names has mode %v after run, want 0555 as it was", mode)
			}
		})
	}
}

// plantModCache fills a module cache beneath home as go leaves one: a toolchain's folder holding a
// file and a folder with a file, every one read-only, and with link not nil a link to outside,
// planted by link beside the toolchain. It is every error.
func plantModCache(home, outside string, link func(target, link string) error) error {
	mod := filepath.Join(home, "go", "pkg", "mod", "golang.org")
	bin := filepath.Join(mod, "toolchain@v0.0.1-go1.27.4.darwin-arm64", "bin")
	errs := []error{os.MkdirAll(bin, 0o700), os.WriteFile(filepath.Join(bin, "go"), nil, 0o444), os.WriteFile(filepath.Join(filepath.Dir(bin), "LICENSE"), nil, 0o444)}
	if link != nil {
		errs = append(errs, link(outside, filepath.Join(mod, "outside")))
	}
	for dir := bin; dir != filepath.Join(home, "go"); dir = filepath.Dir(dir) {
		errs = append(errs, os.Chmod(dir, 0o555))
	}
	return errors.Join(errs...)
}

// TestAGoUnderIsolateReadsNoEnvFile: under Isolate, the process's GOENV is off, and a go started
// from Clean(os.Environ()) reads no env file, so not the GOPRIVATE the run's file holds, wherever the run
// had it: the file GOENV names, go/env in the configuration folder, the run's own GOENV of off in
// either case, a GOENV that names no file, none at all, and a run whose GOENV the app keeps. A test's
// go env -w writes Isolate's configuration folder, never the run's file, and no go reads what it
// wrote. In every
// case go reads its telemetry as off and leaves no counter folder. Each case runs go env GOENV
// GOPRIVATE GOTELEMETRY, and the configuration folder is the test's own on every system.
//
// What it costs when this fails: a test's go reads the person's go settings, and with them a
// credential the app never kept, or on Windows a go's telemetry holds a file open in Isolate's folder
// and restore fails.
func TestAGoUnderIsolateReadsNoEnvFile(t *testing.T) {
	// serial: run clears the process's environment, and each case points the configuration folder.
	ran := 0
	for _, tc := range []struct {
		name  string
		from  string   // where the run's file is: "GOENV" names one, "config" is go/env in the configuration folder, "missing" a GOENV that names none, "" none at all
		start []string // what the run sets beside startWithout and the GOENV of from
		opts  []Option
		write bool // the test runs go env -w GOPRIVATE first
	}{
		{name: "the file GOENV names", from: "GOENV"},
		{name: "the file in the configuration folder", from: "config"},
		{name: "the file GOENV names, kept", from: "GOENV", opts: []Option{Keep(goEnvName)}},
		{name: "GOENV off", from: "config", start: []string{"GOENV=off"}},
		{name: "goenv off in another case", from: "config", start: []string{"goenv=off"}},
		{name: "no file in the configuration folder"},
		{name: "a GOENV that names no file", from: "missing"},
		{name: "a test's go env -w", from: "GOENV", write: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ran++
			configHere(t, tc.from == "config")
			start := startWithout()
			var file string
			switch tc.from {
			case "GOENV":
				file = filepath.Join(t.TempDir(), "env")
				if err := os.WriteFile(file, []byte(sourceEnv), 0o600); err != nil {
					t.Fatal(err)
				}
				start = append(start, goEnvName+"="+file)
			case "missing":
				start = append(start, goEnvName+"="+filepath.Join(t.TempDir(), "none"))
			}
			start = append(start, tc.start...)
			var goenv, private, telemetry, set, counters string
			var saw, wrote error
			code, err := run(runner(func() int {
				set, _ = os.LookupEnv(goEnvName)
				if tc.write {
					cmd := exec.CommandContext(t.Context(), "go", "env", "-w", "GOPRIVATE=written.example")
					cmd.Env = Clean(os.Environ())
					wrote = cmd.Run()
				}
				goenv, private, telemetry, saw = goSees(t.Context())
				counters = counterFolder()
				return 0
			}), start, tc.opts)
			if code != 0 || err != nil || saw != nil || wrote != nil {
				t.Fatalf("run: %d, %v; go: %v; go env -w: %v", code, err, saw, wrote)
			}
			// go env GOENV prints nothing where GOENV is off.
			if seen := [3]string{set, goenv, private}; seen != [3]string{"off", "", ""} {
				t.Errorf("GOENV is %q, go read GOENV %q and GOPRIVATE %q, want off, no file and none", set, goenv, private)
			}
			if b, err := os.ReadFile(file); file != "" && (err != nil || string(b) != sourceEnv) {
				t.Errorf("the run's env file holds %q, %v, want it as the run wrote it", b, err)
			}
			if seen := [2]string{telemetry, counters}; seen != [2]string{"off", ""} {
				t.Errorf("go env GOTELEMETRY printed %q, and go left %q, want off and no counter folder", telemetry, counters)
			}
		})
	}
	if ran != 8 {
		t.Fatalf("ran %d cases, want 8", ran)
	}
}

// credentialRoutes are the routes by which a credential reaches a go, each as the
// settings, NAME=value, that carry the credential secret, and the file a route reads, by name, with
// what it holds: GOFLAGS, userinfo in GOPROXY or GONOSUMDB, a .netrc that GOPRIVATE sends go to
// (NETRC names it, as HOME's is Isolate's), and GOAUTH, whose git form names a folder of
// credentials.
var credentialRoutes = []struct { //nolint:gochecknoglobals // a test table, read only
	route    string
	settings func(secret, dir string) []string
}{
	{"GOFLAGS", func(secret, _ string) []string { return []string{"GOFLAGS=-ldflags=-X=main.token=" + secret} }},
	{"userinfo in GOPROXY", func(secret, _ string) []string {
		return []string{"GOPROXY=https://" + userinfo("u", secret) + "proxy.example/"}
	}},
	{"userinfo in GONOSUMDB", func(secret, _ string) []string { return []string{"GONOSUMDB=" + userinfo("u", secret) + "sum.example"} }},
	{"netrc reached through GOPRIVATE", func(secret, dir string) []string {
		return []string{"GOPRIVATE=" + secret + ".private.example", "NETRC=" + filepath.Join(dir, secret+".netrc")}
	}},
	{"GOAUTH", func(secret, dir string) []string { return []string{"GOAUTH=git " + filepath.Join(dir, secret)} }},
}

// TestNoCredentialReachesAGoUnderIsolateUnlessKept: for each route of credentialRoutes, a run that
// holds a credential on it both in go's env file, which its GOENV names, and in its environment
// hands neither to a test: under an Isolate that keeps GOENV, go env -json of a go started from
// Clean(os.Environ()), and Clean(os.Environ()) itself, hold neither secret. Where the app keeps the route's
// names too, the environment's secret reaches the test, the app's choice, and the env
// file's never does. The .netrc file holds the secret as a password for the GOPRIVATE host. The
// planted red is GOENV=off dropped from pinned and from passes, which hands the run's GOENV, kept,
// to go, and every row reads the env file's secret.
//
// What it costs when this fails: every child of every test, the program under test among them, is
// handed the person's proxy password, a credential folder or command, or a linker flag that holds a
// secret, through go's env file or the run's environment, which the app never listed.
func TestNoCredentialReachesAGoUnderIsolateUnlessKept(t *testing.T) {
	// serial: run clears the process's environment, and the configuration folder is the test's.
	configHere(t, false)
	ran := 0
	for _, r := range credentialRoutes {
		dir := t.TempDir()
		fromFile, fromEnv := r.settings("fileSecret7", dir), r.settings("envSecret7", dir)
		file := filepath.Join(dir, "env")
		if err := os.WriteFile(file, []byte(strings.Join(fromFile, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		netrc := "machine envSecret7.private.example login u password envSecret7\n"
		if err := os.WriteFile(filepath.Join(dir, "envSecret7.netrc"), []byte(netrc), 0o600); err != nil {
			t.Fatal(err)
		}
		names := []string{goEnvName}
		for _, kv := range fromEnv {
			k, _, _ := strings.Cut(kv, "=")
			names = append(names, k)
		}
		for _, keep := range [][]string{{goEnvName}, names} {
			ran++
			var seen string
			var saw error
			code, err := run(runner(func() int {
				cmd := exec.CommandContext(t.Context(), "go", "env", "-json")
				cmd.Env = Clean(os.Environ())
				b, err := cmd.Output()
				seen, saw = string(b)+strings.Join(cmd.Env, "\n"), err
				return 0
			}), append(append(startWithout(), goEnvName+"="+file), fromEnv...), []Option{Keep(keep...)})
			if code != 0 || err != nil || saw != nil {
				t.Fatalf("%s, keeping %q: run %d, %v; go env -json: %v", r.route, keep, code, err, saw)
			}
			kept := len(keep) > 1
			if strings.Contains(seen, "fileSecret7") || strings.Contains(seen, "envSecret7") != kept {
				t.Errorf("%s, keeping %q: the env file's secret reached the test: %v, the environment's: %v; want %v and %v",
					r.route, keep, strings.Contains(seen, "fileSecret7"), strings.Contains(seen, "envSecret7"), false, kept)
			}
		}
	}
	if ran != 2*len(credentialRoutes) || len(credentialRoutes) != 5 {
		t.Fatalf("ran %d cases of %d routes, want 10 of 5", ran, len(credentialRoutes))
	}
}

// TestAGoGivenHOMEWithoutXDGConfigHomeReadsTelemetryOff: under Isolate, a go started with the
// process's environment but for XDG_CONFIG_HOME, as a child given HOME and not XDG_CONFIG_HOME is
// (proc.Bare gives PATH, LANG and HOME), reads go's telemetry off, and so starts no upload process.
// On Linux its configuration folder is then HOME's .config, not the one Isolate's environment names.
//
// What it costs when this fails: such a go takes telemetry as local and starts its upload process,
// which runs on past the go and writes into Isolate's folder while restore removes it, and the test
// binary's TestMain panics on a folder not empty, now and then, as a linux-amd64 run did.
func TestAGoGivenHOMEWithoutXDGConfigHomeReadsTelemetryOff(t *testing.T) {
	// serial: run clears the process's environment, and the configuration folder is the test's.
	configHere(t, false)
	var telemetry string
	var saw error
	code, err := run(runner(func() int {
		var env []string
		for _, kv := range Clean(os.Environ()) {
			if k, _, _ := strings.Cut(kv, "="); !strings.EqualFold(k, "XDG_CONFIG_HOME") {
				env = append(env, kv)
			}
		}
		cmd := exec.CommandContext(t.Context(), "go", "env", "GOTELEMETRY")
		cmd.Env = env
		b, err := cmd.Output()
		telemetry, saw = strings.TrimSpace(string(b)), err
		return 0
	}), startWithout(), nil)
	if code != 0 || err != nil || saw != nil {
		t.Fatalf("run: %d, %v; go env GOTELEMETRY: %v", code, err, saw)
	}
	if telemetry != "off" {
		t.Errorf("a go given HOME and no XDG_CONFIG_HOME printed GOTELEMETRY %q, want off", telemetry)
	}
}

// TestHomeConfigIsWhereAChildWithHOMEAloneReadsOnlyWhereXDGWouldDiffer: homeConfig is HOME's
// .config where the system reads XDG_CONFIG_HOME (Linux and the BSDs), and none where it does not
// (Windows, macOS, iOS and Plan 9, whose folder does not follow XDG_CONFIG_HOME). readsXDG is the
// one the build tags chose, and it agrees with os.UserConfigDir on this system.
//
// What it costs when this fails: Isolate writes a mode file where no go reads it, or leaves out the
// one a go given HOME alone reads.
func TestHomeConfigIsWhereAChildWithHOMEAloneReadsOnlyWhereXDGWouldDiffer(t *testing.T) {
	// serial: it sets the process's HOME and XDG_CONFIG_HOME.
	home := abs("tmp/testkit-1/home")
	for xdg, want := range map[bool]string{true: filepath.Join(home, ".config"), false: ""} {
		if got := homeConfig(xdg, home); got != want {
			t.Errorf("homeConfig(%v) = %q, want %q", xdg, got, want)
		}
	}
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", "")
	got, err := os.UserConfigDir()
	if err != nil && readsXDG {
		t.Fatalf("os.UserConfigDir with HOME %s: %v", dir, err)
	}
	if readsXDG != (got == filepath.Join(dir, ".config")) {
		t.Errorf("readsXDG is %v, and os.UserConfigDir with HOME %s alone is %q", readsXDG, dir, got)
	}
}

// TestAnEnvFileThatCannotBeReadFailsIsolate:a GOENV that names a folder, a file that is there and
// cannot be read, fails run before any test, and leaves no folder of Isolate's behind.
//
// What it costs when this fails: the tests run with go's settings silently gone.
func TestAnEnvFileThatCannotBeReadFailsIsolate(t *testing.T) {
	// serial: run clears the process's environment.
	configHere(t, false)
	pattern := filepath.Join(os.TempDir(), ownPrefix+"*")
	before, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatal(err)
	}
	ran := false
	code, err := run(runner(func() int { ran = true; return 0 }), append(startWithout(), goEnvName+"="+t.TempDir()), nil)
	if err == nil || code != 1 || ran {
		t.Errorf("run with GOENV a folder: %d, %v, and the tests ran: %v; want it refused before them", code, err, ran)
	}
	if after, err := filepath.Glob(pattern); err != nil || !slices.Equal(after, before) {
		t.Errorf("Isolate's folders in %s were %q and are %q, %v; want none left behind", os.TempDir(), before, after, err)
	}
}

// TestAGoWorkOrToolchainTheRunHadReachesAChildOnlyWhenKept: Isolate pins no go workspace and no
// toolchain. A run started with a GOWORK or a GOTOOLCHAIN hands it to a test's child when the run
// keeps it, by Keep alone or by a Keep that wins over a Drop of GO, and drops it, as any name on no
// list, when it does not. The planted red is GOWORK=off or GOTOOLCHAIN=local pinned again, which the
// kept rows read in place of the run's.
//
// What it costs when this fails: a module whose tests build in a workspace of its own, or that keeps
// GOTOOLCHAIN=local so its tests' go downloads no toolchain, cannot keep it, as Isolate forces its own
// choice over the caller's; or a workspace or a toolchain reaches the tests
// of a module that never asked for it.
func TestAGoWorkOrToolchainTheRunHadReachesAChildOnlyWhenKept(t *testing.T) {
	// serial: run clears the process's environment, which every test reads; its restore puts it back.
	ran := 0
	for name, value := range map[string]string{"GOWORK": abs("planted/go.work"), "GOTOOLCHAIN": "go1.27.4+path"} {
		start := append(slices.DeleteFunc(planted(), func(kv string) bool { return strings.HasPrefix(strings.ToUpper(kv), name+"=") }), name+"="+value)
		for row, tc := range map[string]struct {
			opts []Option
			want string
		}{
			"kept":                 {[]Option{Keep(name)}, value},
			"kept, GO dropped":     {[]Option{Drop("GO"), Keep(name)}, value},
			"not kept":             {nil, ""},
			"not kept, GO dropped": {[]Option{Drop("GO")}, ""},
		} {
			ran++
			var got map[string]string
			code, err := run(runner(func() int {
				got = child(t, Clean(os.Environ()))
				return 0
			}), start, tc.opts)
			if err != nil || code != 0 || len(got) == 0 {
				t.Fatalf("%s, %s: run: code %d, %v, the child read %d variables", name, row, code, err, len(got))
			}
			if v, ok := got[name]; v != tc.want || ok != (tc.want != "") {
				t.Errorf("%s, %s: the child's %s is %q (set %v), want %q", name, row, name, v, ok, tc.want)
			}
		}
	}
	if ran != 8 {
		t.Fatalf("ran %d rows, want 8", ran)
	}
}
