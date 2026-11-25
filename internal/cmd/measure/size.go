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
const Ceiling = 5234

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
