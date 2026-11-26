package testkit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec" //nolint:depguard // RequireMain runs go list, which reads the module and builds nothing
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// mainPath is the import path of this package, whose Main a TestMain must call, and mainName the
// name a file that imports it with no name of its own calls it by.
const (
	mainPath = "github.com/birdtable/go-testkit"
	mainName = "testkit"
)

// unisolated is the comment that takes a package out of RequireMain's check, followed by the reason.
const unisolated = "//testkit:unisolated"

// mainSystems are the systems RequireMain lists the packages for.
func mainSystems() []string {
	return []string{"darwin", "linux", "windows"}
}

// RequireMain fails t for each package of the module the test runs in that has tests and whose
// TestMain does not call Main, so no package's tests run in the person's environment because its
// TestMain was left out. Put one test that calls it at the module's root:
//
//	func TestEveryPackageCallsMain(t *testing.T) {
//		testkit.RequireMain(t)
//	}
//
// patterns are go list's, read in the module's root folder; with none it is ./..., every package of
// the module. It lists them with go list -e -json once for each of darwin, linux and windows, by GOOS,
// which builds nothing, in the clean environment and with GOWORK=off, so the module's own go.mod
// decides what it reads. Under GoOffline go list reads only the module cache, and a module the cache
// lacks fails t with go's error, at the import that needs it, which the failure names.
//
// A package that has test files for a system needs, among those files, in its own test package or its
// external one, a TestMain whose body calls this package's Main directly, under any name its file
// imports it by, or unqualified in this package's own tests or under a dot import. The check reads the
// call and does not follow it: a TestMain that calls a function of the package's own that calls Main
// does not count, and neither does a Main of another package imported under the name testkit.
//
// Each failure names the package, the systems and the reason: it has no TestMain; its TestMain does
// not call Main; or its only TestMain is in a file that system leaves out, which the failure names
// with its //go:build line, or says its file name leaves it out. So a TestMain in main_linux_test.go
// alone fails for darwin and windows.
//
// A package opts out with a line //testkit:unisolated followed by the reason, in any of its _test.go
// files built for that system. A line with no reason fails. Each package that opts out is logged on
// every run, with its systems and its reason.
//
// RequireMain fails t, and never skips it: where no Isolate is in force, where a pattern is empty or
// starts with -, which go list would read as a flag, where the test's folder has no go.mod above it,
// and where go list fails or reports an error for a package it lists.
func RequireMain(t testing.TB, patterns ...string) {
	t.Helper()
	if inForce().set == nil {
		t.Fatalf("testkit: RequireMain: no Isolate is in force, and go list would read the person's settings")
	}
	if len(patterns) == 0 {
		patterns = []string{"./..."}
	}
	for _, p := range patterns {
		if p == "" || strings.HasPrefix(p, "-") {
			t.Fatalf("testkit: RequireMain: refused the pattern %q, which is empty or starts with -, so go list would read it as a flag", p)
		}
	}
	root, ok := modRoot()
	if !ok {
		t.Fatalf("testkit: RequireMain: the test's folder has no go.mod above it")
	}
	var found findings
	for _, goos := range mainSystems() {
		pkgs, err := listFor(t.Context(), root, goos, patterns)
		if err != nil {
			t.Fatalf("testkit: RequireMain: go list for %s: %v", goos, err)
		}
		for _, p := range pkgs {
			for _, v := range checkMain(root, p) {
				found.add(p.ImportPath, goos, v)
			}
		}
	}
	for _, f := range found {
		if f.optOut {
			t.Logf("testkit: RequireMain: %s is unisolated on %s: %s", f.pkg, strings.Join(f.systems, ", "), f.text)
		} else {
			t.Errorf("testkit: RequireMain: %s on %s: %s", f.pkg, strings.Join(f.systems, ", "), f.text)
		}
	}
}

// A listedPkg is what RequireMain reads of a package go list prints.
type listedPkg struct {
	ImportPath, Dir                           string
	TestGoFiles, XTestGoFiles, IgnoredGoFiles []string
	Error                                     *listErr
	DepsErrors                                []*listErr
}

// A listErr is an error go list gives for a package: Err, at Pos, a file's path from the folder go
// list ran in and its line and column, or "".
type listErr struct{ Pos, Err string }

// goErr is e, of a package go list ran in root for, as go gives it, at its position, with the import
// path of the import there when the position is one: go names only the package that imports it. The
// position is cut at its last two colons, as a path on Windows may hold one.
func goErr(root string, e *listErr) string {
	if e.Pos == "" {
		return e.Err
	}
	at := e.Pos + ": " + e.Err
	rest, col, ok := strings.CutLast(e.Pos, ":")
	name, line, ok2 := strings.CutLast(rest, ":")
	if !ok || !ok2 {
		return at
	}
	if !filepath.IsAbs(name) {
		name = filepath.Join(root, name)
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
	if err != nil {
		return at
	}
	for _, imp := range f.Imports {
		for _, pos := range []token.Pos{imp.Pos(), imp.Path.Pos()} {
			if p := fset.Position(pos); strconv.Itoa(p.Line) == line && strconv.Itoa(p.Column) == col {
				return e.Pos + ": " + imp.Path.Value + ": " + e.Err
			}
		}
	}
	return at
}

// listFor is what go list -e -json prints for patterns, run in the folder root for the system goos,
// in the clean environment with GOWORK=off.
func listFor(ctx context.Context, root, goos string, patterns []string) ([]listedPkg, error) {
	cmd := exec.CommandContext(ctx, "go", append([]string{"list", "-e", "-json"}, patterns...)...) //nolint:gosec // G204: go, with the caller's patterns, none of which starts with -
	cmd.Dir, cmd.Env = root, append(Clean(os.Environ()), "GOOS="+goos, "GOWORK=off")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%w\n%s", err, strings.TrimSpace(stderr.String()))
	}
	var pkgs []listedPkg
	for dec := json.NewDecoder(&stdout); ; {
		var p listedPkg
		if err := dec.Decode(&p); errors.Is(err, io.EOF) {
			return pkgs, nil
		} else if err != nil {
			return nil, fmt.Errorf("reading its output: %w", err)
		}
		pkgs = append(pkgs, p)
	}
}

// A verdict is one thing checkMain finds of a package on a system: a failure, or with optOut its
// reason for opting out.
type verdict struct {
	text   string
	optOut bool
}

// checkMain is what RequireMain finds of the package p, as go list run in root listed it for one
// system: nothing where its TestMain calls Main or it has no tests, else each failure, or its opt-out.
func checkMain(root string, p listedPkg) []verdict {
	var out []verdict
	for _, e := range append([]*listErr{p.Error}, p.DepsErrors...) {
		if e != nil {
			out = append(out, verdict{text: goErr(root, e)})
		}
	}
	built := append(slices.Clone(p.TestGoFiles), p.XTestGoFiles...)
	if len(out) != 0 || len(built) == 0 {
		return out
	}
	self := p.ImportPath == mainPath
	fset := token.NewFileSet()
	var reason, mainIn string
	calls := false
	for _, name := range built {
		f, err := parser.ParseFile(fset, filepath.Join(p.Dir, name), nil, parser.ParseComments)
		if err != nil {
			out = append(out, verdict{text: err.Error()})
			continue
		}
		r, has := optOut(f)
		if has && r == "" {
			out = append(out, verdict{text: fmt.Sprintf("the %s line in %s gives no reason", unisolated, name)})
		}
		if reason == "" {
			reason = r
		}
		if fd := testMain(f); fd != nil && mainIn == "" {
			mainIn, calls = name, callsMain(f, fd, self && slices.Contains(p.TestGoFiles, name))
		}
	}
	switch {
	case len(out) != 0:
		return out
	case reason != "":
		return []verdict{{text: reason, optOut: true}}
	case mainIn != "" && calls:
		return nil
	case mainIn != "":
		return []verdict{{text: fmt.Sprintf("its TestMain, in %s, does not call %s.Main itself, and a call through another function does not count", mainIn, mainName)}}
	}
	if left := leftOut(fset, p); len(left) != 0 {
		return []verdict{{text: "no TestMain is built for it there: " + strings.Join(left, "; ")}}
	}
	return []verdict{{text: "it has tests and no TestMain"}}
}

// leftOut is, for each test file of p that the system leaves out and that holds a TestMain, the file and
// what leaves it out: its //go:build line, or its name.
func leftOut(fset *token.FileSet, p listedPkg) []string {
	var out []string
	for _, name := range p.IgnoredGoFiles {
		if !strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(p.Dir, name), nil, parser.ParseComments)
		if err != nil || testMain(f) == nil {
			continue
		}
		by := "its file name"
		if line := buildLine(f); line != "" {
			by = line
		}
		out = append(out, fmt.Sprintf("the one in %s is left out by %s", name, by))
	}
	return out
}

// buildLine is the //go:build line of f, above its package clause, or "".
func buildLine(f *ast.File) string {
	for _, g := range f.Comments {
		if g.Pos() >= f.Package {
			break
		}
		for _, c := range g.List {
			if strings.HasPrefix(c.Text, "//go:build ") {
				return c.Text
			}
		}
	}
	return ""
}

// optOut is the reason of the first //testkit:unisolated line of f, trimmed, and whether f has one.
func optOut(f *ast.File) (string, bool) {
	for _, g := range f.Comments {
		for _, c := range g.List {
			rest, ok := strings.CutPrefix(c.Text, unisolated)
			if ok && (rest == "" || rest[0] == ' ' || rest[0] == '\t') {
				return strings.TrimSpace(rest), true
			}
		}
	}
	return "", false
}

// testMain is the func TestMain of f, with no receiver, or nil.
func testMain(f *ast.File) *ast.FuncDecl {
	for _, d := range f.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv == nil && fd.Name.Name == "TestMain" && fd.Body != nil {
			return fd
		}
	}
	return nil
}

// callsMain is whether the body of fd, of the file f, calls this package's Main: through a name f
// imports it by, or unqualified where bare, which is this package's own test package, or under a dot
// import.
func callsMain(f *ast.File, fd *ast.FuncDecl, bare bool) bool {
	names := map[string]bool{}
	for _, imp := range f.Imports {
		if path, err := strconv.Unquote(imp.Path.Value); err != nil || path != mainPath {
			continue
		}
		switch {
		case imp.Name == nil:
			names[mainName] = true
		case imp.Name.Name == ".":
			bare = true
		case imp.Name.Name != "_":
			names[imp.Name.Name] = true
		}
	}
	calls := false
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return !calls
		}
		switch fun := call.Fun.(type) {
		case *ast.Ident:
			calls = calls || bare && fun.Name == "Main"
		case *ast.SelectorExpr:
			x, ok := fun.X.(*ast.Ident)
			calls = calls || ok && names[x.Name] && fun.Sel.Name == "Main"
		}
		return !calls
	})
	return calls
}

// findings are what RequireMain found, in the order found, one for each package and text, with every
// system it was found on.
type findings []*finding

// A finding is one failure, or with optOut one opt-out, of the package pkg on systems.
type finding struct {
	pkg, text string
	optOut    bool
	systems   []string
}

// add adds v, found of pkg on goos, to the finding of pkg with v's text, or as a new one.
func (fs *findings) add(pkg, goos string, v verdict) {
	for _, f := range *fs {
		if f.pkg == pkg && f.text == v.text && f.optOut == v.optOut {
			f.systems = append(f.systems, goos)
			return
		}
	}
	*fs = append(*fs, &finding{pkg: pkg, text: v.text, optOut: v.optOut, systems: []string{goos}})
}
