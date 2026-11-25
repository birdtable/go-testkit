package link

import (
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const module = "github.com/birdtable/go-testkit"

// root is the module's root, two folders up from this package's; filepath.Join cleans it into the
// system's separator.
const root = "../.."

// top is the path of the module's root package, in layers.yaml and in what breaks prints.
const top = "."

// row is what layers.yaml lets one package import of the module: may for its code, and tests-may
// besides for its tests.
type row struct {
	may, testsMay []string
}

// list is the names of an inline list, "[a, b]", the one form a row's lists take.
func list(v string) ([]string, error) {
	inner, opened := strings.CutPrefix(v, "[")
	inner, closed := strings.CutSuffix(inner, "]")
	if !opened || !closed || strings.ContainsAny(inner, "[]") {
		return nil, fmt.Errorf("%q is not a list in brackets", v)
	}
	var out []string
	for n := range strings.SplitSeq(inner, ",") {
		if n = strings.TrimSpace(n); n != "" {
			out = append(out, n)
		}
	}
	return out, nil
}

// readLayers reads layers.yaml in its narrow shape: comment lines, the module line, the packages
// line, and for each package its path, role and job, its may list and at most one tests-may list. It
// is each package's row, by path; any other line, a path twice or another module refuses the file.
func readLayers(text string) (map[string]row, error) {
	out := map[string]row{}
	cur := ""
	for i, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(line)
		k, v, _ := strings.Cut(t, ": ")
		var err error
		switch {
		case t == "" || strings.HasPrefix(t, "#") || t == "packages:":
		case k == "module":
			if v != module {
				return nil, fmt.Errorf("line %d names the module %q, not %s", i+1, v, module)
			}
		case k == "- path":
			if _, dup := out[v]; dup || v == "" {
				return nil, fmt.Errorf("line %d names %q a second time, or names nothing", i+1, v)
			}
			cur, out[v] = v, row{}
		case cur != "" && (k == "role" || k == "job"):
		case cur != "" && k == "may":
			r := out[cur]
			r.may, err = list(v)
			out[cur] = r
		case cur != "" && k == "tests-may":
			r := out[cur]
			r.testsMay, err = list(v)
			out[cur] = r
		default:
			return nil, fmt.Errorf("line %d (%q) is in no shape the reader takes", i+1, t)
		}
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", i+1, err)
		}
	}
	return out, nil
}

// packages is the folder of each package of the module under dir, slash-separated and relative to
// it, the root package's a dot: each folder that holds a Go file, testdata and dot folders aside. A
// folder below dir with a go.mod of its own is another module, and nothing in it is the module's.
func packages(dir string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && p != dir {
			if strings.HasPrefix(d.Name(), ".") || d.Name() == "testdata" {
				return filepath.SkipDir
			}
			switch _, err := os.Stat(filepath.Join(p, "go.mod")); {
			case err == nil:
				return filepath.SkipDir
			case !errors.Is(err, fs.ErrNotExist):
				return err
			}
		}
		if !d.IsDir() && strings.HasSuffix(p, ".go") {
			rel, err := filepath.Rel(dir, filepath.Dir(p))
			if err != nil {
				return err
			}
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	// The walk meets the root package's files between its folders, so a folder comes up more than
	// once and not in a row: sorted first, each is named once.
	slices.Sort(out)
	return slices.Compact(out), err
}

// within is the path inside the module that the import path imp names, the root package's a dot,
// and false for an import of anything else. A package of a module nested in this one (contain) has
// such a path too, and no row, so an import of it is an edge no row lists.
func within(imp string) (string, bool) {
	if imp == module {
		return top, true
	}
	return strings.CutPrefix(imp, module+"/")
}

// imports is the module's packages that the package rel under dir imports, first from its code, then
// from its tests. Every Go file is read, whatever system or tag it builds for, since a file built on
// one system alone is as much the package's: the files are parsed for imports, no constraint read.
func imports(dir, rel string) (code, tests []string, err error) {
	entries, err := os.ReadDir(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		return nil, nil, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, filepath.FromSlash(rel), e.Name()), nil, parser.ImportsOnly)
		if err != nil {
			return nil, nil, err
		}
		for _, spec := range f.Imports {
			imp, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return nil, nil, err
			}
			sub, ok := within(imp)
			switch {
			case !ok:
			case strings.HasSuffix(e.Name(), "_test.go"):
				tests = append(tests, sub)
			default:
				code = append(code, sub)
			}
		}
	}
	return code, tests, nil
}

// breaks is each import of the module under dir that a row does not list, and each row whose path
// holds no package. A package with no row may import nothing of the module.
func breaks(dir string, rows map[string]row) ([]string, error) {
	pkgs, err := packages(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for p := range rows {
		if !slices.Contains(pkgs, p) {
			out = append(out, "the row "+p+" names no package")
		}
	}
	for _, p := range pkgs {
		code, tests, err := imports(dir, p)
		if err != nil {
			return nil, err
		}
		r := rows[p]
		for _, imp := range code {
			if !slices.Contains(r.may, imp) {
				out = append(out, p+" imports "+imp)
			}
		}
		for _, imp := range tests {
			if imp != p && !slices.Contains(r.may, imp) && !slices.Contains(r.testsMay, imp) {
				out = append(out, p+"'s tests import "+imp)
			}
		}
	}
	slices.Sort(out)
	return out, nil
}

// table is the module's layers.yaml.
func table(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, "layers.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Every package of the module imports only what its row in layers.yaml lets it, its tests only that
// and their tests-may, and every row names a package. The root package has a row.
//
// What it costs when this fails: a package takes an edge the table does not show, so a reader of the
// table, and a ruling made from it, is wrong about what depends on what.
func TestEveryPackageImportsOnlyWhatItsRowLets(t *testing.T) {
	t.Parallel()
	rows, err := readLayers(table(t))
	if err != nil {
		t.Fatal(err)
	}
	got, err := breaks(root, rows)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("the module breaks layers.yaml:\n%s", strings.Join(got, "\n"))
	}
	if _, ok := rows[top]; !ok {
		t.Fatalf("read %d rows and none for the root package, so this proves little", len(rows))
	}
}

// write plants a package rel under dir: one code file and one test file, with the imports of the
// module each names, the root package named by a dot.
func write(t *testing.T, dir, rel string, code, tests []string) {
	t.Helper()
	d := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(d, 0o700); err != nil {
		t.Fatal(err)
	}
	name := path.Base(rel)
	if rel == top {
		name = "planted"
	}
	// The test file sorts after every planted folder, so the walk meets the root package's files on
	// both sides of its folders, as it does in the module itself.
	for file, imps := range map[string][]string{"a.go": code, "z_test.go": tests} {
		src := "package " + name + "\n\n"
		for _, imp := range imps {
			src += "import _ \"" + path.Join(module, imp) + "\"\n"
		}
		if err := os.WriteFile(filepath.Join(d, file), []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// The check goes red on planted trees: for an import of the code that may does not list, a test's
// import that neither list holds, an import of a package with no row, an import the root package's
// row does not list, an import of the root package that a row does not list, an import of a module
// nested in this one, and a row with no package; a nested module's own files are not read as the
// module's; and the reader refuses a line in no shape it takes.
//
// What it costs when this fails: the table is read and never refuses, which is a table nothing reads.
func TestTheCheckRefusesAnEdgeItsRowDoesNotList(t *testing.T) {
	t.Parallel()
	base := "module: " + module + "\npackages:\n  - path: .\n    may: [b]\n  - path: a\n    role: leaf\n    job: x\n    may: [b]\n    tests-may: [c, .]\n  - path: b\n    may: []\n  - path: c\n    may: []\n"
	ran := 0
	for name, tc := range map[string]struct {
		plant func(dir string)
		want  []string
	}{
		"every edge listed": {func(dir string) {
			write(t, dir, "a", []string{"b"}, []string{"b", "c", "a", "."})
			write(t, dir, ".", []string{"b"}, []string{"b", "."})
		}, nil},
		"code takes a tests-may edge": {func(dir string) {
			write(t, dir, "a", []string{"c"}, nil)
		}, []string{"a imports c"}},
		"code takes the root by a tests-may edge": {func(dir string) {
			write(t, dir, "a", []string{"."}, nil)
		}, []string{"a imports ."}},
		"a test takes an edge no list holds": {func(dir string) {
			write(t, dir, "b", nil, []string{"a"})
		}, []string{"b's tests import a"}},
		"a package with no row": {func(dir string) {
			write(t, dir, "d", []string{"b"}, nil)
		}, []string{"d imports b"}},
		"the root takes an edge its row does not list": {func(dir string) {
			write(t, dir, ".", []string{"c"}, []string{"a"})
		}, []string{". imports c", ".'s tests import a"}},
		"the root takes a module nested in this one": {func(dir string) {
			write(t, dir, ".", []string{"n"}, nil)
		}, []string{". imports n"}},
	} {
		ran++
		dir := t.TempDir()
		write(t, dir, ".", nil, nil)
		write(t, dir, "b", nil, nil)
		write(t, dir, "c", nil, nil)
		write(t, dir, "a", nil, nil)
		// n is a module nested in the planted one: its files import what no row lets, and are not read.
		write(t, dir, "n", []string{"a", "c"}, []string{"a"})
		if err := os.WriteFile(filepath.Join(dir, "n", "go.mod"), []byte("module "+module+"/n\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		tc.plant(dir)
		rows, err := readLayers(base)
		if err != nil {
			t.Fatal(err)
		}
		got, err := breaks(dir, rows)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s: %q, want %q", name, got, tc.want)
		}
	}
	if ran != 7 {
		t.Fatalf("ran %d planted trees, want 7", ran)
	}
	dir := t.TempDir()
	write(t, dir, "a", nil, nil)
	write(t, dir, "b", nil, nil)
	write(t, dir, "n", nil, nil)
	if err := os.WriteFile(filepath.Join(dir, "n", "go.mod"), []byte("module "+module+"/n\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := breaks(dir, map[string]row{"a": {}, "b": {}, "gone": {}, "n": {}}); err != nil || !slices.Equal(got, []string{"the row gone names no package", "the row n names no package"}) {
		t.Errorf("a row with no package, and a row for a nested module: %q, %v", got, err)
	}

	for _, bad := range []string{base + "    may: b\n", base + "    may: b]\n", base + "    may: [b\n", base + "    needs: [b]\n", base + "  - path: a\n", "module: example.com/other\n", "    may: []\n"} {
		if _, err := readLayers(bad); err == nil {
			t.Errorf("the reader took %q", bad[max(0, len(bad)-30):])
		}
	}
}
