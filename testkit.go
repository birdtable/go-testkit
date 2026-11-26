package testkit

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/birdtable/go-testkit/internal/homevars"
)

// ownPrefix starts the name of the folder Isolate makes for the tests' own environment.
const ownPrefix = "testkit-"

// kept is the safe default of what the tests keep from the environment they were started in, which
// Keep adds to and Drop takes from: the path, the Go runtime's own knobs, what Windows needs to start
// a process, what Rerun hands its child (helperVar), which Helper reads only in a child's run, and
// the second account the host set up for the tests (OtherAccountVar), which OtherAccount reads.
func kept() []string {
	return []string{"PATH", "GORACE", "GOTRACEBACK", "GODEBUG", "GOCOVERDIR", "SYSTEMROOT", "COMSPEC", "PROGRAMDATA", helperVar, OtherAccountVar}
}

// homes is every variable that names a folder of the person's, pointed at a folder of the tests':
// homevars' list for this system, the one proc.HomeEnv gives a child. On Windows the go
// tool and os read the home, the cache and the configuration from USERPROFILE, LOCALAPPDATA and
// APPDATA, as Unix reads them from HOME and XDG_.
func homes() []string {
	return homevars.Folders(homevars.Here)
}

// goDirs is every folder of the go tool Isolate pins where the run's go had it, so a go a test
// starts from the tests' new home keeps its build cache and its modules.
func goDirs() []string {
	return []string{"GOCACHE", "GOMODCACHE", "GOPATH"}
}

// pinned is every setting Isolate gives the tests whatever they were started with, as NAME=value: no
// system configuration of git's, and no env file of go's, safe defaults of the isolation
// that no caller is asked to choose. So a go a test starts reads its settings from the environment,
// which holds what the run kept and nothing else, and from the toolchain's own go.env, which GOENV
// does not turn off. The go workspace is not pinned: GOWORK is the
// caller's, by Keep.
func pinned() []string {
	return []string{"GIT_CONFIG_NOSYSTEM=1", goEnvName + "=off"}
}

// offline is every go setting GoOffline gives the tests and their children, as NAME=value: no module
// proxy; no direct fetch, which GOPRIVATE or GONOPROXY naming a module would start though the proxy is
// off; no checksum server, which go asks directly when the proxy is off; and no toolchain download,
// which go starts with the proxy off.
func offline() []string {
	return []string{"GOPROXY=off", "GONOPROXY=none", "GOSUMDB=off", "GOTOOLCHAIN=local"}
}

// isOffline is whether the variable named k is one of offline's, matching names as this system does
// (foldNames), so on Windows goproxy is GOPROXY and elsewhere it is another variable, which go does
// not read.
func isOffline(k string) bool {
	for _, kv := range offline() {
		if name, _, _ := strings.Cut(kv, "="); name == k || foldNames && strings.EqualFold(name, k) {
			return true
		}
	}
	return false
}

// withOffline is set, what an Isolate set beneath its folder, with the settings of offline added when
// c was given GoOffline.
func withOffline(set map[string]string, c conf) map[string]string {
	if c.offline {
		for _, kv := range offline() {
			k, v, _ := strings.Cut(kv, "=")
			set[k] = v
		}
	}
	return set
}

// gitConfig is the variable that names git's global configuration file, which Isolate points into
// its folder.
const gitConfig = "GIT_CONFIG_GLOBAL"

// isolated is what the Isolate in force set, by name in upper case, the folder it made, and the conf
// it was given; set is nil when none is in force.
type isolated struct {
	set  map[string]string
	own  string
	conf conf
}

// inForce is the Isolate in force, read back from the environment it made, since the process's
// environment is the one state an Isolate leaves: HOME is the folder home beneath one named
// testkit-*, and each name Isolate set has the value it gave, and the go tool's folders as the
// process has them. None is in force when HOME is any other folder, a test's own among them.
func inForce() isolated {
	home := os.Getenv("HOME")
	own := filepath.Dir(home)
	if !filepath.IsAbs(home) || filepath.Base(home) != "home" || !strings.HasPrefix(filepath.Base(own), ownPrefix) {
		return isolated{}
	}
	set := withOffline(ownSet(own), active)
	for _, k := range goDirs() {
		if v := os.Getenv(k); filepath.IsAbs(v) {
			set[k] = v
		}
	}
	return isolated{set: set, own: own, conf: active}
}

// ownSet is what Isolate sets beneath the folder own, by name: each of homes, a folder named for it
// in lower case, as proc.HomeEnv gives it, with HOMEDRIVE and HOMEPATH on Windows naming
// USERPROFILE's; git's global configuration and the settings of pinned; the go tool's folders aside.
func ownSet(own string) map[string]string {
	set := map[string]string{gitConfig: filepath.Join(own, "gitconfig")}
	for _, kv := range homevars.Under(own) {
		k, v, _ := strings.Cut(kv, "=")
		set[k] = v
	}
	for _, kv := range pinned() {
		k, v, _ := strings.Cut(kv, "=")
		set[k] = v
	}
	return set
}

// An Option changes what Main and Isolate let through or drop beyond the fixed lists, and what Main
// runs before it isolates. The fixed kept and homes lists stay private; Keep and Drop extend the allow
// list a run was given, Before and RerunEnv are the hooks a containment plugs in by, and Options makes
// one Option of several.
type Option func(*conf)

// conf is the extra an Isolate was given over the fixed kept list: each a name or a prefix in upper
// case, so a name is matched however it is cased, as Windows names Path; and the hooks of Before and
// RerunEnv, in the order given.
type conf struct {
	keep []string // exact names that do not leak, though kept does not hold them
	drop []string // name prefixes that leak, though kept or a Keep would hold them

	before   []func(start []string) (code int, done bool, err error) // what Main runs before it isolates (Before)
	rerunEnv []func() []string                                       // what a Rerun's child is given beside Clean's (RerunEnv)

	offline bool // the settings of offline win over the run's, a Keep and a Drop (GoOffline)
	guard   bool // a NoNetwork is among the options, whose guard only Main's Before sets, so Isolate refuses it
}

// GoOffline makes every go a test starts run with no network: no module proxy, no direct
// fetch, no checksum server and no toolchain download. It holds while the isolation is in force: a
// test that moves HOME to a folder of its own has none in force, as Clean says, and Clean then gives
// a child none of the four.
//
// Under Main or Isolate it sets GOPROXY=off, GONOPROXY=none, GOSUMDB=off and GOTOOLCHAIN=local in the
// test process, and Clean gives a child those four and no other value of them, whatever the run held
// and whatever a Keep or a Drop says; restore puts the run's own values back. Every system takes the
// same four. It sets nothing else: GOWORK, GOFLAGS, GOPRIVATE and GOAUTH stay the caller's, by Keep.
// The module and build caches need nothing of it: Isolate pins GOMODCACHE and GOCACHE where the run's
// go had them, so a module the run's cache holds still builds. A go.sum line already there is still
// checked, and one missing is still go's error under its default -mod=readonly.
func GoOffline() Option {
	return func(c *conf) { c.offline = true }
}

// Before makes Main run f on the environment the process started in, before it isolates the process
// and before any test runs. Main runs each Before in the order given; the first whose done is true
// ends Main with its code and err, and then no test runs and no later Before is called. One whose done
// is false lets Main go on, whatever its code and err. Isolate alone runs none. A nil f adds nothing.
// A containment is a Before: its f reruns the test binary inside the containment and returns the
// rerun's code with done true, or done false in the rerun itself, which then runs the tests.
func Before(f func(start []string) (code int, done bool, err error)) Option {
	return func(c *conf) {
		if f != nil {
			c.before = append(c.before, f)
		}
	}
}

// RerunEnv makes Rerun, RerunVia and RerunInterrupted call f at each start of a child and give the
// child each NAME=value f returns, after the entries of Clean and before the one by which Helper
// reads its args, which no entry of f's replaces. An entry with no name before its = is dropped. An
// entry naming what the isolation sets, a home, git's global configuration, a pinned setting, a go
// folder or one of GoOffline's four, in any case, fails the Rerun with ErrRefused before any child
// starts, so f cannot undo the isolation for a child. It
// holds for as long as the isolation of the Main or Isolate given it is in force, so it reaches every
// Rerun of the tests of that run; a Rerun with no Main or Isolate in force gives no entry of any
// RerunEnv. f is called from the test that reruns, so one called from parallel tests is safe for
// concurrent use. A nil f adds nothing.
func RerunEnv(f func() []string) Option {
	return func(c *conf) {
		if f != nil {
			c.rerunEnv = append(c.rerunEnv, f)
		}
	}
}

// Options is one Option that applies each of opts in the order given, as Main applies the options it
// is given, so a package hands its callers several options as one.
func Options(opts ...Option) Option {
	return func(c *conf) {
		for _, o := range opts {
			o(c)
		}
	}
}

// Keep lets each named variable through to a test and its children, though the fixed list does not
// hold it. A name is exact, and a Keep of it wins over a Drop of a prefix it starts with.
func Keep(names ...string) Option {
	return func(c *conf) {
		for _, n := range names {
			c.keep = append(c.keep, strings.ToUpper(n))
		}
	}
}

// Drop makes every variable whose name starts with one of the prefixes leak, though the fixed list
// would hold it; a Keep of the exact name wins over it. It narrows the names a run reads through,
// the fixed list and a Keep; it does not drop a name Isolate itself set, such as TMPDIR, which Clean
// passes by the folder Isolate made, not by the leak rule.
func Drop(prefixes ...string) Option {
	return func(c *conf) {
		for _, p := range prefixes {
			c.drop = append(c.drop, strings.ToUpper(p))
		}
	}
}

// confOf is the conf the options build, in order.
func confOf(opts []Option) conf {
	var c conf
	for _, o := range opts {
		o(&c)
	}
	return c
}

// active is the conf of the Isolate in force, set by isolate and put back by its restore. An Isolate
// is serial (it clears the environment), so the write happens before the tests run and the read-back
// after, and Clean and Rerun read it from parallel tests with no write in flight.
var active conf //nolint:gochecknoglobals // the conf of the Isolate in force, which Clean and Rerun read; an Isolate is serial, so it is written before the tests and read after

// made is the folder the Isolate in force in this process made, set and put back with active; it is
// empty when this process made none. The environment alone cannot tell a folder this process made
// from one a parent run handed down, which a child that runs no Main of its own inherits; Isolated
// reads it so.
var made string //nolint:gochecknoglobals // the folder of the Isolate in force, which Isolated reads; written with active, before the tests and after them

// leaks is whether the run's value of the variable named name must never reach a test or a child of
// one under c: a name c.keep holds does not leak, a name a c.drop prefix starts does, and every other
// name but those of the fixed kept list leaks, in any case. A name Isolate sets leaks too, as the run
// had it; Clean passes it only with the value Isolate gave it.
func (c conf) leaks(name string) bool {
	u := strings.ToUpper(name)
	if slices.Contains(c.keep, u) {
		return false
	}
	for _, p := range c.drop {
		if strings.HasPrefix(u, p) {
			return true
		}
	}
	return !slices.Contains(kept(), u)
}

// Clean is the environment for a child process, from environ, which is most often the test
// process's own: each variable of environ that does not leak, and each one Isolate set, with the
// value Isolate gave it or, for a folder, a path beneath the folder Isolate made, such as a test's
// t.TempDir. Outside an Isolate, or once a test points HOME at another folder, every name of homes
// leaks, so a child Clean gives the person's environment to has no HOME at all, and the Keep and Drop
// of the Isolate no longer apply: Clean then drops by the fixed list alone, which keeps less, never
// more. An entry with no name is dropped.
func Clean(environ []string) []string {
	return inForce().clean(environ)
}

// clean is Clean under the isolation in: each variable of environ that passes, then, under a
// GoOffline, each setting of offline that environ did not give with its value, so a test that
// changed or unset one in its process still gives its child the setting and not go's default.
func (in isolated) clean(environ []string) []string {
	out := make([]string, 0, len(environ))
	given := map[string]bool{}
	for _, kv := range environ {
		if k, v, _ := strings.Cut(kv, "="); k != "" && in.passes(k, v) {
			out = append(out, kv)
			if isOffline(k) {
				given[strings.ToUpper(k)] = true
			}
		}
	}
	if in.set == nil || !in.conf.offline {
		return out
	}
	for _, kv := range offline() {
		if k, _, _ := strings.Cut(kv, "="); !given[k] {
			out = append(out, kv)
		}
	}
	return out
}

// passes is whether the variable k with the value v reaches a child under in. GOENV, in any case,
// passes only as the off Isolate gave it, whatever a Keep says, so no child's go reads an env file:
// not the person's, by a Keep of the run's GOENV, and not one a test wrote and pointed GOENV at.
// Under a GoOffline each setting of offline passes only with offline's value, as GOENV does.
func (in isolated) passes(k, v string) bool {
	want, ok := in.set[strings.ToUpper(k)]
	switch {
	case strings.EqualFold(k, goEnvName), in.conf.offline && isOffline(k):
		return ok && v == want
	case !in.conf.leaks(k):
		return true
	}
	return ok && (v == want || filepath.IsAbs(v) && beneath(v, in.own))
}

// beneath is whether the absolute path p is the folder dir or a path below it, as filepath reads
// both.
func beneath(p, dir string) bool {
	rel, err := filepath.Rel(dir, filepath.Clean(p))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// Main is the code a test binary's TestMain exits with. It isolates the process, runs the tests and
// restores it: an environment it cannot make is an error before any test runs, and one it cannot
// put back, or a folder it cannot remove, an error after, for TestMain to end on. The options Keep
// and Drop change the allow list for the run and its children. Before any of that it runs each
// Before, in order, on the environment the process started in, and the first that says done ends it
// with that Before's code and error, with no test run and nothing isolated. The folders it gives the
// tests are Isolate's, named as made and not resolved, as Isolate says.
func Main(m interface{ Run() int }, opts ...Option) (int, error) {
	return run(m, os.Environ(), opts)
}

// run is Main with the environment the process is taken to have started in.
func run(m interface{ Run() int }, start []string, opts []Option) (int, error) {
	for _, f := range confOf(opts).before {
		if code, done, err := f(start); done {
			return code, err
		}
	}
	restore, err := isolate(start, opts)
	if err != nil {
		return 1, fmt.Errorf("the tests could not be given an environment of their own, so none ran: %w", err)
	}
	code := m.Run()
	if err := restore(); err != nil {
		return code, fmt.Errorf("the tests ran, and their environment was not put back: %w", err)
	}
	return code, nil
}

// Isolate gives the test process an environment of its own: it clears the environment, sets again
// the variables of kept, points each of homes at a folder of its own beneath a new one, sets git's
// global configuration to a file in it and the settings of pinned, and pins each of goDirs where the
// run's go had it (the run's environment first, then go's own env file, then go's default). That is
// all Isolate reads of go's env file: GOENV is off (pinned), so a go a test starts reads no file of
// the person's, and its settings (GOPRIVATE, GOPROXY, GOFLAGS, GOAUTH among them) come from the
// environment, which holds a setting of the run's only where the app lists it in Keep,
// and from go.env in the root of the go on PATH, which go reads whatever GOENV says and where an
// image or a packager may put defaults; Isolate does not pin those names against it. A name the app
// neither sets nor keeps takes the toolchain's default, so a module missing from the cache is asked
// of the public proxy and checksum server by its path, unless the app passes GoOffline. A credential the person's env file or
// environment holds, in GOFLAGS, in a GOPROXY or GONOSUMDB URL, in a .netrc
// that GOPRIVATE would send go to, or behind GOAUTH, so never reaches a test unless the app keeps
// it, and a Keep of GOENV does not bring a file back. A test's go env -w writes go/env in the new
// folder's configuration folder, which no go reads under Isolate. On Windows the run's GOENV is
// found in any case, as Windows reads names. A file that is there and cannot be read fails Isolate.
// go's telemetry is off, a safe default, in the new folder's configuration
// folder, and in its home's .config where a child given HOME without XDG_CONFIG_HOME reads it, so a
// go a test starts opens no file there and starts no upload process, and restore can remove the
// folder, on Windows too and with no upload process writing into it. restore puts back the environment as it was and removes the
// folder, with a module cache a test's go filled beneath it, read-only as go leaves it, as go clean
// -modcache would; it is for after m.Run, and a second call does nothing. An Isolate inside another is undone by its own restore, and the outer one's
// stays in force. The options Keep and Drop change the allow list for the run and its children; GOWORK
// is on no fixed list and Isolate sets none, so it reaches a test only by a Keep. GOTOOLCHAIN is the
// caller's as GOWORK is: a go a test starts takes the run's only by a Keep, else go.env's, auto in a
// release, so a go older than the module's toolchain line switches to that toolchain, found in the
// pinned GOMODCACHE or downloaded; a child given HOME with no GOMODCACHE or GOPATH downloads it
// beneath Isolate's home. An app that wants no toolchain download, and no fetch of any
// kind, passes GoOffline, which pins GOTOOLCHAIN=local with the proxy, direct fetch and checksum
// server off. The new folder is os.MkdirTemp's in the run's temp folder, named as made
// and its links not resolved, and so is each folder set beneath it, t.TempDir and os.TempDir among
// them: on macOS a path under /var/folders, where a path read back by filepath.EvalSymlinks or
// os.Getwd is under /private/var/folders, as /var is a link. A test that compares the two resolves
// its own with filepath.EvalSymlinks first. Isolate runs no Before, so options that hold a NoNetwork
// are its error, and it changes nothing: the tests would otherwise run with the network open.
func Isolate(opts ...Option) (restore func() error, err error) {
	if confOf(opts).guard {
		return nil, errIsolateGuard
	}
	return isolate(os.Environ(), opts)
}

// errIsolateGuard is Isolate's refusal of options that hold a NoNetwork.
var errIsolateGuard = errors.New("NoNetwork is an option of Main: Isolate runs no Before, so it would set no guard and the tests would run with the network open")

// isolate is Isolate from start, the environment the process is taken to have started in, which
// in Isolate is its own. It sets active to the conf the options build only once the environment is
// made, so a failure leaves the conf of the Isolate that was in force, and restore puts that back.
func isolate(start []string, opts []Option) (func() error, error) {
	before, prev, prevMade := os.Environ(), active, made
	c := confOf(opts)
	dirs, err := goSettings(start)
	if err != nil {
		return nil, err
	}
	own, err := os.MkdirTemp("", ownPrefix)
	if err != nil {
		return nil, err
	}
	set := withOffline(ownSet(own), c)
	for _, k := range homes() {
		if err := os.Mkdir(set[k], 0o700); err != nil {
			return nil, errors.Join(err, os.RemoveAll(own))
		}
	}
	for i, k := range goDirs() {
		set[k] = dirs[i]
	}
	keep := keptOf(start, c)
	if c.guard {
		keep = append(withoutGuardVar(keep), guardRerunEnv()...) // the guard's marker, set once its probe passed
	}
	if err := replace(keep, set); err != nil {
		return nil, errors.Join(err, replace(before, nil), os.RemoveAll(own))
	}
	config, err := os.UserConfigDir()
	if err == nil {
		err = telemetryOff(config, own)
	}
	if bare := homeConfig(readsXDG, set["HOME"]); err == nil && bare != "" {
		err = telemetryOff(bare, own)
	}
	if err != nil {
		return nil, errors.Join(err, replace(before, nil), os.RemoveAll(own))
	}
	active, made = c, own
	var once sync.Once
	return func() error {
		var err error
		once.Do(func() {
			active, made = prev, prevMade
			err = errors.Join(replace(before, nil), removeOwn(own))
		})
		return err
	}, nil
}

// removeOwn removes Isolate's folder own as go clean -modcache removes a module cache: go leaves one
// it fills beneath own (by HOME, with no GOMODCACHE or GOPATH) read-only, so on Unix no file in it
// can be removed. A removal that fails makes each folder beneath own the owner's to write and
// removes again. The walk reads each entry as itself, never through a link, and the
// change of mode goes by an os.Root of own, so neither a link nor a junction takes it outside own.
func removeOwn(own string) error {
	if os.RemoveAll(own) == nil {
		return nil
	}
	root, err := os.OpenRoot(own)
	if err != nil {
		return err
	}
	walked := fs.WalkDir(root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			err = root.Chmod(p, 0o700)
		}
		return err
	})
	if err := errors.Join(root.Close(), os.RemoveAll(own)); err != nil {
		return errors.Join(err, walked)
	}
	return nil
}

// keptOf is each variable of environ that does not leak under c.
func keptOf(environ []string, c conf) []string {
	var out []string
	for _, kv := range environ {
		if k, _, _ := strings.Cut(kv, "="); k != "" && !c.leaks(k) {
			out = append(out, kv)
		}
	}
	return out
}

// replace clears the process's environment and sets each variable of environ and then each of set.
func replace(environ []string, set map[string]string) error {
	os.Clearenv()
	var errs []error
	for _, kv := range environ {
		if k, v, _ := strings.Cut(kv, "="); k != "" {
			errs = append(errs, os.Setenv(k, v))
		}
	}
	for k, v := range set {
		errs = append(errs, os.Setenv(k, v))
	}
	return errors.Join(errs...)
}

// goEnvName is the variable that names go's env file, which Isolate pins off.
const goEnvName = "GOENV"

// goSettings is the value of each of goDirs, in its order, where the run's go reads it: start's own
// value, else the one go's env file holds (goEnvFile), else go's default. A value that is not an
// absolute path is no folder, and is read past.
func goSettings(start []string) ([]string, error) {
	file, err := goEnvFile(start)
	if err != nil {
		return nil, err
	}
	pick := func(k string) string {
		for _, v := range []string{lookup(start, k), file[k]} {
			if filepath.IsAbs(v) {
				return v
			}
		}
		return ""
	}
	gopath := pick("GOPATH")
	if gopath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("go's default GOPATH is in the home folder: %w", err)
		}
		gopath = filepath.Join(home, "go")
	}
	mod := pick("GOMODCACHE")
	if mod == "" {
		mod = filepath.Join(filepath.SplitList(gopath)[0], "pkg", "mod")
	}
	cache := pick("GOCACHE")
	if cache == "" {
		dir, err := os.UserCacheDir()
		if err != nil {
			return nil, fmt.Errorf("go's default GOCACHE is in the cache folder: %w", err)
		}
		cache = filepath.Join(dir, "go-build")
	}
	return []string{cache, mod, gopath}, nil
}

// goEnvFile is the settings of go's env file as the run's go would read it, by name: the file GOENV
// names in start, or go/env in the person's configuration folder; none when GOENV is off, there is no
// such folder, or the file is not there, as go reads none then either. A file that is there and
// cannot be read is an error. goSettings reads it for goDirs alone; no other setting of it reaches a
// test.
func goEnvFile(start []string) (map[string]string, error) {
	name := lookup(start, goEnvName)
	if name == "" {
		if dir, err := os.UserConfigDir(); err == nil {
			name = filepath.Join(dir, "go", "env")
		}
	}
	out := map[string]string{}
	if name == "" || name == "off" {
		return out, nil
	}
	b, err := os.ReadFile(filepath.Clean(name))
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, fmt.Errorf("go's env file: %w", err)
	}
	lines := bufio.NewScanner(bytes.NewReader(b))
	for lines.Scan() {
		if k, v, ok := strings.Cut(strings.TrimSpace(lines.Text()), "="); ok {
			out[k] = v
		}
	}
	return out, lines.Err()
}

// homeConfig is the configuration folder os.UserConfigDir gives a process with the home home and
// no XDG_CONFIG_HOME, where that is not the folder XDG_CONFIG_HOME names: home's .config where
// the system reads XDG_CONFIG_HOME (xdg, readsXDG for this one), "" where it does not. A child
// given HOME without XDG_CONFIG_HOME, as proc.Bare gives one, reads go's telemetry mode there, so Isolate turns it off there too. Where it was not, a go a test
// started read no mode, took telemetry as local, and started its upload process, which runs on
// past the go and wrote into the folder as restore removed it, as a linux-amd64 run did.
func homeConfig(xdg bool, home string) string {
	if !xdg {
		return ""
	}
	return filepath.Join(home, ".config")
}

// telemetryOff turns go's telemetry off in config, the configuration folder the process has under
// Isolate, by the mode file go reads there (go/telemetry/mode). A go a test starts then opens no
// counter file in Isolate's folder and starts no upload process, which on Windows would hold a file
// there open past the go and keep restore from removing the folder. config is refused unless it is
// absolute and beneath own, so the person's own telemetry setting is never written.
func telemetryOff(config, own string) error {
	if !filepath.IsAbs(config) || !beneath(config, own) {
		return fmt.Errorf("go's configuration folder %q is not beneath the tests' folder, so go's telemetry was not turned off", config)
	}
	dir := filepath.Join(config, "go", "telemetry")
	err := os.MkdirAll(dir, 0o700)
	if err == nil {
		err = os.WriteFile(filepath.Join(dir, "mode"), []byte("off"), 0o600)
	}
	if err != nil {
		return fmt.Errorf("go's telemetry could not be turned off: %w", err)
	}
	return nil
}

// lookup is the last value environ gives the variable named k, which is the one a process reads,
// matching names as this system does (foldNames).
func lookup(environ []string, k string) string {
	return lookupIn(environ, k, foldNames)
}

// lookupIn is lookup with names matched in any case when fold is set, as Windows reads them, and
// exactly when it is not.
func lookupIn(environ []string, k string, fold bool) string {
	v := ""
	for _, kv := range environ {
		if name, value, _ := strings.Cut(kv, "="); name == k || fold && strings.EqualFold(name, k) {
			v = value
		}
	}
	return v
}
