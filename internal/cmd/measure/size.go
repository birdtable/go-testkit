package main

import (
	"bytes"
	"errors"
	"io/fs"
	"path"
	"slices"
	"strings"
)

// Ceiling is the most non-test Go lines testkit's modules may hold together, as Size counts them:
// 4261, the measured count at the head of the pull request that adds this command (main ca267fc, the
// root 3321 and contain 940). A change past it raises it by its measured rise, with the reason here, as go-proc's
// Ceiling is raised.
//
// Raised by 881 to 5142, by measure, for the taskfile module and cmd/apigate: taskfile, a module of its own, is 485
// (taskfile/taskfile.go), and cmd/apigate, go-proc's gate/api.sh in Go, adds 396 to the root's
// count (cmd/apigate/main.go). contain is 940, as before.
//
// Raised by 27 to 5169, by measure, for the build on aix, illumos and solaris: hold.go adds 16 for NoHold and the skip, and hold_nofifo.go, HoldRead's
// error on those three systems, is 11. The rename to go-testkit moves no line.
//
// Raised by 8 to 5177, by measure, for the marks check: cmd/apigate's marks check refuses a listed
// name whose doc says Experimental, as go-console's shell gate did, and its doc says so.
//
// Raised by 33 to 5210, by measure, for Interrupted, the context a child of
// RerunInterrupted takes its interrupt by, with the handler installed before the ready line, and its
// doc, which says that order is the promise and why (rerun.go).
//
// Raised by 12 to 5222, by measure, for doc lines alone: Isolate, Main, HoldRead,
// Binary.Path and ShortDir each say their folder is named as made, not resolved, so on macOS it is
// under /var and not /private/var, and that a test comparing paths resolves with EvalSymlinks.
//
// Raised by 12 to 5234, by measure, for doc lines alone: contain, taskfile and cmd/apigate each say
// what they are built on, what they add and what the caller configures (14 lines), and two comments
// lose a line each where an issue citation came out.
//
// Raised by 211 to 5445, by measure, for BuildAt, which builds a program at an earlier commit for an
// upgrade test: buildat.go is 183, the ref and package refusals, git rev-parse and git archive
// unpacked through an os.Root, and its doc; binary.go adds 28, compile building a BuildAt's tree from
// its folder with GOWORK=off, and modRoot and importPath, which resolve and BuildAt share.
//
// Raised by 528 to 5973, by measure, for NoNetwork, the network guard: nonetwork.go is the option, its
// probe and its doc of what it does and does not promise (128); nonetwork_darwin.go is the sandbox_init
// call by name with no cgo, and its profile (93); nonetwork_linux.go is the two-step rerun into a user
// and network namespace, loopback brought up, the ready pipe and the passing on of signals (272);
// nonetwork_windows.go and nonetwork_other.go are the refusal elsewhere (17 each); and doc.go's
// paragraph names it (1). The .s trampolines are not Go lines.
//
// Raised by 19 to 5992, by measure, for NoNetwork's Go resolver on Linux: nonetwork_linux.go's
// withGoResolver appends netdns=go to the rerun's GODEBUG, keeping the caller's settings (17), and
// nonetwork.go's doc says so and narrows the limit to programs that are not Go (2).
//
// Raised by 136 to 6128, by measure, for BuildAt reading the old tree raw, from the second review
// of v0.4.0: buildat.go is 319, up from 183. git archive and its tar reader give way to git ls-tree
// -r -z, its parser, and git cat-file --batch, streamed through an os.Root, so no filter or attribute
// of the repository applies; every git call is given GIT_NO_LAZY_FETCH=1, and a tree a partial clone
// lacks is refused, saying so, and only a missing object is said so; and the doc says what the tree
// holds and which git the promise needs.
//
// Raised by 9 to 6137, by measure, for Isolate's refusal of NoNetwork: conf's guard flag, which NoNetwork
// sets, and Isolate's check, its error and its doc (testkit.go, 9); NoNetwork's doc keeps its lines.
//
// Raised by 4 to 6141, by measure, for a guard step's error ending the run: noNetwork takes its step as
// a parameter, ends the run on any error with a code of 1 or more, and its doc says so (nonetwork.go).
//
// Raised by 9 to 6150, by measure, for the guard's interface check on Linux: wayOut counts only the
// interfaces that are up and names the first way out, a run in another namespace that fails it refuses
// rather than nest again, and extraInterfaces is the add-only seam its tests use (nonetwork_linux.go).
//
// Raised by 9 to 6159, by measure, for the guard's marker in the tests' environment: NoNetwork keeps
// guardVar and isolate puts the guard's own value of it there once the tests checked in, so a fuzz worker
// or a helper child does not nest (testkit.go, nonetwork.go); guardVar's doc, moved to nonetwork.go to be
// read on every system, and NoNetwork's say so.
//
// Raised by 15 to 6174, by measure, for NoNetwork beside contain as uid 0: NoNetwork's doc says what a
// uid 0 caller's tests hold and that NoNetwork goes first (4), and namespaceRefused, the refusal's
// wording, says so to a uid 0 caller refused with no permission (nonetwork_linux.go, 11).
//
// Raised by 7 to 6181, by measure, for tests that cannot read their network namespace on Linux: the run
// refuses, naming the link and its error, rather than nest again, and runNetLink is the seam its test
// names a link of no namespace by (nonetwork_linux.go).
//
// Raised by 12 to 6193, by measure, for the uid 0 refusal's two causes: namespaceRefused says to put
// NoNetwork first only inside contain's containment, read by containedVar, and names a container's
// seccomp profile outside it; runGuarded takes the caller's uid, which the namespace's root is not; and
// the docs say guardedFrom is set before the probe, and a child probes again (nonetwork_linux.go, nonetwork.go).
//
// Raised by 365 to 6558, by measure, for RequireMain, the check that every package's TestMain calls
// Main: requiremain.go is 363, go list -e -json for darwin, linux and windows with GOARCH=amd64 and cgo
// off, the TestMain and its call read with go/parser, the opt-out line, the file and build line that
// leave a TestMain out, a package a pattern names that a system builds nothing of passed, go's errors
// with the import they name, and its doc; and doc.go's two lines name it (2).
const Ceiling = 6558

// Count is one module's size: its folder from the repository's top, "." for the root module, and
// its lines.
type Count struct {
	Module string
	Lines  int
}

// errNoModule is the refusal of a tree with no go.mod at its top.
var errNoModule = errors.New("measure: the repository's top holds no go.mod, so it is not a module")

// Size counts the non-test Go lines of each module in fsys the way `wc -l` of their files does, one
// per newline. A module is a folder holding a go.mod, the top first. A file counts to the nearest
// module above it, and only if it is a .go file not named _test.go, outside its module's internal/,
// testdata and any folder whose name starts with a dot or an underscore. The counts are the root's
// first and then the others by folder.
func Size(fsys fs.FS) ([]Count, error) {
	if _, err := fs.Stat(fsys, "go.mod"); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, errNoModule
		}
		return nil, err
	}
	lines := map[string]int{".": 0}
	err := fs.WalkDir(fsys, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return dir(fsys, name, lines)
		}
		if !source(name) {
			return nil
		}
		b, err := fs.ReadFile(fsys, name)
		if err != nil {
			return err
		}
		lines[module(path.Dir(name), lines)] += bytes.Count(b, []byte("\n"))
		return nil
	})
	if err != nil {
		return nil, err
	}
	counts := make([]Count, 0, len(lines))
	for m, n := range lines {
		counts = append(counts, Count{Module: m, Lines: n})
	}
	slices.SortFunc(counts, func(a, b Count) int { return strings.Compare(a.Module, b.Module) })
	return counts, nil
}

// dir enters the folder name, or skips it where it holds no package of a module's surface, and adds
// it to lines where it holds a go.mod of its own.
func dir(fsys fs.FS, name string, lines map[string]int) error {
	if name == "." {
		return nil
	}
	base := path.Base(name)
	if base == "testdata" || strings.HasPrefix(base, ".") || strings.HasPrefix(base, "_") {
		return fs.SkipDir
	}
	if _, err := fs.Stat(fsys, name+"/go.mod"); err == nil {
		lines[name] = 0
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if m := module(path.Dir(name), lines); name == path.Join(m, "internal") {
		return fs.SkipDir
	}
	return nil
}

// module is the module the folder name is in: the nearest folder at or above it that lines holds.
func module(name string, lines map[string]int) string {
	for {
		if _, ok := lines[name]; ok || name == "." {
			return name
		}
		name = path.Dir(name)
	}
}

// source says whether the file name is a Go file a build of the package reads.
func source(name string) bool {
	base := path.Base(name)
	return strings.HasSuffix(base, ".go") && !strings.HasSuffix(base, "_test.go") &&
		!strings.HasPrefix(base, ".") && !strings.HasPrefix(base, "_")
}
