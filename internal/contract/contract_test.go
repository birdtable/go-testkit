package contract_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// root is the repository's top, from this package's folder, where go test runs its test.
const root = "../.."

// gitLimit bounds each git command the test runs.
const gitLimit = time.Minute

// modules are testkit's three: each one's folder from the repository's top, which holds its api folder,
// and the prefix of its tags.
var modules = []struct{ dir, tags string }{ //nolint:gochecknoglobals // the table of the three modules
	{".", "v"},
	{"contain", "contain/v"},
	{"taskfile", "taskfile/v"},
}

// systems are the systems a contract line may name, and that "all" names.
var systems = []string{"darwin", "linux", "windows"} //nolint:gochecknoglobals // the three systems a line names

// line is one line of a contract.txt: the package's folder in its module ("." for the module's own
// package), the test that holds the promise, the systems it is about and the promise.
type line struct {
	pkg, test, systems, promise string
	text                        string // the line as the file has it
	n                           int    // its number in the file
}

// Each module's api/contract.txt is section 13's promise index. For each module:
//
//   - every contract line is four tab-separated fields, and names a test that exists in its package;
//   - none is left out of the build on a system the line names, by the file name or a build line. A test
//     that skips at run time is the release tier's to find: its runs fail on any skip;
//   - the file is sorted by package, test and systems, with no line twice;
//   - a line removed since the module's last tag is listed in api/breaks/<tag>.contract.txt, and that
//     file lists nothing else.
//
// With no tag of the module reachable from the head the last is read against no file, and the test
// says so; it never skips, since the release tier fails a skip.
//
// What it costs when this fails: a product rests on a promise no test holds, on a system where its test
// never runs, or loses one at a tag with nothing saying so.
func TestEachModulesPromiseIndexKeepsSection13(t *testing.T) {
	t.Parallel()
	git(t, "", "rev-parse", "--show-toplevel") // git reads this repository, or the test fails here
	for _, m := range modules {
		t.Run(m.dir, func(t *testing.T) {
			t.Parallel()
			api := path.Join(m.dir, "api")
			head := read(t, filepath.FromSlash(path.Join(root, api, "contract.txt")))
			lines := parse(t, head)
			for _, l := range lines {
				held(t, m.dir, l)
			}
			sorted(t, lines)
			base := lastTag(t, m.tags)
			if base == "" {
				t.Logf("no %s* tag is reachable from the head, so no line of %s/contract.txt is removed since one", m.tags, api)
			}
			removed(t, api, base, strings.TrimPrefix(base, m.dir+"/"), head)
		})
	}
}

// read is the file at name, which must be there.
func read(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return b
}

// textLines are the lines of b, with no line for the newline that ends it.
func textLines(b []byte) []string {
	s := string(b)
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

// parse reads a contract.txt, failing t for each line that is not four tab-separated fields, each with
// something in it and the systems field "all" or systems of systems, each once.
func parse(t *testing.T, b []byte) []line {
	t.Helper()
	var out []line
	for i, s := range textLines(b) {
		f := strings.Split(s, "\t")
		if len(f) != 4 || slices.Contains(f, "") || strings.TrimSpace(s) != s {
			t.Errorf("contract line %d, %q, wants four tab-separated fields: the package, the test, the systems and the promise", i+1, s)
			continue
		}
		if _, err := named(f[2]); err != nil {
			t.Errorf("contract line %d names the systems %q: %v", i+1, f[2], err)
			continue
		}
		out = append(out, line{pkg: f[0], test: f[1], systems: f[2], promise: f[3], text: s, n: i + 1})
	}
	return out
}

// named is the systems a systems field names: all three for "all", else each system it lists once.
func named(field string) ([]string, error) {
	if field == "all" {
		return systems, nil
	}
	var out []string
	for s := range strings.SplitSeq(field, ",") {
		if !slices.Contains(systems, s) {
			return nil, fmt.Errorf("%q is not one of %v, nor all", s, systems)
		}
		if slices.Contains(out, s) {
			return nil, fmt.Errorf("%q is named twice", s)
		}
		out = append(out, s)
	}
	return out, nil
}

// held fails t unless the line's test is a top-level func of its package's test files, in a file built
// on each system the line names, with cgo and without.
func held(t *testing.T, module string, l line) {
	t.Helper()
	dir := filepath.FromSlash(path.Join(root, module, l.pkg))
	files, err := filepath.Glob(filepath.Join(dir, "*_test.go"))
	if err != nil || len(files) == 0 {
		t.Errorf("contract line %d: the package %s has no test file in %s (%v)", l.n, l.pkg, dir, err)
		return
	}
	in := ""
	fset := token.NewFileSet()
	for _, f := range files {
		file, err := parser.ParseFile(fset, f, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Errorf("contract line %d: %v", l.n, err)
			return
		}
		for _, d := range file.Decls {
			if fn, ok := d.(*ast.FuncDecl); ok && fn.Recv == nil && fn.Name.Name == l.test {
				in = filepath.Base(f)
			}
		}
	}
	if in == "" {
		t.Errorf("contract line %d names %s in %s, and no test of that name is there", l.n, l.test, l.pkg)
		return
	}
	on, _ := named(l.systems)
	for _, goos := range on {
		for _, cgo := range []bool{false, true} {
			ctx := build.Default
			ctx.GOOS, ctx.GOARCH, ctx.CgoEnabled = goos, "amd64", cgo
			ok, err := ctx.MatchFile(dir, in)
			if err != nil {
				t.Errorf("contract line %d: reading %s's build lines: %v", l.n, in, err)
				return
			}
			if !ok {
				t.Errorf("contract line %d names %s for %s, and its file %s is not built there (cgo %v)", l.n, l.test, goos, in, cgo)
			}
		}
	}
}

// sorted fails t unless each line comes after the one before it by package, then test, then systems.
func sorted(t *testing.T, lines []line) {
	t.Helper()
	for i := 1; i < len(lines); i++ {
		a, b := lines[i-1], lines[i]
		switch c := slices.Compare([]string{a.pkg, a.test, a.systems}, []string{b.pkg, b.test, b.systems}); {
		case c == 0:
			t.Errorf("contract lines %d and %d both give %s %s for %s; a test's promise about some systems is one line", a.n, b.n, b.pkg, b.test, b.systems)
		case c > 0:
			t.Errorf("contract line %d, %s %s, comes before line %d, %s %s; the file is sorted by package, test and systems", a.n, a.pkg, a.test, b.n, b.pkg, b.test)
		}
	}
}

// noTag are git describe's own answers that no tag is reachable; any other failure is the test's.
var noTag = []string{"No names found", "No tags can describe"} //nolint:gochecknoglobals // git's two answers

// lastTag is the module's last tag reachable from the head, or "" where git answers there is none. A
// shallow clone fails t, since the history it lacks would read as no tag, and so does any other failure
// of git describe.
func lastTag(t *testing.T, prefix string) string {
	t.Helper()
	if shallow := string(git(t, "", "rev-parse", "--is-shallow-repository")); shallow != "false" {
		t.Fatalf("this clone is shallow (%s), so its last %s* tag cannot be found; fetch its whole history (git fetch --unshallow)", shallow, prefix)
	}
	out, err := gitErr(t, "", "describe", "--tags", "--abbrev=0", "--match", prefix+"*")
	if err != nil {
		if !slices.ContainsFunc(noTag, func(s string) bool { return strings.Contains(err.Error(), s) }) {
			t.Fatalf("git describe could not read the %s* tags: %v", prefix, err)
		}
		t.Logf("git describe finds no %s* tag: %v", prefix, err)
		return ""
	}
	return string(out)
}

// atTag is the file at name in the tag's tree, and whether the tree has it. git ls-tree lists nothing
// for a path the tree lacks; a failure of git fails t.
func atTag(t *testing.T, tag, name string) ([]byte, bool) {
	t.Helper()
	if tag == "" {
		return nil, false
	}
	if len(git(t, "", "ls-tree", "--name-only", tag, "--", name)) == 0 {
		return nil, false
	}
	return git(t, "", "show", tag+":"+name), true
}

// removed fails t unless the lines the contract had at base and has no longer are what
// api/breaks/<version>.contract.txt lists, no more and no fewer. No file lists none.
func removed(t *testing.T, api, base, version string, head []byte) {
	t.Helper()
	old, _ := atTag(t, base, api+"/contract.txt")
	now := textLines(head)
	var gone []string
	for _, s := range textLines(old) {
		if !slices.Contains(now, s) {
			gone = append(gone, s)
		}
	}
	var listed []string
	if base != "" {
		name := filepath.FromSlash(path.Join(root, api, "breaks", version+".contract.txt"))
		if _, err := os.Stat(name); err == nil {
			listed = textLines(read(t, name))
		}
	}
	for _, s := range gone {
		if !slices.Contains(listed, s) {
			t.Errorf("%s/contract.txt had %q at %s and has it no longer, and %s/breaks/%s.contract.txt does not list it", api, s, base, api, version)
		}
	}
	for _, s := range listed {
		if !slices.Contains(gone, s) {
			t.Errorf("%s/breaks/%s.contract.txt lists %q, which is no line removed from %s/contract.txt since %s", api, version, s, api, base)
		}
	}
}

// git is what git prints given args in the repository, its last newline cut, failing t on an error.
func git(t *testing.T, dir string, args ...string) []byte {
	t.Helper()
	out, err := gitErr(t, dir, args...)
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return out
}

// gitErr is what git prints given args in the repository, its last newline cut, or the error with
// what git said on stderr.
func gitErr(t *testing.T, dir string, args ...string) ([]byte, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), gitLimit)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...) //nolint:gosec // G204: git, with the test's own arguments and tag names this repository's git gives
	cmd.Dir = filepath.Join(root, dir)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, errors.Join(err, ctx.Err())
		}
		return nil, fmt.Errorf("%w: %s", err, bytes.TrimSpace(stderr.Bytes()))
	}
	return bytes.TrimSuffix(stdout.Bytes(), []byte("\n")), nil
}
