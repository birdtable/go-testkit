package testkit

import (
	"cmp"
	"errors"
	"fmt"
	"go/token"
	"io"
	"maps"
	"os"
	"os/exec" //nolint:depguard // testkit imports none of birdtable's libraries, so not the proc runner: a built program is started here, with its own deadline
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"unicode"
)

// A Binary is a program under test. Build and BuildAt return one; Path, Run and RunInput compile it on first
// use, once for each package however many Binary values or tests ask for it. The build lives under
// the test's own temp folder, so Isolate's restore removes it with the rest. The one build is for the
// test binary's life under Main's Isolate: a package first built inside a nested Isolate has its
// binary removed by that Isolate's restore, so Build it under the outer one, not a nested one.
// X and Trimpath give a Binary built with those flags, compiled once for each package and flag set.
// Dir and Env give a Binary that runs the same build in another folder or with more variables.
type Binary struct {
	key        buildKey
	opts       []Option
	refusedPkg error             // why Build refused its package or an option; kept by every copy, and such a Binary never runs go
	vars       map[string]string // each -X: the variable X named and the value it set
	refused    error             // why X refused a name or a value; kept by every copy, and such a Binary never runs go
	dir        string            // the folder Dir gave its runs; empty is the test process's own
	env        []string          // each NAME=value Env added, laid over the clean environment in order
	badEnv     error             // why Env refused an entry; kept by every copy, and such a Binary runs nothing
}

// Build makes a Binary for the package pkg, an import path or a folder as `go build` takes one. A
// relative path is read from the module the test runs in, so a package names it the same from any
// test folder. opts change the environment a run of the binary sees, over the test's own. A pkg that
// starts with -, which go build would read as a flag, is refused as a refused X is, and
// so is every copy X and Trimpath make of it. So are opts that hold an option only Main acts on,
// NoNetwork, Before or RerunEnv, which a run of the binary would ignore; the refusal names it.
func Build(pkg string, opts ...Option) *Binary {
	if strings.HasPrefix(pkg, "-") {
		return &Binary{key: buildKey{pkg: pkg}, opts: opts, refusedPkg: fmt.Errorf(
			"testkit: Build refuses a package that starts with -, which go build would read as a flag: %q", pkg)}
	}
	if err := mainOnly("Build", opts); err != nil {
		return &Binary{key: buildKey{pkg: pkg}, opts: opts, refusedPkg: fmt.Errorf("testkit: %w", err)}
	}
	return &Binary{key: buildKey{pkg: resolve(pkg)}, opts: opts}
}

// X is a copy of b built with the linker flag -X name=value, which sets the string variable name to
// value in the program, as in X("main.version", "1.2.3"). name is what the linker reads: an import
// path, a dot and the variable. value is any string, empty or holding spaces and = included. Each X
// adds one -X, and a second X of the same name replaces the first; X writes the word go build hands
// the linker, so nothing parses a flag string. A name of any other form is refused, and so is a value
// with white space and both quotes, which no -ldflags word can hold: the Binary never runs go, Path
// is empty, Run gives code -1 with the reason on Stderr, and every copy made from it is refused too.
// b itself is unchanged.
func (b *Binary) X(name, value string) *Binary {
	c := b.clone()
	if c.refused != nil {
		return c
	}
	if c.vars == nil {
		c.vars = map[string]string{}
	}
	c.vars[name] = value
	c.key.ldflags, c.refused = linkerWords(c.vars)
	return c
}

// Trimpath is a copy of b built with -trimpath, so the binary holds no file system path of the
// build. b itself is unchanged.
func (b *Binary) Trimpath() *Binary {
	c := b.clone()
	c.key.trimpath = true
	return c
}

// Dir is a copy of b whose runs, by Run, RunInput and Start, start in the folder dir, read as exec
// reads a command's folder; an empty dir is the test process's own folder, as a plain Build runs in.
// Dir sets no PWD: a run gets the clean environment's PWD, if it holds one, unchanged, so a program
// that reads PWD for its folder takes Env("PWD=" + dir) as well. It is per copy: b itself, and every
// other copy, is unchanged, and the build is the same one.
func (b *Binary) Dir(dir string) *Binary {
	c := b.clone()
	c.dir = dir
	return c
}

// Env is a copy of b whose runs get each NAME=value of kv after the clean environment Run gives, so
// a name in kv overrides its clean value, the last of a name winning as os/exec keeps it (with no
// regard to case on Windows), while Keep and Drop still decide what reaches a run from the test
// process. Env is the caller's own way past the pins: a name Isolate pins, GOENV or HOME, given by
// Env reaches the run with Env's value although the clean environment would drop that value from
// the test process's. A second Env adds to the first. An entry with no name, such as =v or Windows'
// =C:=C:\, is dropped, as Clean drops one, so Env(os.Environ()...) works there. An entry without =
// is refused: the copy and every copy made from it run nothing, Run giving code -1 with the reason
// on Stderr and Start an ErrRefused. Path is unchanged by it. b itself is unchanged, and the build
// is the same one.
func (b *Binary) Env(kv ...string) *Binary {
	c := b.clone()
	for _, e := range kv {
		name, _, ok := strings.Cut(e, "=")
		switch {
		case !ok:
			if c.badEnv == nil {
				c.badEnv = fmt.Errorf("%w: Env of %s refuses %q, which holds no =, so it is no NAME=value", ErrRefused, b.key.pkg, e)
			}
		case name != "":
			c.env = append(c.env, e)
		}
	}
	return c
}

// clone is a copy of b with an options slice, a variables map and an Env list of its own.
func (b *Binary) clone() *Binary {
	c := *b
	c.opts = slices.Clone(b.opts)
	c.vars = maps.Clone(b.vars)
	c.env = slices.Clone(b.env)
	return &c
}

// runnable is the path of b's build for a run, or why it runs nothing: Env's refusal, which builds
// nothing, or resolve's.
func (b *Binary) runnable() (string, error) {
	if b.badEnv != nil {
		return "", b.badEnv
	}
	return b.resolve()
}

// place gives c, a command that runs b's build, Dir's folder and the clean environment with Env's
// variables after it.
func (b *Binary) place(c *exec.Cmd) {
	c.Dir, c.Env = b.dir, append(b.environ(), b.env...)
}

// linkerWords is the -ldflags value that sets each of vars, two words for each in name order, -X and
// then name=value, so the same variables are one build however they were added, or why there is
// none. go build trims the whole value of each rune unicode.IsSpace matches, and then splits it on
// space, tab, carriage return and newline, a word that starts with ' or " running to the next of the
// same quote, so a name=value that holds any such rune is quoted with a quote it does not hold, and a
// last word ending in one, a \v or a U+00A0 say, keeps it. go build refuses an -ldflags value that starts with a quote (it reads one
// that does not start with - as a package pattern), so the value always starts with the unquoted -X,
// and only a name=value word is ever quoted. That word starts with the name, so with neither a quote,
// a - nor the @ the linker reads as a file of flags.
func linkerWords(vars map[string]string) (string, error) {
	words := make([]string, 0, 2*len(vars))
	for _, name := range slices.Sorted(maps.Keys(vars)) {
		if err := varName(name); err != nil {
			return "", err
		}
		w := name + "=" + vars[name]
		if strings.ContainsFunc(w, unicode.IsSpace) {
			q := "'"
			if strings.Contains(w, q) {
				q = `"`
			}
			if strings.Contains(w, q) {
				return "", fmt.Errorf("testkit: X refuses the value for %s, which holds white space (a rune unicode.IsSpace matches) and both ' and \", so no -ldflags word holds it", name)
			}
			w = q + w + q
		}
		words = append(words, "-X", w)
	}
	return strings.Join(words, " "), nil
}

// pathBytes are the bytes varName takes in an import path.
const pathBytes = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-._~+/"

// varName is nil when name is a variable as -X names one, an import path, a dot and a Go identifier,
// as in main.version or example.com/m/v2/build.Version, and why not otherwise. The path holds only
// pathBytes and starts with none of - / or ., so neither = nor white space reaches the word.
func varName(name string) error {
	dot := strings.LastIndexByte(name, '.')
	if dot < 1 || !token.IsIdentifier(name[dot+1:]) || strings.Trim(name[:dot], pathBytes) != "" || strings.ContainsAny(name[:1], "-/.") {
		return fmt.Errorf("testkit: X refuses %q, which is not an import path, a dot and a Go identifier, the variable -X sets", name)
	}
	return nil
}

// A buildKey is what one compilation is: a package, the tree it is read from and the flags it is built
// with, as a comparable struct, so no package and flag pair can stand for another. at is the zero
// source for a Build, which reads today's tree.
type buildKey struct {
	pkg      string
	ldflags  string
	trimpath bool
	at       source
}

// builds holds one compilation for each package and flag set, so every Binary and test that names
// them shares the one build; buildsMu guards the map, and each built's own once guards its
// compilation.
var (
	buildsMu sync.Mutex              //nolint:gochecknoglobals // the one build cache the test binary shares, so a package is compiled once
	builds   = map[buildKey]*built{} //nolint:gochecknoglobals // the one build cache the test binary shares, so a package is compiled once
)

// A built is one compilation of a package with its flags.
type built struct {
	key  buildKey
	once sync.Once
	path string
	err  error
}

// buildOf is the one built for key, made on first ask.
func buildOf(key buildKey) *built {
	buildsMu.Lock()
	defer buildsMu.Unlock()
	b := builds[key]
	if b == nil {
		b = &built{key: key}
		builds[key] = b
	}
	return b
}

// resolve compiles the package once and is the path and the error of that one compilation.
func (bt *built) resolve() (string, error) {
	bt.once.Do(func() { bt.path, bt.err = compile(bt.key) })
	return bt.path, bt.err
}

// resolve is the path of b's build, compiled on first use, or why there is none: Build's or X's
// refusal, which never reaches go, or the build's failure.
func (b *Binary) resolve() (string, error) {
	if err := cmp.Or(b.refusedPkg, b.refused); err != nil {
		return "", err
	}
	return buildOf(b.key).resolve()
}

// Path is where the binary is, compiled on first use. It is empty when the build failed or Build or
// X refused, and Run then returns that failure as its Result. It is in a folder os.MkdirTemp made in
// the temp folder, as made and not resolved: on macOS under /var, which filepath.EvalSymlinks reads
// as /private/var, so a test that compares it with a path the binary reports, as from os.Executable,
// resolves it first.
func (b *Binary) Path() string {
	path, err := b.resolve()
	if err != nil {
		return ""
	}
	return path
}

// Install copies the program b built into folder under name, with .exe added on Windows, and is the
// copy's path, for a test that puts folder on its child's PATH so the child starts the program by
// name. name is one file name, without the .exe: a name that is empty, . or .., holds a
// / a \ or a :, or is not local to folder as filepath.IsLocal reads it (on Windows, a name such as NUL
// or COM1) is refused. So is a name already in folder: Install replaces nothing. A refusal, a build
// that failed and a copy that failed each fail t. On Unix the copy is made with mode 0o755, less the
// umask, as go build makes its own; on Windows it runs by its .exe.
func (b *Binary) Install(t testing.TB, folder, name string) string {
	t.Helper()
	built, err := b.resolve()
	if err != nil {
		t.Fatalf("testkit: Install of %s: %v", b.key.pkg, err)
	}
	path, err := install(built, folder, name, func() {})
	if err != nil {
		t.Fatalf("testkit: Install of %s: %v", b.key.pkg, err)
	}
	return path
}

// install is Install with the built program's path given: the copy's path, or why there is none.
// It holds syscall.ForkLock for reading from the copy's open to its close, so no fork starts while
// the copy is open for writing: a child forked then holds it until its own exec, and a start of the
// copy meanwhile fails ETXTBSY on Linux (golang/go#22315). look runs once the copy is
// open and once it is written, for a test; Install's look does nothing.
func install(built, folder, name string, look func()) (string, error) {
	if name == "" || name == "." || strings.ContainsAny(name, `/\:`) || !filepath.IsLocal(name) || keptName(name) {
		return "", fmt.Errorf("refused the name %q: it is one file name in the folder, with no / \\ or :, and not . or .. or a name the system keeps", name)
	}
	path := filepath.Join(folder, name+exeSuffix)
	src, err := os.Open(built) //nolint:gosec // G304: the program this package built, at the path its build gave
	if err != nil {
		return "", err
	}
	syscall.ForkLock.RLock()
	dst, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o755) //nolint:gosec // G302, G304: a program the test runs, so others may run it; folder is the caller's, and O_EXCL replaces nothing in it
	if err != nil {
		syscall.ForkLock.RUnlock()
		return "", errors.Join(fmt.Errorf("placing the program: %w", err), src.Close())
	}
	look()
	_, err = io.Copy(dst, src)
	look()
	err = errors.Join(err, dst.Close())
	syscall.ForkLock.RUnlock()
	if err = errors.Join(err, src.Close()); err != nil {
		// O_EXCL made the file, so the half copy is this call's own to remove.
		return "", errors.Join(fmt.Errorf("copying the program to %s: %w", path, err), os.Remove(path))
	}
	return path, nil
}

// Result is what one run left behind: its exit code and what it wrote to each stream.
type Result struct {
	Code           int
	Stdout, Stderr string
}

// Out is stdout and stderr together, for an assertion that does not care which stream a line was on.
func (r Result) Out() string { return r.Stdout + r.Stderr }

// Run runs the binary with args, in the clean environment the test has with Env's variables after
// it, in Dir's folder, and returns its code and what it wrote. A build that failed, or Build's, X's
// or Env's refusal, is a Result with code -1 and the failure on Stderr.
func (b *Binary) Run(args ...string) Result {
	return b.RunInput("", args...)
}

// RunInput is Run with stdin handed to the binary, as a socket hands a per-request command its one
// line.
func (b *Binary) RunInput(stdin string, args ...string) Result {
	path, err := b.runnable()
	if err != nil {
		return Result{Code: -1, Stderr: err.Error()}
	}
	c := exec.Command(path, args...) //nolint:gosec,noctx // the binary this Binary built; a caller with a proc runner runs it through that runner for a deadline and a group kill, and a leaf runs it directly
	c.Stdin = strings.NewReader(stdin)
	b.place(c)
	var out, errb strings.Builder
	c.Stdout, c.Stderr = &out, &errb
	code := 0
	if err := c.Run(); err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			return Result{Code: -1, Stderr: "running " + b.key.pkg + ": " + err.Error()}
		}
		code = ee.ExitCode()
	}
	return Result{Code: code, Stdout: out.String(), Stderr: errb.String()}
}

// environ is the clean environment a run of the binary sees: the test's own, cleaned, with the
// Binary's own options laid over the Isolate's conf.
func (b *Binary) environ() []string {
	in := inForce()
	if len(b.opts) > 0 {
		c := conf{keep: slices.Clone(in.conf.keep), drop: slices.Clone(in.conf.drop)}
		for _, o := range b.opts {
			o(&c)
		}
		in.conf = c
	}
	return in.clean(os.Environ())
}

// compile builds key's package with key's flags into a folder of its own beneath the temp folder,
// which under an Isolate is the tests', removed by restore. make cover sets GOCOVERDIR, and then
// every run of the binary writes what it reached there, so a run counts towards coverage too. The
// -ldflags value is one argument, as linkerWords wrote it.
func compile(key buildKey) (string, error) {
	pkg := key.pkg
	dir, err := os.MkdirTemp("", "testkit-build-")
	if err != nil {
		return "", err
	}
	bin := filepath.Join(dir, "binary"+exeSuffix)
	args := []string{"build", "-o", bin}
	if os.Getenv("GOCOVERDIR") != "" {
		args = append(args, "-cover")
	}
	if key.trimpath {
		args = append(args, "-trimpath")
	}
	if key.ldflags != "" {
		args = append(args, "-ldflags="+key.ldflags)
	}
	// A folder is built from inside it, as the package ".", so a module of its own is read from its
	// own go.mod and a folder of the caller's module from the module above it. An import path is built
	// where the test runs, against that module. A BuildAt's tree is unpacked beside the binary and built
	// from its source's folder there, with GOWORK=off, so no go.work but its own go.mod decides.
	// The build runs in the clean environment the test has, not the test process's own, so a variable a
	// test set (GOFLAGS, CGO_ENABLED) does not reach the one shared binary. Under Isolate that is the
	// pinned GOCACHE, GOMODCACHE and GOPATH; outside one Clean drops them, so Build is for use under Main.
	target, where, env := pkg, "", Clean(os.Environ())
	if at := key.at; at.commit != "" {
		tree := filepath.Join(dir, "tree")
		if err := os.Mkdir(tree, 0o700); err != nil {
			return "", err
		}
		if err := unpack(at, tree); err != nil {
			return "", fmt.Errorf("building %s: %w", pkg, err)
		}
		target, where = at.target, filepath.Join(tree, filepath.FromSlash(at.dir))
		env = append(slices.DeleteFunc(env, func(kv string) bool {
			k, _, _ := strings.Cut(kv, "=")
			return k == "GOWORK" || foldNames && strings.EqualFold(k, "GOWORK")
		}), "GOWORK=off")
	} else if fi, err := os.Stat(pkg); err == nil && fi.IsDir() {
		target, where = ".", pkg
	}
	cmd := exec.Command("go", append(args, target)...) //nolint:gosec,noctx // the build is a bounded compile, and no caller context reaches compile through the fixed Build and Run signatures
	cmd.Dir, cmd.Env = where, env
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("building %s:\n%s", pkg, out)
	}
	return bin, nil
}

// resolve turns a module-relative path into one that works from any test package's folder: go test
// runs each package in its own folder, so a bare "cmd/app" would resolve differently by depth.
// Walking up to the go.mod is the only answer that does not encode each caller's depth. An absolute
// path and an import path (a dot in the first element) are taken as they are.
func resolve(pkg string) string {
	if pkg == "" || filepath.IsAbs(pkg) || importPath(pkg) {
		return pkg
	}
	if mod, ok := modRoot(); ok {
		return filepath.Join(mod, pkg)
	}
	return pkg
}

// importPath is whether pkg is an import path, not a folder: its first element holds a dot, and is
// neither . nor ..
func importPath(pkg string) bool {
	first, _, _ := strings.Cut(filepath.ToSlash(pkg), "/")
	return first != "." && first != ".." && strings.Contains(first, ".")
}

// modRoot is the folder of the go.mod nearest above the test's folder, the folder itself included,
// and whether there is one.
func modRoot() (string, bool) {
	dir, err := os.Getwd()
	if err != nil {
		return "", false
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}
