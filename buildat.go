package testkit

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec" //nolint:depguard // BuildAt runs git, which reads the caller's repository and writes nothing to it
	"path"
	"path/filepath"
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
// BuildAt resolves ref to its commit at once, and builds from that commit's tree, which git archive
// writes into a folder beneath the temp folder, as Build's own build folder is. The caller's
// repository is only read: no worktree is added, nothing is checked out, and .git is unchanged. A file
// the old tree's .gitattributes marks export-ignore is left out of the archive, so out of the build.
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
	if inForce().set == nil {
		t.Fatalf("testkit: BuildAt of %q at %q: no Isolate is in force, and git would read the person's configuration", pkg, ref)
	}
	at, err := sourceAt(gitPath(t, exec.LookPath), ref, pkg)
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

// sourceAt is the source of pkg at ref in the repository the test runs in, read by git, or why there
// is none. It refuses a ref or a pkg that starts with -, and an absolute pkg, before git runs.
func sourceAt(git, ref, pkg string) (source, error) {
	switch {
	case strings.HasPrefix(ref, "-"):
		return source{}, errors.New("refused the ref, which starts with -, so git would read it as an option")
	case strings.HasPrefix(pkg, "-"):
		return source{}, errors.New("refused the package, which starts with -, so go build would read it as a flag")
	case filepath.IsAbs(pkg):
		return source{}, errors.New("refused the package, an absolute path, which names a folder of today's tree: name it by its path in the module, or by its import path")
	}
	mod, ok := modRoot()
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

// gitOut is what git prints for args, run in dir with the clean environment, less its last newline,
// or git's failure with what it wrote to stderr.
func gitOut(git, dir string, args ...string) (string, error) {
	cmd := exec.Command(git, args...) //nolint:gosec,noctx // the host's git, reading the caller's repository with fixed arguments, a ref past --end-of-options
	cmd.Dir, cmd.Env = dir, Clean(os.Environ())
	out, err := cmd.Output()
	if ee, ok := errors.AsType[*exec.ExitError](err); ok {
		return "", fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(string(ee.Stderr)))
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(strings.ReplaceAll(string(out), "\r\n", "\n"), "\n"), nil
}

// unpack writes the tree of at's commit, as git archive gives it, into the empty folder into: each
// folder, file and link, a file with the mode the archive gives it, less the umask. It writes through
// an os.Root of into, so no entry reaches outside it, through a link or otherwise.
func unpack(at source, into string) (err error) {
	root, err := os.OpenRoot(into)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	cmd := exec.Command(at.git, "archive", "--format=tar", at.commit) //nolint:gosec,noctx // the host's git, writing a full commit's tree to a pipe
	cmd.Dir, cmd.Env = at.top, Clean(os.Environ())
	var stderr strings.Builder
	cmd.Stderr = &stderr
	r, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	werr := untar(root, r)
	// Read the rest, always: the tar reader stops at the end-of-archive blocks, and git archive pads
	// its stream to a 10240-byte record past them. Wait closes the pipe only once git has exited, so
	// padding left in a pipe smaller than it, as Windows' is, blocks git and Wait with it.
	if _, err := io.Copy(io.Discard, r); err != nil {
		werr = errors.Join(werr, err)
	}
	if err := cmd.Wait(); err != nil {
		return errors.Join(werr, fmt.Errorf("git archive %s: %w: %s", at.commit, err, strings.TrimSpace(stderr.String())))
	}
	return werr
}

// untar writes each entry of the tar stream r beneath root: the global header git archive starts
// with is read past, and an entry of a kind git archive does not write is an error.
func untar(root *os.Root, r io.Reader) error {
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		name := strings.TrimSuffix(h.Name, "/")
		switch h.Typeflag {
		case tar.TypeXGlobalHeader:
			continue
		case tar.TypeDir:
			err = root.MkdirAll(name, 0o755)
		case tar.TypeReg:
			err = writeEntry(root, name, h.FileInfo().Mode().Perm(), tr)
		case tar.TypeSymlink:
			err = root.Symlink(h.Linkname, name)
		default:
			err = fmt.Errorf("it is of tar type %q, which git archive does not write", h.Typeflag)
		}
		if err != nil {
			return fmt.Errorf("unpacking %s: %w", h.Name, err)
		}
	}
}

// writeEntry writes the file name beneath root, with its folders, from r, with mode perm less the
// umask. It replaces nothing.
func writeEntry(root *os.Root, name string, perm os.FileMode, r io.Reader) error {
	if err := root.MkdirAll(path.Dir(name), 0o755); err != nil {
		return err
	}
	f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, r)
	return errors.Join(err, f.Close())
}
