package testkit

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
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
	t.Parallel() // through buildAtIn, from the module's folder; the serial tests cover BuildAt reading the test's folder
	repo := upgradeRepo(t)
	before := gitState(t, repo)
	from := filepath.Join(repo, "app", "cmd")
	home := t.TempDir()
	old := buildAtIn(t, from, "v0.1.0", "cmd/upg").Env("HOME="+home, "USERPROFILE="+home)
	cur := buildAtIn(t, from, "HEAD", "example.invalid/upg/cmd/upg").Env("HOME="+home, "USERPROFILE="+home)
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
	if byHash, byTag := buildAtIn(t, from, sha, "cmd/upg").Path(), old.Path(); byHash == "" || byHash != byTag {
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

// TestUnpackRefusesAnEntryThatLeavesItsFolderOrIsOfAnotherKind: unpackEntries, which writes a BuildAt's
// tree from git ls-tree's entries and git cat-file's answers, refuses an entry whose path climbs out of
// its folder, a file written through a link that points out of it, an absolute link it would follow,
// an entry of a kind it does not write, a tree, and a blob git answers is missing, and writes nothing
// outside its folder for any of them. The planted red of each row is unpackEntries returning nil, and
// of the last its error not being errMissing, which lacking reads to say the clone is partial.
//
// What it costs when this fails: a tree with a crafted path or link writes outside the build's
// folder, over a file of the person's, or a missing object is read as a broken repository.
func TestUnpackRefusesAnEntryThatLeavesItsFolderOrIsOfAnotherKind(t *testing.T) {
	t.Parallel()
	one, two := strings.Repeat("1", 40), strings.Repeat("2", 40)
	answer := func(object, body string) string { return fmt.Sprintf("%s blob %d\n%s\n", object, len(body), body) }
	ran := 0
	for _, tc := range []struct {
		row     string
		entries []entry
		answers string
		missing bool
	}{
		{"a path that climbs out", []entry{{"100644", "blob", one, "../outside"}}, answer(one, "x"), false},
		{"a file through a link out", []entry{{"120000", "blob", one, "out"}, {"100644", "blob", two, "out/outside"}}, answer(one, "..") + answer(two, "x"), false},
		{"a file through an absolute link", []entry{{"120000", "blob", one, "abs"}, {"100644", "blob", two, "abs/outside"}}, answer(one, "/") + answer(two, "x"), false},
		{"a tree", []entry{{"040000", "tree", one, "outside"}}, "", false},
		{"a blob git lacks", []entry{{"100644", "blob", one, "a.go"}}, one + " missing\n", true},
	} {
		ran++
		parent := t.TempDir()
		into := filepath.Join(parent, "tree")
		if err := os.Mkdir(into, 0o700); err != nil {
			t.Fatal(err)
		}
		root, err := os.OpenRoot(into)
		if err != nil {
			t.Fatal(err)
		}
		uerr := unpackEntries(root, tc.entries, bufio.NewReader(strings.NewReader(tc.answers)))
		if err := root.Close(); err != nil {
			t.Fatal(err)
		}
		if uerr == nil {
			t.Errorf("%s: unpackEntries gave nil, want a refusal", tc.row)
		} else if errors.Is(uerr, errMissing) != tc.missing {
			t.Errorf("%s: unpackEntries gave %v, which is errMissing %v, want %v", tc.row, uerr, !tc.missing, tc.missing)
		}
		if _, err := os.Lstat(filepath.Join(parent, "outside")); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s: unpackEntries wrote outside its folder: %v", tc.row, err)
		}
	}
	if ran != 5 {
		t.Fatalf("ran %d rows, want 5", ran)
	}
}

// fileURL is the file:// URL of the folder dir, a drive's path given the slash git wants before it.
func fileURL(dir string) string {
	p := filepath.ToSlash(dir)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return "file://" + p
}

// TestBuildAtInAPartialCloneFetchesNothingAndSaysTheCloneIsPartial: in a blobless clone of
// upgradeRepo (git clone --filter=blob:none from a file:// URL), which lacks v0.1.0's program, BuildAt
// of v0.1.0 fails its build, Run giving code -1 with an error that says the clone is partial and lacks
// the tree, and nothing beneath the clone's .git changed: no pack was fetched from the promisor remote.
// The planted red is GIT_NO_LAZY_FETCH left out of git's environment: git fetches the blob, writes its
// pack beneath .git, and the build passes. It needs the git CI has, 2.45 or later.
//
// What it costs when this fails: a CI checkout with filter: blob:none makes every BuildAt reach the
// network from a test, with what credentials the checkout left, and write into .git.
func TestBuildAtInAPartialCloneFetchesNothingAndSaysTheCloneIsPartial(t *testing.T) {
	t.Parallel() // buildAtIn, as BuildAt from the clone's folder
	src := upgradeRepo(t)
	gitIn(t, src, "config", "uploadpack.allowFilter", "true")
	parent := t.TempDir()
	gitIn(t, parent, "clone", "-q", "--filter=blob:none", fileURL(src), "clone")
	clone := filepath.Join(parent, "clone")
	before := gitState(t, clone)
	r := buildAtIn(t, filepath.Join(clone, "app", "cmd"), "v0.1.0", "cmd/upg").Run("version")
	if r.Code != -1 || !strings.Contains(r.Stderr, "the clone is partial") || !strings.Contains(r.Stderr, "lacks the tree of") {
		t.Errorf("v0.1.0 in a blobless clone: %+v, want code -1 and an error saying the clone is partial and lacks the tree", r)
	}
	after := gitState(t, clone)
	keys := maps.Clone(before)
	maps.Copy(keys, after)
	for _, k := range slices.Sorted(maps.Keys(keys)) {
		if before[k] != after[k] {
			t.Errorf(".git%s was %q and is %q: git fetched from the promisor remote", k, before[k], after[k])
		}
	}
}

// TestBuildAtInAPartialCloneSaysARefusalAsItIsAndNotAsAMissingTree: in a blobless clone that holds
// every object of the tree it builds, a refusal of another kind is said as itself, never as the clone
// being partial. The tree, written by git hash-object --literally, names one file twice, so the second
// write is refused as a file that exists; its blob is fetched into the clone first. The planted red is
// unpack giving every failure of the unpacking to lacking: the error says the clone is partial and
// tells the person to fetch, which fixes nothing.
//
// What it costs when this fails: a refusal on safety, a path or link that climbs out of the build's
// folder, reads as a missing object in a partial clone, and its advice (fetch, clone again) hides it.
func TestBuildAtInAPartialCloneSaysARefusalAsItIsAndNotAsAMissingTree(t *testing.T) {
	t.Parallel() // as above
	src := upgradeRepo(t)
	gitIn(t, src, "config", "uploadpack.allowFilter", "true")
	scratch := t.TempDir()
	content := filepath.Join(scratch, "dup.txt")
	if err := os.WriteFile(content, []byte("dup\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	blob := gitIn(t, src, "hash-object", "-w", content)
	raw, err := hex.DecodeString(blob)
	if err != nil {
		t.Fatal(err)
	}
	entry := append([]byte("100644 dup\x00"), raw...)
	treeFile := filepath.Join(scratch, "tree")
	if err := os.WriteFile(treeFile, append(slices.Clone(entry), entry...), 0o600); err != nil {
		t.Fatal(err)
	}
	tree := gitIn(t, src, "hash-object", "-t", "tree", "--literally", "-w", treeFile)
	commit := gitIn(t, src, "commit-tree", "-p", "HEAD", "-m", "a tree that names one file twice", tree)
	gitIn(t, src, "tag", "dup", commit)
	parent := t.TempDir()
	gitIn(t, parent, "clone", "-q", "--filter=blob:none", fileURL(src), "clone")
	clone := filepath.Join(parent, "clone")
	gitIn(t, clone, "cat-file", "blob", blob) // fetched here, with no GIT_NO_LAZY_FETCH, so the clone holds it
	r := buildAtIn(t, filepath.Join(clone, "app", "cmd"), "dup", "cmd/upg").Run("version")
	if r.Code != -1 || !strings.Contains(r.Stderr, "unpacking dup") || strings.Contains(r.Stderr, "partial") {
		t.Errorf("a tree naming dup twice, in a blobless clone that holds it: %+v, want code -1 and the refusal of dup, not said as a partial clone", r)
	}
}

// rawMain is the program of TestBuildAtReadsTheTreeAsCommitted: it prints, quoted, the two files it
// embeds.
const rawMain = `package main

import (
	_ "embed"
	"fmt"
)

//go:embed note.txt
var note string

//go:embed extra.txt
var extra string

func main() { fmt.Printf("%q %q", note, extra) }
`

// TestBuildAtReadsTheTreeAsCommitted: an old tree whose .gitattributes gives note.txt a smudge filter
// the repository's own config sets, and eol=crlf, and marks extra.txt export-ignore, and which holds a
// submodule, builds, and its program embeds both files as committed: note.txt with its LF ending and
// the filter not run, so nothing is written beneath .git, and extra.txt present. The planted red of
// each is reading the tree through git archive: the filter writes .git/smudged, note.txt ends CRLF, and
// extra.txt is left out, so the build fails.
//
// What it costs when this fails: Git LFS set up in the repository's config runs for an old tree's
// files, may download, and writes beneath .git; or the build sees a tree no checkout gives.
func TestBuildAtReadsTheTreeAsCommitted(t *testing.T) {
	t.Parallel() // buildAtIn, as BuildAt from the repository's folder
	repo := t.TempDir()
	gitIn(t, repo, "init", "-q")
	smudged := filepath.ToSlash(filepath.Join(repo, ".git", "smudged"))
	gitIn(t, repo, "config", "filter.spy.smudge", "tee '"+smudged+"'")
	writeTree(t, repo, map[string]string{
		"app/go.mod":                 "module example.invalid/raw\n\ngo 1.27\n",
		"app/cmd/raw/main.go":        rawMain,
		"app/cmd/raw/note.txt":       "raw\n",
		"app/cmd/raw/extra.txt":      "kept\n",
		"app/cmd/raw/.gitattributes": "note.txt filter=spy text eol=crlf\nextra.txt export-ignore\n",
	})
	gitIn(t, repo, "add", "-A")
	gitIn(t, repo, "commit", "-q", "-m", "a tree with a filter, an export-ignore and a submodule")
	head := gitIn(t, repo, "rev-parse", "HEAD")
	gitIn(t, repo, "update-index", "--add", "--cacheinfo", "160000,"+head+",app/sub")
	gitIn(t, repo, "commit", "-q", "-m", "the submodule")
	gitIn(t, repo, "tag", "v0.1.0")
	before := gitState(t, repo)
	if r := buildAtIn(t, filepath.Join(repo, "app"), "v0.1.0", "cmd/raw").Run(); r.Code != 0 || r.Stdout != `"raw\n" "kept\n"` {
		t.Errorf("the tree as committed: %+v, want code 0 and %q", r, `"raw\n" "kept\n"`)
	}
	after := gitState(t, repo)
	keys := maps.Clone(before)
	maps.Copy(keys, after)
	for _, k := range slices.Sorted(maps.Keys(keys)) {
		if before[k] != after[k] {
			t.Errorf(".git%s was %q and is %q: the repository's smudge filter ran", k, before[k], after[k])
		}
	}
}

// TestBuildAtBuildsWithGOWORKOffWhateverGoWorkIsNear: BuildAt builds an earlier commit with GOWORK=off,
// so a go.work in a folder above the build's folder, where the temp folder is, does not decide its
// modules: it would make go build fail with "no modules were found in the current workspace". The
// planted red is GOWORK left unset in the build's environment. Its twin below has the go.work the
// old tree commits.
//
// What it costs when this fails: a stray go.work in the temp folder, another account's in /tmp or the
// person's own, fails every BuildAt build with go's workspace error, far from its cause.
func TestBuildAtBuildsWithGOWORKOffWhateverGoWorkIsNear(t *testing.T) {
	// serial: t.Chdir into the repository and t.Setenv of the temp folder.
	repo := upgradeRepo(t)
	tmp := t.TempDir()
	writeTree(t, tmp, map[string]string{"go.work": "go 1.27\n"})
	for _, k := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(k, tmp)
	}
	t.Chdir(filepath.Join(repo, "app"))
	if r := BuildAt(t, "HEAD", "cmd/upg").Run("version"); r.Code != 0 || r.Stdout != "0.2.0\n" {
		t.Errorf("a go.work above the build's folder: %+v, want code 0 and the version", r)
	}
}

// TestBuildAtBuildsWithGOWORKOffOverAGoWorkTheOldTreeCommits: a go.work the old tree commits beside
// its go.mod, which uses nothing, does not decide the build's modules either. The planted red is GOWORK
// left unset in the build's environment: go build fails with "no modules were found in the current
// workspace".
//
// What it costs when this fails: an old release that committed its go.work cannot be built at all.
func TestBuildAtBuildsWithGOWORKOffOverAGoWorkTheOldTreeCommits(t *testing.T) {
	t.Parallel() // through buildAtIn, as above
	repo := upgradeRepo(t)
	writeTree(t, repo, map[string]string{"app/go.work": "go 1.27\n"})
	gitIn(t, repo, "add", "-A")
	gitIn(t, repo, "commit", "-q", "-m", "a go.work that uses nothing")
	if r := buildAtIn(t, filepath.Join(repo, "app"), "HEAD", "cmd/upg").Run("version"); r.Code != 0 || r.Stdout != "0.2.0\n" {
		t.Errorf("a go.work the old tree commits: %+v, want code 0 and the version", r)
	}
}
