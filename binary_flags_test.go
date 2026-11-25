package testkit

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// progFlags prints the version its -X set, then the file name its own source has in the binary,
// which -trimpath makes the module path in place of the folder it was built in.
const progFlags = `package main

import (
	"fmt"
	"runtime"
)

var version = "unset"

func main() {
	_, file, _, _ := runtime.Caller(0)
	fmt.Printf("%s\n%s\n", version, file)
}
`

// trimmedFile is the name progFlags's source has in a binary built with -trimpath.
const trimmedFile = "example.invalid/prog/main.go"

// flagsRun runs b and is the version and the source file name progFlags printed.
func flagsRun(t *testing.T, b *Binary) (version, file string) {
	t.Helper()
	r := b.Run()
	lines := strings.Split(strings.TrimSpace(r.Stdout), "\n")
	if r.Code != 0 || len(lines) != 2 {
		t.Fatalf("the program: code %d, stdout %q, stderr %q", r.Code, r.Stdout, r.Stderr)
	}
	return lines[0], lines[1]
}

// TestXAndTrimpathReachTheBuildAndEachFlagSetIsABuildOfItsOwn: X sets the program's version and
// Trimpath takes the build folder out of its source names; one package built with two flag sets is
// two binaries, and the same flags share one, however the X calls were ordered; and a second X of a
// name replaces the first.
//
// What it costs when this fails: a version golden or a reproducible-build check reads a binary
// built without its flags, or another test's binary built with other flags.
func TestXAndTrimpathReachTheBuildAndEachFlagSetIsABuildOfItsOwn(t *testing.T) {
	t.Parallel()
	prog := plantProgram(t, progFlags)
	plain, a, b := Build(prog), Build(prog).X("main.version", "a"), Build(prog).X("main.version", "b")
	trim, trimA := Build(prog).Trimpath(), Build(prog).Trimpath().X("main.version", "a")
	if v, file := flagsRun(t, plain); v != "unset" || !filepath.IsAbs(file) {
		t.Errorf("a plain build printed version %q and file %q, want unset and an absolute path", v, file)
	}
	if v, _ := flagsRun(t, a); v != "a" {
		t.Errorf(`X("main.version", "a") printed version %q, want a`, v)
	}
	if v, file := flagsRun(t, trim); v != "unset" || file != trimmedFile {
		t.Errorf("Trimpath printed version %q and file %q, want unset and %q", v, file, trimmedFile)
	}
	if v, file := flagsRun(t, trimA); v != "a" || file != trimmedFile {
		t.Errorf("Trimpath and X printed version %q and file %q, want a and %q", v, file, trimmedFile)
	}
	paths := map[string]string{}
	for name, bin := range map[string]*Binary{"plain": plain, "a": a, "b": b, "trimpath": trim, "trimpath and a": trimA} {
		p := bin.Path()
		if other, ok := paths[p]; ok || p == "" {
			t.Errorf("the %s build's path %q is empty or the %s build's too", name, p, other)
		}
		paths[p] = name
	}
	for _, same := range []struct {
		name      string
		got, want *Binary
	}{
		{"a second Build with the same X", Build(prog).X("main.version", "a"), a},
		{"a second X of the name", Build(prog).X("main.version", "a").X("main.version", "b"), b},
		{"X then Trimpath", Build(prog).X("main.version", "a").Trimpath(), trimA},
		{"two X in the other order", Build(prog).X("main.other", "o").X("main.version", "a"), Build(prog).X("main.version", "a").X("main.other", "o")},
	} {
		if got, want := same.got.Path(), same.want.Path(); got != want || got == "" {
			t.Errorf("%s built %q, want the one build %q", same.name, got, want)
		}
	}
}

// progVars prints, one quoted line each, the variable of its inner package and each of its own, in
// the order varsSet names them.
const progVars = `package main

import (
	"fmt"

	"example.invalid/prog/inner"
)

var (
	spaces = "unset"
	empty  = "unset"
	single = "unset"
	double = "unset"
	both   = "unset"
	white  = "unset"
	at     = "unset"
	flag   = "unset"
)

func main() {
	for _, v := range []string{inner.Version, spaces, empty, single, double, both, white, at, flag} {
		fmt.Printf("%q\n", v)
	}
}
`

// varsSet are the variables progVars prints, in its order, and the value each row sets.
var varsSet = []struct{ row, name, value string }{ //nolint:gochecknoglobals // a test table, read only
	{"a name with a dot path, first in name order, with a value that is quoted", "example.invalid/prog/inner.Version", "1.2.3 (dev)"},
	{"a value with spaces and =", "main.spaces", "a b=c = d"},
	{"an empty value", "main.empty", ""},
	{"a value with spaces and '", "main.single", "it's set"},
	{`a value with spaces and "`, "main.double", `say "set"`},
	{"a value with both quotes and no white space", "main.both", `a'b"c`},
	{"a value with a tab and a newline", "main.white", "a\tb\nc"},
	{"a value that starts with @, read by the linker as no file", "main.at", "@/planted/flags"},
	{"a value that holds a linker flag, read by the linker as no flag", "main.flag", "a -extld=/planted/linker"},
}

// TestXSetsEachValueAsTheBuiltProgramReadsIt: each name and value of varsSet, set by X on one
// build, is what the built program reads: a variable of a package with a dot path, whose value
// is quoted and first in name order, so the -ldflags value would start with a quote were -X not a
// word of its own, and values with spaces and =, empty, with either quote, with a tab and a newline,
// starting with @, or holding a linker flag, each one word to go build and the linker.
//
// What it costs when this fails: a version golden reads a value cut at a space or an =, go build
// refuses the -ldflags value of a first variable whose value holds a space, an empty value leaves
// the variable as it was, or a value reaches the linker as a flag or a file of flags.
func TestXSetsEachValueAsTheBuiltProgramReadsIt(t *testing.T) {
	t.Parallel()
	prog := plantProgram(t, progVars)
	if err := os.Mkdir(filepath.Join(prog, "inner"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(prog, "inner", "inner.go"), []byte("package inner\n\nvar Version = \"unset\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	b := Build(prog)
	for _, v := range varsSet {
		b = b.X(v.name, v.value)
	}
	r := b.Run()
	lines := strings.Split(strings.TrimSuffix(r.Stdout, "\n"), "\n")
	if r.Code != 0 || len(lines) != len(varsSet) {
		t.Fatalf("the program: code %d, stdout %q, stderr %q", r.Code, r.Stdout, r.Stderr)
	}
	for i, v := range varsSet {
		if got, err := strconv.Unquote(lines[i]); err != nil || got != v.value {
			t.Errorf("%s: X(%q, %q) left the program reading %s, want %q", v.row, v.name, v.value, lines[i], v.value)
		}
	}
}

// progLast prints its one variable, quoted.
const progLast = `package main

import "fmt"

var version = "unset"

func main() { fmt.Printf("%q\n", version) }
`

// TestXKeepsTheWhiteSpaceThatEndsTheLastValue: a value that ends in a rune unicode.IsSpace matches
// and go build does not split on, a \v or a U+00A0, set on the one variable, so its word is the last
// of the -ldflags value, is what the built program reads, byte for byte. go build trims the whole
// value with strings.TrimSpace before it splits it, so such a word, unquoted, loses it.
//
// What it costs when this fails: a version golden reads a value cut short, with no error, and the
// cut comes back when a variable that sorts later is added.
func TestXKeepsTheWhiteSpaceThatEndsTheLastValue(t *testing.T) {
	t.Parallel()
	prog := plantProgram(t, progLast)
	ran := 0
	for _, value := range []string{"1.2.3\v", "1.2.3 "} {
		ran++
		r := Build(prog).X("main.version", value).Run()
		if got, err := strconv.Unquote(strings.TrimSuffix(r.Stdout, "\n")); r.Code != 0 || err != nil || got != value {
			t.Errorf("X(main.version, %q): code %d, the program read %s, stderr %q; want %q", value, r.Code, r.Stdout, r.Stderr, value)
		}
	}
	if ran != 2 {
		t.Fatalf("ran %d cases, want 2", ran)
	}
}

// TestXRefusesWhatTheLinkerWouldNotReadAsOneVariableAndNeverRunsGo: a name that is not an import
// path, a dot and a Go identifier is refused, and so is a value with white space and both quotes:
// Path is empty, Run gives code -1 with the reason, a later X keeps the refusal, and no build of the
// package is made, so go never ran.
//
// What it costs when this fails: a name carries an =, a quote or white space into the -ldflags word,
// so go build or the linker reads a second word, a flag the test author did not write.
func TestXRefusesWhatTheLinkerWouldNotReadAsOneVariableAndNeverRunsGo(t *testing.T) {
	t.Parallel()
	prog := plantProgram(t, progFlags)
	for _, tc := range []struct{ row, name, value, says string }{
		{"an empty name", "", "a", `refuses ""`},
		{"a name with no dot", "version", "a", `refuses "version"`},
		{"a name with no path", ".version", "a", `refuses ".version"`},
		{"a name with no variable", "main.", "a", `refuses "main."`},
		{"a variable that is no identifier", "main.1version", "a", `refuses "main.1version"`},
		{"a variable that is a keyword", "main.func", "a", `refuses "main.func"`},
		{"a name with an =", "main.version=-extld", "a", `refuses "main.version=-extld"`},
		{"a path with an =", "main=x.version", "a", `refuses "main=x.version"`},
		{"a name with a space", "main.version -extld", "a", `refuses "main.version -extld"`},
		{"a path with a space", "main -extld=/planted/linker.version", "a", `refuses "main -extld=/planted/linker.version"`},
		{"a name with a newline", "main.version\n", "a", `refuses "main.version\n"`},
		{"a name with a quote", "main'.version", "a", `refuses "main'.version"`},
		{"a path that starts with -", "-extld.version", "a", `refuses "-extld.version"`},
		{"a path that starts with /", "/planted.version", "a", `refuses "/planted.version"`},
		{"a path that starts with @", "@planted.version", "a", `refuses "@planted.version"`},
		{"a value with white space and both quotes", "main.version", `a ' " b`, "the value for main.version"},
		{"a value with a no-break space and both quotes", "main.version", "a' \"b", "the value for main.version"},
	} {
		for how, b := range map[string]*Binary{
			"X":             Build(prog).X(tc.name, tc.value),
			"X then a good": Build(prog).X(tc.name, tc.value).X("main.version", "a"),
		} {
			if p := b.Path(); p != "" {
				t.Errorf("%s, %s: Path() = %q, want empty", tc.row, how, p)
			}
			r := b.Run()
			if r.Code != -1 || !strings.Contains(r.Stderr, "testkit: X refuses") || !strings.Contains(r.Stderr, tc.says) {
				t.Errorf("%s, %s: Run() = %+v, want code -1 and the refusal naming %s", tc.row, how, r, tc.says)
			}
		}
	}
	buildsMu.Lock()
	defer buildsMu.Unlock()
	for key := range builds {
		if key.pkg == prog {
			t.Errorf("a refused X made a build of the package: %+v", key)
		}
	}
}

// TestBuildRefusesAPackageGoBuildWouldReadAsAFlag: a package that starts with - is refused as a
// refused X is, and so is every copy X and Trimpath make of it: Path is empty, Run gives code -1
// with the reason, and no build of it is made.
//
// What it costs when this fails: Build's package reaches go build as a flag, -toolexec running a
// program around every tool or an -ldflags replacing the one X wrote.
func TestBuildRefusesAPackageGoBuildWouldReadAsAFlag(t *testing.T) {
	t.Parallel()
	for _, pkg := range []string{"-toolexec=./planted", "-ldflags=-extld=./planted"} {
		for name, b := range map[string]*Binary{
			"Build":                   Build(pkg),
			"X after it":              Build(pkg).X("main.version", "a"),
			"Trimpath after it":       Build(pkg).Trimpath(),
			"X after a refused X too": Build(pkg).X("version", "a").X("main.version", "a"),
		} {
			if p := b.Path(); p != "" {
				t.Errorf("%s of Build(%q): Path() = %q, want empty", name, pkg, p)
			}
			r := b.Run()
			if r.Code != -1 || !strings.Contains(r.Stderr, "Build refuses a package that starts with -") || !strings.Contains(r.Stderr, pkg) {
				t.Errorf("%s of Build(%q): Run() = %+v, want code -1 and the refusal naming the package", name, pkg, r)
			}
		}
		buildsMu.Lock()
		for key := range builds {
			if key.pkg == pkg {
				t.Errorf("a refused Build made a build: %+v", key)
			}
		}
		buildsMu.Unlock()
	}
}

// TestXAndTrimpathLeaveTheBinaryTheyAreCalledOnAsItWas: each returns a new Binary with an options
// slice and a variables map of its own, and the one it was called on keeps its flags, its options,
// its variables and no refusal; and two keys whose fields joined would read alike stay two builds.
//
// What it costs when this fails: a test that shares one Binary across cases has its build changed
// under it by a later X or Trimpath, or a refusal on one copy reaches another.
func TestXAndTrimpathLeaveTheBinaryTheyAreCalledOnAsItWas(t *testing.T) {
	t.Parallel()
	b := Build(plantProgram(t, progFlags), Keep("X_A")).X("main.version", "a")
	before := *b
	x, tp, refused := b.X("main.other", "o"), b.Trimpath(), b.X("version", "a")
	if b.key != before.key || b.refused != nil || len(b.opts) != 1 || len(b.vars) != 1 {
		t.Fatalf("the Binary is %+v after X and Trimpath, want it as it was, %+v", *b, before)
	}
	if x.key.ldflags != "-X main.other=o -X main.version=a" || x.key.trimpath || !tp.key.trimpath || tp.key.ldflags != "-X main.version=a" || refused.refused == nil {
		t.Errorf("X gave %+v, Trimpath %+v and the refused X %+v", x.key, tp.key, refused)
	}
	for name, c := range map[string]*Binary{"X": x, "Trimpath": tp, "the refused X": refused} {
		if c == b || len(c.opts) != 1 || &c.opts[0] == &b.opts[0] {
			t.Errorf("%s shares the Binary or its options slice", name)
		}
	}
	if tp.X("main.third", "t"); len(tp.vars) != 1 || len(b.vars) != 1 {
		t.Errorf("an X on a copy reached the variables of another: %v and %v", tp.vars, b.vars)
	}
	if buildOf(buildKey{pkg: "p -X", ldflags: "a"}) == buildOf(buildKey{pkg: "p", ldflags: "-X a"}) {
		t.Error("two keys whose package and flags joined read alike share one build")
	}
}

// TestATestsGOFLAGSDoesNotReachTheBuild: a GOFLAGS the test set, asking for -trimpath and an -X,
// changes neither the plain build nor one with its own X: the flags come only from X and Trimpath.
//
// What it costs when this fails: a test's GOFLAGS changes the one shared binary every other test runs,
// or adds linker flags X never writes.
func TestATestsGOFLAGSDoesNotReachTheBuild(t *testing.T) {
	// serial: t.Setenv forbids t.Parallel, and every other test's build reads GOFLAGS.
	t.Setenv("GOFLAGS", "-trimpath -ldflags=-X=main.version=fromgoflags")
	prog := plantProgram(t, progFlags)
	if v, file := flagsRun(t, Build(prog)); v != "unset" || !filepath.IsAbs(file) {
		t.Errorf("a plain build under the test's GOFLAGS printed version %q and file %q, want unset and an absolute path", v, file)
	}
	if v, file := flagsRun(t, Build(prog).X("main.version", "own")); v != "own" || !filepath.IsAbs(file) {
		t.Errorf(`X("main.version", "own") under the test's GOFLAGS printed version %q and file %q, want own and an absolute path`, v, file)
	}
}

// TestCoverStillWritesWithXAndTrimpath: under GOCOVERDIR a build with X and Trimpath is still built
// with -cover, so its run writes what it reached there.
//
// What it costs when this fails: task cover loses the runs of every binary built with flags.
func TestCoverStillWritesWithXAndTrimpath(t *testing.T) {
	// serial: t.Setenv forbids t.Parallel, and every other test's build reads GOCOVERDIR.
	dir := t.TempDir()
	t.Setenv("GOCOVERDIR", dir)
	if v, _ := flagsRun(t, Build(plantProgram(t, progFlags)).X("main.version", "c").Trimpath()); v != "c" {
		t.Errorf("the covered build printed version %q, want c", v)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	wrote := false
	for _, e := range entries {
		wrote = wrote || strings.HasPrefix(e.Name(), "covmeta.")
	}
	if !wrote {
		t.Errorf("the run wrote no coverage to GOCOVERDIR: %v", entries)
	}
}
