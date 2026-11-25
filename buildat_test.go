package testkit

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// gitIn runs the host's git with args in dir, in the clean environment and as a fixed author, and is
// what it prints, less its last newline; a failure fails t.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), GitPath(t), append([]string{"-c", "user.name=testkit", "-c", "user.email=testkit@example.invalid", "-c", "init.defaultBranch=main"}, args...)...) //nolint:gosec // G204: the host's git, with each caller's fixed arguments
	cmd.Dir, cmd.Env = dir, Clean(os.Environ())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSuffix(strings.ReplaceAll(string(out), "\r\n", "\n"), "\n")
}

// writeTree writes each file of files, by its slash-separated path, beneath dir, with its folders.
func writeTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// upgOld is the program of upgradeRepo's v0.1.0: it reports its version, and writes its settings in
// the home in format 1, a bare colour=blue line.
const upgOld = `package main

import (
	"fmt"
	"os"
	"path/filepath"
)

const version = "0.1.0"

func main() {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	switch os.Args[1] {
	case "version":
		fmt.Println(version)
	case "write":
		if err := os.WriteFile(filepath.Join(home, "upg.conf"), []byte("colour=blue\n"), 0o600); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	default:
		os.Exit(2)
	}
}
`

// upgNew is the program of upgradeRepo's later commit: it reports its version, writes format 2, a
// format line and a colour: line, and reads either format, saying which it read.
const upgNew = `package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const version = "0.2.0"

func main() {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	file := filepath.Join(home, "upg.conf")
	switch os.Args[1] {
	case "version":
		fmt.Println(version)
	case "write":
		if err := os.WriteFile(file, []byte("format 2\ncolour: blue\n"), 0o600); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "read":
		b, err := os.ReadFile(file)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if rest, ok := strings.CutPrefix(string(b), "format 2\ncolour: "); ok {
			fmt.Println("colour", strings.TrimSpace(rest), "from format 2")
		} else if rest, ok := strings.CutPrefix(string(b), "colour="); ok {
			fmt.Println("colour", strings.TrimSpace(rest), "from format 1")
		} else {
			fmt.Fprintf(os.Stderr, "no format I know: %q\n", b)
			os.Exit(1)
		}
	default:
		os.Exit(2)
	}
}
`

// upgradeRepo makes a git repository in a folder of the test's own and returns its folder. Its module,
// example.invalid/upg, is in the folder app, below the top, and its program is app/cmd/upg: upgOld at
// the first commit, which the annotated tag v0.1.0 names, and upgNew at the second, which HEAD names
// and the worktree holds.
func upgradeRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	gitIn(t, repo, "init", "-q")
	writeTree(t, repo, map[string]string{"app/go.mod": "module example.invalid/upg\n\ngo 1.27\n", "app/cmd/upg/main.go": upgOld})
	gitIn(t, repo, "add", "-A")
	gitIn(t, repo, "commit", "-q", "-m", "v0.1.0: settings in format 1")
	gitIn(t, repo, "tag", "-a", "-m", "v0.1.0", "v0.1.0")
	writeTree(t, repo, map[string]string{"app/cmd/upg/main.go": upgNew})
	gitIn(t, repo, "commit", "-q", "-a", "-m", "settings in format 2, and format 1 still read")
	return repo
}

// gitState is each file and folder beneath repo's .git, by its path there, as its mode and, for a
// file, the hash of its bytes, so two states differ where git wrote anything.
func gitState(t *testing.T, repo string) map[string]string {
	t.Helper()
	state := map[string]string{}
	root := filepath.Join(repo, ".git")
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		v := info.Mode().String()
		if d.Type().IsRegular() {
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			v += fmt.Sprintf(" %x", sha256.Sum256(b))
		}
		state[p[len(root):]] = v
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(state) < 10 {
		t.Fatalf("read %d entries of %s, fewer than a repository with two commits holds", len(state), root)
	}
	return state
}

// TestBuildAtBuildsAnEarlierReleaseBesideTheCurrentOneAndLeavesGitAsItWas: in a repository whose
// module sits below its top, a test in a package folder of that module builds its program at the tag
// v0.1.0 by its path in the module and at HEAD by its import path. The v0.1.0 binary reports 0.1.0
// and writes its settings in format 1 in the test's home, and the HEAD binary reports 0.2.0 and reads
// them; the tag's commit, named by its hash, is the tag's one build; and nothing beneath .git changed.
//
// What it costs when this fails: an upgrade test runs today's code twice and proves nothing, or a
// test that builds an old release writes into the repository it runs in.
func TestBuildAtBuildsAnEarlierReleaseBesideTheCurrentOneAndLeavesGitAsItWas(t *testing.T) {
	// serial: t.Chdir into the repository.
	repo := upgradeRepo(t)
	before := gitState(t, repo)
	t.Chdir(filepath.Join(repo, "app", "cmd"))
	home := t.TempDir()
	old := BuildAt(t, "v0.1.0", "cmd/upg").Env("HOME="+home, "USERPROFILE="+home)
	cur := BuildAt(t, "HEAD", "example.invalid/upg/cmd/upg").Env("HOME="+home, "USERPROFILE="+home)
	ran := 0
	for _, tc := range []struct {
		b    *Binary
		want string
	}{{old, "0.1.0\n"}, {cur, "0.2.0\n"}} {
		ran++
		if r := tc.b.Run("version"); r.Code != 0 || r.Stdout != tc.want {
			t.Errorf("%s version: %+v, want code 0 and %q", tc.b.key.pkg, r, tc.want)
		}
	}
	if ran != 2 {
		t.Fatalf("ran %d builds, want 2", ran)
	}
	if r := old.Run("write"); r.Code != 0 {
		t.Fatalf("v0.1.0 write: %+v", r)
	}
	if r := cur.Run("read"); r.Code != 0 || r.Stdout != "colour blue from format 1\n" {
		t.Errorf("HEAD read: %+v, want the colour v0.1.0 wrote, read from format 1", r)
	}
	sha := gitIn(t, repo, "rev-parse", "v0.1.0^{commit}")
	if byHash, byTag := BuildAt(t, sha, "cmd/upg").Path(), old.Path(); byHash == "" || byHash != byTag {
		t.Errorf("the build of %s is %q and that of v0.1.0 %q, want the one build", sha, byHash, byTag)
	}
	after := gitState(t, repo)
	keys := maps.Clone(before)
	maps.Copy(keys, after)
	for _, k := range slices.Sorted(maps.Keys(keys)) {
		if before[k] != after[k] {
			t.Errorf(".git%s was %q and is %q: BuildAt wrote into the repository", k, before[k], after[k])
		}
	}
}

// TestBuildAtRefusesWhatItCannotReadAndNamesTheRefAndThePackage: BuildAt fails the test, and does not
// skip it, for a ref that starts with -, a ref that names no commit, a package that starts with -, an
// absolute package, a package outside the repository, a test folder with no go.mod above it, a module
// in no git repository and a test with no Isolate in force, each failure naming the ref and the
// package and saying why. The planted red of each row is BuildAt returning a Binary.
//
// What it costs when this fails: git reads a ref as an option, an upgrade test passes on a typo of
// its tag by skipping, or BuildAt reads the person's git configuration.
func TestBuildAtRefusesWhatItCannotReadAndNamesTheRefAndThePackage(t *testing.T) {
	// serial: t.Chdir into each row's folder, and t.Setenv for the last.
	repo := upgradeRepo(t)
	app := filepath.Join(repo, "app")
	empty := t.TempDir()
	plain := plantModule(t, "module example.invalid/plain\n\ngo 1.27\n", "")
	refused := func(row, ref, pkg string, want ...string) {
		t.Helper()
		returned := false
		r := record(t, func(tb testing.TB) {
			tb.Helper()
			BuildAt(tb, ref, pkg)
			returned = true
		})
		if returned || r.skipped != "" {
			t.Errorf("%s: BuildAt returned %v and skipped %q, want it to fail the test", row, returned, r.skipped)
		}
		for _, w := range append([]string{fmt.Sprintf("%q", ref), fmt.Sprintf("%q", pkg)}, want...) {
			if !strings.Contains(r.failed, w) {
				t.Errorf("%s: BuildAt failed with %q, want it to hold %q", row, r.failed, w)
			}
		}
	}
	ran := 0
	for _, tc := range []struct {
		row, dir, ref, pkg string
		want               []string
	}{
		{"a ref that starts with -", app, "--output=elsewhere", "cmd/upg", []string{"starts with -", "option"}},
		{"a ref that names no commit", app, "v9.9.9", "cmd/upg", []string{"names no commit of the repository at"}},
		{"a package that starts with -", app, "v0.1.0", "-toolexec=elsewhere", []string{"starts with -", "flag"}},
		{"an absolute package", app, "v0.1.0", filepath.Join(app, "cmd", "upg"), []string{"absolute"}},
		{"a package outside the repository", app, "v0.1.0", "../../elsewhere", []string{"outside the repository"}},
		{"no go.mod", empty, "v0.1.0", "cmd/upg", []string{"no go.mod"}},
		{"no repository", plain, "HEAD", ".", []string{"in no git repository"}},
	} {
		ran++
		t.Chdir(tc.dir)
		refused(tc.row, tc.ref, tc.pkg, tc.want...)
	}
	if ran != 7 {
		t.Fatalf("ran %d rows, want 7", ran)
	}
	t.Chdir(app)
	t.Setenv("HOME", t.TempDir())
	refused("no Isolate", "v0.1.0", "cmd/upg", "no Isolate is in force")
}

// TestBuildAtUnderGoOfflineFailsAModuleTheCacheLacksAndNamesIt: an old tree that needs a module the
// module cache lacks, built under Main given GoOffline, fails its build with go's GOPROXY=off line
// naming the module, Run giving code -1 and Path nothing, and nothing is fetched.
//
// What it costs when this fails: an upgrade test reaches the network for an old release's modules,
// and on a host with none it hangs or fails on a lookup.
func TestBuildAtUnderGoOfflineFailsAModuleTheCacheLacksAndNamesIt(t *testing.T) {
	// serial: t.Chdir into the repository, and run clears the process's environment.
	repo := t.TempDir()
	gitIn(t, repo, "init", "-q")
	writeTree(t, repo, map[string]string{
		"go.mod":  "module example.invalid/needs\n\ngo 1.27\n\nrequire example.invalid/x v1.0.0\n",
		"go.sum":  "example.invalid/x v1.0.0 h1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=\nexample.invalid/x v1.0.0/go.mod h1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=\n",
		"main.go": "package main\n\nimport _ \"example.invalid/x\"\n\nfunc main() {}\n",
	})
	gitIn(t, repo, "add", "-A")
	gitIn(t, repo, "commit", "-q", "-m", "v0.1.0: needs example.invalid/x")
	gitIn(t, repo, "tag", "v0.1.0")
	t.Chdir(repo)
	b := BuildAt(t, "v0.1.0", ".")
	var res Result
	var path string
	code, err := run(runner(func() int {
		res, path = b.Run("version"), b.Path()
		return 0
	}), offlineRun(), []Option{GoOffline()})
	if err != nil || code != 0 {
		t.Fatalf("run: code %d, %v", code, err)
	}
	if res.Code != -1 || path != "" || !strings.Contains(res.Stderr, "example.invalid/x") || !strings.Contains(res.Stderr, "module lookup disabled by GOPROXY=off") {
		t.Errorf("the build under GoOffline: %+v, path %q; want code -1, no path, and go's GOPROXY=off line naming example.invalid/x", res, path)
	}
	for _, d := range []string{"go-get=1", "https://"} {
		if strings.Contains(res.Stderr, d) {
			t.Errorf("the build printed %q, and GoOffline fetches nothing:\n%s", d, res.Stderr)
		}
	}
}
