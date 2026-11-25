package testkit

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// TestCleanKeepsWhatDoesNotLeakAndOnlyIsolatesOwnValues: under an Isolate, Clean keeps each
// variable of kept as given, and each one Isolate set with its value or with a folder beneath its
// own, and drops the run's home, a relative folder, a path that climbs out, another git
// configuration, a token and an entry with no name. GOWORK passes with any value, as this package's
// TestMain keeps it: Isolate sets none of its own. Outside an Isolate no home passes.
//
// What it costs when this fails: a child of a test reads the person's home or git configuration, or
// a test that points HOME at its own folder for a child finds it dropped.
func TestCleanKeepsWhatDoesNotLeakAndOnlyIsolatesOwnValues(t *testing.T) {
	t.Parallel()
	dir, tmp := own(t), t.TempDir()
	keep := []string{"PATH=/bin", "Path=/bin", "GORACE=halt_on_error=1", "HOME=" + filepath.Join(dir, "home"), "HOME=" + tmp,
		"XDG_STATE_HOME=" + filepath.Join(tmp, "state"), gitConfig + "=" + filepath.Join(dir, "gitconfig"), "GIT_CONFIG_NOSYSTEM=1", "GOWORK=off", "GOWORK=/person/go.work"}
	drop := []string{"HOME=/person", "XDG_STATE_HOME=planted/state", "HOME=" + filepath.Join(dir, "..", "person"), gitConfig + "=/person/.gitconfig",
		"GIT_CONFIG_NOSYSTEM=0", "GITHUB_TOKEN=x", "GIT_DIR=" + tmp, "LANG=C", "=C:=C:\\", "NOEQUALS"}
	if got := Clean(slices.Concat(keep, drop)); !slices.Equal(got, keep) {
		t.Errorf("Clean kept\n%q\nwant\n%q", got, keep)
	}
	outside := 0
	for _, k := range homes() {
		outside++
		if (isolated{}).passes(k, filepath.Join(dir, strings.ToLower(k))) {
			t.Errorf("outside an Isolate %s passes", k)
		}
	}
	if outside != len(homes()) || outside == 0 || !(isolated{}).passes("PATH", "/bin") {
		t.Fatalf("checked %d of %d homes outside an Isolate, or PATH did not pass", outside, len(homes()))
	}
}

// TestTheGoFoldersComeFromTheRunThenGosEnvFileThenGosDefault: each of goDirs is the run's own value
// when it is an absolute path, else the one go's env file holds, else go's default, and a GOENV of
// off reads no file.
//
// What it costs when this fails: a go a test starts builds into an empty cache or downloads every
// module again, or a cache the person moved with go env -w is not found.
func TestTheGoFoldersComeFromTheRunThenGosEnvFileThenGosDefault(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	file := filepath.Join(dir, "env")
	if err := os.WriteFile(file, []byte("GOPRIVATE=example.com/*\nGOMODCACHE="+abs("file/mod")+"\nGOCACHE=relative\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	defaults := []string{filepath.Join(cache, "go-build"), filepath.Join(home, "go", "pkg", "mod"), filepath.Join(home, "go")}
	ran := 0
	for name, tc := range map[string]struct {
		start, want []string
	}{
		"the run's own":      {[]string{"GOCACHE=" + abs("run/cache"), "GOMODCACHE=" + abs("run/mod"), "GOPATH=" + abs("run/go"), "GOENV=" + file}, []string{abs("run/cache"), abs("run/mod"), abs("run/go")}},
		"the env file":       {[]string{"GOENV=" + file, "GOPATH=" + abs("run/go")}, []string{defaults[0], abs("file/mod"), abs("run/go")}},
		"a relative value":   {[]string{"GOENV=off", "GOCACHE=cache", "GOPATH=go"}, defaults},
		"GOENV off":          {[]string{"GOENV=off"}, defaults},
		"a missing env file": {[]string{"GOENV=" + filepath.Join(dir, "none")}, defaults},
		"the modules of a GOPATH list": {[]string{"GOENV=off", "GOPATH=" + abs("a") + string(filepath.ListSeparator) + abs("b")},
			[]string{defaults[0], filepath.Join(abs("a"), "pkg", "mod"), abs("a") + string(filepath.ListSeparator) + abs("b")}},
	} {
		ran++
		got, err := goSettings(tc.start)
		if err != nil || !slices.Equal(got, tc.want) {
			t.Errorf("%s: %q, %v, want %q", name, got, err, tc.want)
		}
	}
	if ran != 6 {
		t.Fatalf("ran %d cases, want 6", ran)
	}
}

// ownHomevars is the one package of the module the root may import: its own list of the home, temp
// and XDG_ names.
const ownHomevars = "github.com/birdtable/go-testkit/internal/homevars"

// foreign is each import of the Go source src that is neither the standard library's, as the go
// command tells one (the first element of a standard library path holds no dot), nor ownHomevars.
func foreign(src string) ([]string, error) {
	f, err := parser.ParseFile(token.NewFileSet(), "x.go", src, parser.ImportsOnly)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, imp := range f.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			return nil, err
		}
		if first, _, _ := strings.Cut(path, "/"); strings.Contains(first, ".") && path != ownHomevars {
			out = append(out, path)
		}
	}
	return out, nil
}

// TestTheModuleImportsTheStandardLibraryAndNoOtherBlock: every Go file of the root package, tests
// included, imports the standard library and, of the module, internal/homevars alone, and none of
// birdtable's libraries; the planted sources, one that imports another package,
// one a library and one go-proc's gitenv and load beside testkit's homevars, are each red.
//
// What it costs when this fails: a block every project's tests start from pulls in another block or
// a library, and a project that took it for its few lines takes a dependency it never chose, or the
// cycle with go-proc's own tests returns.
func TestTheModuleImportsTheStandardLibraryAndNoOtherBlock(t *testing.T) {
	t.Parallel()
	planted := 0
	for name, tc := range map[string]struct {
		src  string
		want int
	}{
		"the standard library":    {"package p\n\nimport (\n\t\"os\"\n\t\"os/exec\"\n)\n", 0},
		"another package":         {"package p\n\nimport \"github.com/example/app/gitenv\"\n", 1},
		"a library":               {"package p\n\nimport _ \"go.yaml.in/yaml/v3\"\n", 1},
		"go-proc beside homevars": {"package p\n\nimport (\n\t\"github.com/birdtable/go-proc/gitenv\"\n\t\"github.com/birdtable/go-proc/load\"\n\t\"github.com/birdtable/go-testkit/internal/homevars\"\n)\n", 2},
	} {
		planted++
		if got, err := foreign(tc.src); err != nil || len(got) != tc.want {
			t.Errorf("%s: imports %q (%v), want %d that are not the standard library's", name, got, err, tc.want)
		}
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	read := 0
	for _, name := range files {
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		read++
		if got, err := foreign(string(b)); err != nil || len(got) != 0 {
			t.Errorf("%s imports %q (%v), and the root imports the standard library and internal/homevars alone", name, got, err)
		}
	}
	if planted != 4 || read < 3 {
		t.Fatalf("read %d planted sources and %d Go files of the root", planted, read)
	}
}
