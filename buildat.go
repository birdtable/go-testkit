package testkit

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec" //nolint:depguard // BuildAt runs git, which reads the caller's repository and writes nothing to it
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// BuildAt makes a Binary for the package pkg as it was at ref, a tag, a branch or a commit of the git
// repository the test runs in, for an upgrade test that runs an earlier release's program beside the
// current one. pkg is read as Build reads it, against the module the test runs in, at the same place
// in the old tree: a path relative to that module is the folder there, and an import path is built
// from that module's folder. opts are Build's. The Binary is Build's: X, Trimpath, Dir and Env work on
// it as there, and Path, Run and Install build it on first use, once for each commit, package and flag
// set, in the cache Build uses, so two refs that name one commit build once.
//
// BuildAt resolves ref to its commit at once, and builds from that commit's tree, which it reads raw,
// by git ls-tree and git cat-file, into a folder beneath the temp folder, as Build's own build folder
// is. The caller's repository is only read: no worktree is added, nothing is checked out, and .git is
// unchanged. The tree is written as committed: no filter and no attribute applies, so a smudge filter
// the repository's config sets, Git LFS's among them, never runs, an LFS file stays its pointer file,
// line endings stay as committed, and a file marked export-ignore is there. A file keeps its executable
// bit and a link is a link, and a submodule's folder is left out, as go build reads none. Every git it
// runs is given GIT_NO_LAZY_FETCH=1, so a partial clone's promisor remote is never asked for a missing
// object: such a tree fails the build, saying the clone is partial and lacks it. That promise needs
// git 2.45 or later, the first git that reads the variable; an older git fetches the object.
// go build runs there in the clean environment Build's has, with GOWORK=off, so the old tree's own
// go.mod and go.sum decide its modules, and no go.work above the folder or in the tree does; under
// GoOffline a module the old tree needs that the module cache lacks fails the build, and go's output
// names it. git runs in the clean environment too, so it reads git's configuration as Isolate set it.
//
// BuildAt fails t, and never skips it: where no Isolate is in force; where ref starts with -, which
// git would read as an option, or names no commit; where pkg starts with -, which go build would read
// as a flag, is absolute, which names a folder of today's tree, or names a folder outside the
// repository; and where the test's folder has no go.mod above it or is in no git repository. Each
// failure names ref and pkg. A build that fails is Build's: Path is empty, and Run gives code -1 with
// go's output on Stderr.
func BuildAt(t testing.TB, ref, pkg string, opts ...Option) *Binary {
	t.Helper()
	return buildAtIn(t, "", ref, pkg, opts...) // "": the test's folder
}

// buildAtIn is BuildAt from the folder from, or the test's where from is empty: the repository and its
// module are the ones from is in. Given a folder, it reads no cwd, so BuildAt's own tests run in parallel.
func buildAtIn(t testing.TB, from, ref, pkg string, opts ...Option) *Binary {
	t.Helper()
	if inForce().set == nil {
		t.Fatalf("testkit: BuildAt of %q at %q: no Isolate is in force, and git would read the person's configuration", pkg, ref)
	}
	at, err := sourceAt(gitPath(t, exec.LookPath), from, ref, pkg)
	if err != nil {
		t.Fatalf("testkit: BuildAt of %q at %q: %v", pkg, ref, err)
	}
	return &Binary{key: buildKey{pkg: pkg + "@" + at.commit, at: at}, opts: opts}
}

// A source is the tree a BuildAt build reads: commit, a full commit of the repository whose top folder
// is top; dir, the folder of that tree, slash-separated from its top, that go build runs in; target,
// what it builds there, "." or an import path; and git, the git that reads the tree. A Build has none.
type source struct {
	git, top, commit, dir, target string
}

// sourceAt is the source of pkg at ref in the repository the folder from is in, read by git, or why
// there is none. It refuses a ref or a pkg that starts with -, and an absolute pkg, before git runs.
func sourceAt(git, from, ref, pkg string) (source, error) {
	switch {
	case strings.HasPrefix(ref, "-"):
		return source{}, errors.New("refused the ref, which starts with -, so git would read it as an option")
	case strings.HasPrefix(pkg, "-"):
		return source{}, errors.New("refused the package, which starts with -, so go build would read it as a flag")
	case filepath.IsAbs(pkg):
		return source{}, errors.New("refused the package, an absolute path, which names a folder of today's tree: name it by its path in the module, or by its import path")
	}
	mod, ok := modRootOf(from)
	if !ok {
		return source{}, errors.New("the test's folder has no go.mod above it")
	}
	out, err := gitOut(git, mod, "rev-parse", "--show-toplevel", "--show-prefix")
	if err != nil {
		return source{}, fmt.Errorf("the module at %s is in no git repository: %w", mod, err)
	}
	top, prefix, _ := strings.Cut(out, "\n")
	commit, err := gitOut(git, mod, "rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
	if err != nil {
		return source{}, fmt.Errorf("refused the ref, which names no commit of the repository at %s: %w", top, err)
	}
	at := source{git: git, top: top, commit: commit, dir: path.Join(".", prefix), target: pkg}
	if !importPath(pkg) {
		at.dir, at.target = path.Join(at.dir, filepath.ToSlash(pkg)), "."
		if !filepath.IsLocal(filepath.FromSlash(at.dir)) {
			return source{}, fmt.Errorf("refused the package, which names %s, outside the repository at %s", at.dir, top)
		}
	}
	return at, nil
}

// gitEnv is the environment of every git BuildAt runs: the clean one, so git reads its configuration
// as Isolate set it, and GIT_NO_LAZY_FETCH=1, so git asks no promisor remote for an object the
// repository lacks, and a partial clone is read as it is and written to never.
func gitEnv() []string {
	return append(Clean(os.Environ()), "GIT_NO_LAZY_FETCH=1")
}

// gitOut is what git prints for args, run in dir with gitEnv, less its last newline, or git's failure
// with what it wrote to stderr.
func gitOut(git, dir string, args ...string) (string, error) {
	out, err := gitRaw(git, dir, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(strings.ReplaceAll(string(out), "\r\n", "\n"), "\n"), nil
}

// gitRaw is gitOut's run: git's output as it wrote it.
func gitRaw(git, dir string, args ...string) ([]byte, error) {
	cmd := exec.Command(git, args...) //nolint:gosec,noctx // the host's git, reading the caller's repository with fixed arguments, a ref past --end-of-options
	cmd.Dir, cmd.Env = dir, gitEnv()
	out, err := cmd.Output()
	if ee, ok := errors.AsType[*exec.ExitError](err); ok {
		return nil, fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(string(ee.Stderr)))
	}
	return out, err
}

// errMissing is the error of an object of the tree that the repository lacks.
var errMissing = errors.New("the repository lacks the object")

// An entry is one entry of a commit's tree as git ls-tree -r names it: its git mode, the kind of its
// object, the object's name, and its slash-separated path from the tree's top.
type entry struct {
	mode, kind, object, name string
}

// unpack writes the tree of at's commit into the empty folder into, read raw: git ls-tree -r names
// each file, link and submodule of it, and git cat-file --batch gives each file's and link's bytes as
// committed, so no filter or attribute applies. It writes through an os.Root of into, so no entry
// reaches outside it, through a link or otherwise. A tree the repository lacks fails it, saying so
// where the clone is partial.
func unpack(at source, into string) (err error) {
	out, err := gitRaw(at.git, at.top, "ls-tree", "-r", "-z", "--full-tree", at.commit)
	if err != nil {
		return lacking(at, err)
	}
	entries, err := parseTree(out)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(into)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	cmd := exec.Command(at.git, "cat-file", "--batch") //nolint:gosec,noctx // the host's git, giving the objects ls-tree named
	cmd.Dir, cmd.Env = at.top, gitEnv()
	var stderr strings.Builder
	cmd.Stderr = &stderr
	in, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	r, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	// The names go in while the answers are read, so neither pipe fills while git waits on the
	// other, as Windows' small pipes would.
	sent := make(chan error, 1)
	go func() { sent <- sendNames(in, entries) }()
	werr := unpackEntries(root, entries, bufio.NewReader(r))
	// Read the rest, always: Wait closes the pipe only once git has exited, so an answer left in it
	// after a failure blocks git and Wait with it.
	if _, err := io.Copy(io.Discard, r); err != nil {
		werr = errors.Join(werr, err)
	}
	serr := <-sent
	if err := cmd.Wait(); err != nil {
		return errors.Join(werr, fmt.Errorf("git cat-file: %w: %s", err, strings.TrimSpace(stderr.String())))
	}
	if errors.Is(werr, errMissing) {
		return lacking(at, werr) // only a missing object is said as a partial clone's; a refusal stays what it is
	}
	if werr != nil {
		return werr
	}
	return serr
}

// sendNames writes the object of each file and link of entries to in, a line each, and closes it.
func sendNames(in io.WriteCloser, entries []entry) error {
	for _, e := range entries {
		if e.kind != "blob" {
			continue
		}
		if _, err := io.WriteString(in, e.object+"\n"); err != nil {
			return errors.Join(err, in.Close())
		}
	}
	return in.Close()
}

// parseTree reads the output of git ls-tree -r -z: an entry for each NUL-ended record, its mode, kind
// and object separated by spaces and its path after a tab.
func parseTree(out []byte) ([]entry, error) {
	var entries []entry
	for rec := range strings.SplitSeq(strings.TrimSuffix(string(out), "\x00"), "\x00") {
		if rec == "" {
			continue
		}
		meta, name, ok := strings.Cut(rec, "\t")
		f := strings.Split(meta, " ")
		if !ok || len(f) != 3 || name == "" {
			return nil, fmt.Errorf("git ls-tree gave %q, which is no entry of a tree", rec)
		}
		entries = append(entries, entry{mode: f[0], kind: f[1], object: f[2], name: name})
	}
	return entries, nil
}

// unpackEntries writes each entry beneath root, a file's and a link's bytes read from the answers of
// git cat-file --batch in br, one for each, in order. A submodule is left out; an entry of any other
// kind is an error.
func unpackEntries(root *os.Root, entries []entry, br *bufio.Reader) error {
	for _, e := range entries {
		var err error
		switch {
		case e.mode == "160000" && e.kind == "commit":
			continue // a submodule: its folder is left out, as git archive leaves it, and go build reads none
		case e.kind != "blob":
			err = fmt.Errorf("it is a %s of git mode %s, which BuildAt does not write", e.kind, e.mode)
		default:
			err = unpackBlob(root, e, br)
		}
		if err != nil {
			return fmt.Errorf("unpacking %s: %w", e.name, err)
		}
	}
	return nil
}

// unpackBlob reads the answer for e from br and writes it beneath root: a file of mode 100644 or
// 100755 with that mode less the umask, or a link of mode 120000 to the path its bytes hold.
func unpackBlob(root *os.Root, e entry, br *bufio.Reader) error {
	size, err := blobHeader(e, br)
	if err != nil {
		return err
	}
	if err := root.MkdirAll(path.Dir(e.name), 0o755); err != nil {
		return err
	}
	body := io.LimitReader(br, size)
	switch e.mode {
	case "100644":
		err = writeEntry(root, e.name, 0o644, body)
	case "100755":
		err = writeEntry(root, e.name, 0o755, body)
	case "120000":
		var target strings.Builder
		if _, err = io.Copy(&target, body); err == nil {
			err = root.Symlink(target.String(), e.name)
		}
	default:
		return fmt.Errorf("it is of git mode %s, which BuildAt does not write", e.mode)
	}
	if err != nil {
		return err
	}
	if b, err := br.ReadByte(); err != nil || b != '\n' {
		return fmt.Errorf("git cat-file's answer for %s did not end where its size said", e.object)
	}
	return nil
}

// blobHeader reads the first line of git cat-file's answer for e from br, and is the size of the blob
// it heads, errMissing where git lacks it, or why it is no answer for e.
func blobHeader(e entry, br *bufio.Reader) (int64, error) {
	line, err := br.ReadString('\n')
	if err != nil {
		return 0, fmt.Errorf("git cat-file ended before its answer for %s: %w", e.object, err)
	}
	f := strings.Fields(line)
	if len(f) == 2 && f[0] == e.object && f[1] == "missing" {
		return 0, fmt.Errorf("%w %s", errMissing, e.object)
	}
	if len(f) != 3 || f[0] != e.object || f[1] != "blob" {
		return 0, fmt.Errorf("git cat-file answered %q for the blob %s", strings.TrimSpace(line), e.object)
	}
	size, err := strconv.ParseInt(f[2], 10, 64)
	if err != nil || size < 0 {
		return 0, fmt.Errorf("git cat-file gave %q as the size of %s", f[2], e.object)
	}
	return size, nil
}

// lacking is err, git ls-tree failing or git cat-file answering an object missing, said as what it is
// where the repository is a partial clone: one that lacks the tree, which BuildAt does not fetch. Any
// other failure of the unpacking is not given to it, so a refusal is never said as a missing tree.
func lacking(at source, err error) error {
	promisors, cerr := gitOut(at.git, at.top, "config", "--get-regexp", `^(remote\..*\.promisor|extensions\.partialclone)$`)
	if cerr != nil || promisors == "" {
		return err
	}
	return fmt.Errorf("the clone is partial (%s) and lacks the tree of %s, which BuildAt does not fetch: fetch it first, or clone with no --filter: %w", strings.ReplaceAll(promisors, "\n", "; "), at.commit, err)
}

// writeEntry writes the file name beneath root from r, with mode perm less the umask. It replaces
// nothing.
func writeEntry(root *os.Root, name string, perm os.FileMode, r io.Reader) error {
	f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, r)
	return errors.Join(err, f.Close())
}
