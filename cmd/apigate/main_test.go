package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/birdtable/go-testkit"
)

func TestMain(m *testing.M) {
	code, err := testkit.Main(m, testkit.GoOffline())
	if err != nil {
		panic(err)
	}
	os.Exit(code)
}

// repo is a git repository a test planted, in a folder of its own.
type repo struct {
	t   *testing.T
	dir string
}

// plant makes a repository holding the module example.com/m at its top: a package with the funcs A
// and B, testkit's own tools/apidiff.mod and its sum, a .gitattributes of eol=lf, and an
// api/systems.txt and api/marks.txt of a comment line each. Nothing is committed.
func plant(t *testing.T) *repo {
	t.Helper()
	r := &repo{t: t, dir: t.TempDir()}
	r.git("init", "--quiet", "--ref-format=files", "--initial-branch=main")
	files := map[string]string{
		".gitattributes":  "* text eol=lf\n",
		"go.mod":          "module example.com/m\n\n" + goLine(t) + "\n",
		"m.go":            "// Package m is a module the gate reads.\npackage m\n\n// A is one.\nfunc A() int { return 1 }\n\n// B is two.\nfunc B() int { return 2 }\n",
		"api/systems.txt": "# The lines by which the systems differ.\n",
		"api/marks.txt":   "# The marks.\n",
	}
	for _, name := range []string{"apidiff.mod", "apidiff.sum"} {
		b, err := os.ReadFile(filepath.Join("..", "..", "tools", name))
		if err != nil {
			t.Fatal(err)
		}
		files["tools/"+name] = string(b)
	}
	r.write(files)
	return r
}

// goLine is testkit's own go line, which a planted module takes.
func goLine(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	for line := range strings.Lines(string(b)) {
		if strings.HasPrefix(line, "go ") {
			return strings.TrimSpace(line)
		}
	}
	t.Fatal("testkit's go.mod has no go line")
	return ""
}

// write writes each file at its slash-separated name under the top, and removes each whose
// content is the empty string.
func (r *repo) write(files map[string]string) {
	r.t.Helper()
	for name, content := range files {
		p := filepath.Join(r.dir, filepath.FromSlash(name))
		if content == "" {
			if err := os.Remove(p); err != nil {
				r.t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			r.t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			r.t.Fatal(err)
		}
	}
}

// git runs git with args at the top and is its output, failing the test where git fails.
func (r *repo) git(args ...string) string {
	r.t.Helper()
	ident := []string{"-c", "user.name=apigate", "-c", "user.email=apigate@example.invalid", "-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false"}
	c := exec.CommandContext(r.t.Context(), testkit.GitPath(r.t), append(ident, args...)...) //nolint:gosec // G204: the host's git, with the test's own arguments
	c.Dir = r.dir
	b, err := c.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, b)
	}
	return string(b)
}

// commit commits every file, and tags the commit with each of tags.
func (r *repo) commit(tags ...string) {
	r.t.Helper()
	r.git("add", "-A")
	r.git("commit", "--quiet", "--allow-empty", "-m", "planted")
	for _, tag := range tags {
		r.git("tag", tag)
	}
}

// gate runs the gate with -dir naming folder below the top, slash-separated, or the top for "", and args,
// and gives its exit code and lines.
func (r *repo) gate(folder string, args ...string) (int, string) {
	r.t.Helper()
	var out, errOut bytes.Buffer
	code := run(r.t.Context(), append([]string{"-dir", filepath.Join(r.dir, filepath.FromSlash(folder))}, args...), &out, &errOut)
	return code, out.String() + errOut.String()
}

// windowsOnly is a file built on Windows alone that adds the name WinOnly there.
const windowsOnly = "package m\n\n// WinOnly is Windows' alone.\nfunc WinOnly() int { return 3 }\n"

// subModule plants the module example.com/m/sub in the folder sub, with the func Read, its own api
// files, and the funcs given in more.
func subModule(r *repo, more string) {
	r.write(map[string]string{
		"sub/go.mod":          "module example.com/m/sub\n\n" + goLine(r.t) + "\n",
		"sub/sub.go":          "// Package sub is a module of its own in a folder.\npackage sub\n\n// Read reads.\nfunc Read() {}\n" + more,
		"sub/api/systems.txt": "# The lines by which the systems differ.\n",
		"sub/api/marks.txt":   "# The marks.\n",
	})
}

// gateRow is a tree the gate reads, planted on a repository plant made, and what the gate says of it.
type gateRow struct {
	name   string
	tree   func(r *repo)
	folder string   // the module's folder below the top, or "" for the top
	args   []string // the gate's arguments but -dir
	code   int      // the exit code
	lines  []string // lines the gate's output holds, each whole
}

// check runs each row in parallel and fails each whose code or lines are not the row's.
func check(t *testing.T, rows []gateRow) {
	t.Helper()
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			r := plant(t)
			row.tree(r)
			code, out := r.gate(row.folder, row.args...)
			have := strings.Split(out, "\n")
			for _, want := range row.lines {
				if !slices.Contains(have, want) {
					t.Errorf("the gate's output holds no line %q:\n%s", want, out)
				}
			}
			if code != row.code {
				t.Errorf("the gate exits %d, want %d:\n%s", code, row.code, out)
			}
		})
	}
}

// Each check of the gate is red under its break, the rows go-proc's gate went red under,
// with testkit's reading of no tag and a module in a folder of its own.
func TestEachCheckOfTheGateIsRedUnderItsBreak(t *testing.T) {
	t.Parallel()
	check(t, []gateRow{
		{
			name: "a name one system alone has",
			tree: func(r *repo) {
				r.write(map[string]string{"w_windows.go": windowsOnly})
				r.commit()
			},
			code: 1,
			lines: []string{
				"+windows\tCompatible changes:",
				"+windows\t- WinOnly: added",
				"api: the systems' surfaces are not what api/systems.txt says (- the file, + apidiff from darwin to each system)",
				"api: no tag yet, so no break is held; the systems and the marks are",
			},
		},
		{
			name: "a signature one system alone has, seen by the systems and the breaks alike",
			tree: func(r *repo) {
				r.write(map[string]string{"n.go": "package m\n\n// N names.\nfunc N() string { return \"\" }\n"})
				r.commit("v0.1.0")
				r.write(map[string]string{
					"n.go":         "",
					"n_other.go":   "//go:build !windows\n\npackage m\n\n// N names.\nfunc N() string { return \"\" }\n",
					"n_windows.go": "package m\n\n// N names.\nfunc N() (string, error) { return \"\", nil }\n",
				})
				r.commit()
			},
			code: 1,
			lines: []string{
				"+windows\t- N: changed from func() string to func() (string, error)",
				"+- N: changed from func() string to func() (string, error)",
				"api: the breaks since v0.1.0 are not api/breaks/v0.1.0.txt (- the file, + apidiff); a commit that removes or changes an exported name adds apidiff's line there",
			},
		},
		{
			name: "a line api/systems.txt keeps that no longer happens",
			tree: func(r *repo) {
				r.write(map[string]string{"api/systems.txt": "# The lines by which the systems differ.\nwindows\tCompatible changes:\nwindows\t- WinOnly: added\n"})
				r.commit()
			},
			code: 1,
			lines: []string{
				"-windows\t- WinOnly: added",
				"api: the systems' surfaces are not what api/systems.txt says (- the file, + apidiff from darwin to each system)",
			},
		},
		{
			name: "an exported name removed since the tag with no line in api/breaks/<tag>.txt",
			tree: func(r *repo) {
				r.commit("v0.1.0")
				r.write(map[string]string{"m.go": "// Package m is a module the gate reads.\npackage m\n\n// A is one.\nfunc A() int { return 1 }\n"})
				r.commit()
			},
			code: 1,
			lines: []string{
				"+- B: removed",
				"api: the three systems' surfaces differ only as api/systems.txt says",
				"api: the breaks since v0.1.0 are not api/breaks/v0.1.0.txt (- the file, + apidiff); a commit that removes or changes an exported name adds apidiff's line there",
			},
		},
		{
			name: "a line api/breaks/<tag>.txt keeps for a break the tree does not make",
			tree: func(r *repo) {
				r.commit("v0.1.0")
				r.write(map[string]string{"api/breaks/v0.1.0.txt": "- A: removed\n"})
				r.commit()
			},
			code: 1,
			lines: []string{
				"-- A: removed",
				"api: the breaks since v0.1.0 are not api/breaks/v0.1.0.txt (- the file, + apidiff); a commit that removes or changes an exported name adds apidiff's line there",
			},
		},
		{
			name: "a module that does not build for one system",
			tree: func(r *repo) {
				r.write(map[string]string{"bad_windows.go": "package m\n\nfunc bad() int { return \"x\" }\n"})
				r.commit()
			},
			code:  1,
			lines: []string{"api: apidiff cannot write the module's export data for windows, so nothing is held"},
		},
		{
			name: "a mark naming a name go doc does not find",
			tree: func(r *repo) {
				r.write(map[string]string{"api/marks.txt": "# The marks.\n.\tGone\tstable\n"})
				r.commit()
			},
			code: 1,
			lines: []string{
				"api: api/marks.txt marks . Gone, which go doc does not find",
				"api: api/marks.txt marks 1 names other than experimental",
			},
		},
		{
			name: "a mark naming a name whose doc says Experimental",
			tree: func(r *repo) {
				r.write(map[string]string{
					"m.go":          "// Package m is a module the gate reads.\npackage m\n\n// A is one. Experimental: it may change.\nfunc A() int { return 1 }\n\n// B is two.\nfunc B() int { return 2 }\n",
					"api/marks.txt": "# The marks.\n.\tA\tstable\n.\tB\tstable\n",
				})
				r.commit()
			},
			code: 1,
			lines: []string{
				"api: api/marks.txt line '.\tA\tstable' marks . A, and its doc says 'A is one. Experimental: it may change.'",
				"api: api/marks.txt marks 2 names other than experimental",
			},
		},
		{
			name: "a marks line of two fields, one of four, and the mark experimental",
			tree: func(r *repo) {
				r.write(map[string]string{"api/marks.txt": "# The marks.\n.\tA\n.\tA\tstable\tmore\n.\tB\texperimental\n"})
				r.commit()
			},
			code: 1,
			lines: []string{
				"api: api/marks.txt line '.\tA' is not <package>, <name> and a mark other than experimental, tab-separated",
				"api: api/marks.txt line '.\tA\tstable\tmore' is not <package>, <name> and a mark other than experimental, tab-separated",
				"api: api/marks.txt line '.\tB\texperimental' is not <package>, <name> and a mark other than experimental, tab-separated",
				"api: api/marks.txt marks 3 names other than experimental",
			},
		},
		{
			name: "a shallow clone",
			tree: func(r *repo) {
				r.commit("v0.1.0")
				r.commit()
				clone := filepath.Join(r.t.TempDir(), "clone")
				r.git("clone", "--quiet", "--depth=1", "file:///"+strings.TrimPrefix(filepath.ToSlash(r.dir), "/"), clone)
				r.dir = clone
			},
			code:  1,
			lines: []string{"api: this clone is shallow, so its last tag is not known; fetch the whole history"},
		},
		{
			name: "git describe failing other than with no tag",
			tree: func(r *repo) {
				r.commit("v0.1.0")
				r.git("pack-refs", "--all")
				f, err := os.OpenFile(filepath.Join(r.dir, ".git", "packed-refs"), os.O_APPEND|os.O_WRONLY, 0)
				if err != nil {
					r.t.Fatal(err)
				}
				if _, err := f.WriteString("not a ref line\n"); err != nil {
					r.t.Fatal(err)
				}
				if err := f.Close(); err != nil {
					r.t.Fatal(err)
				}
			},
			code:  1,
			lines: []string{"api: git describe could not read the v* tags, so the breaks cannot be held"},
		},
		{
			name: "a module in a folder whose break since its own tag has no line",
			tree: func(r *repo) {
				subModule(r, "\n// Write writes.\nfunc Write() {}\n")
				r.commit("sub/v0.1.0")
				subModule(r, "")
				r.commit("v0.2.0")
			},
			folder: "sub",
			code:   1,
			lines: []string{
				"+- Write: removed",
				"api: the breaks since sub/v0.1.0 are not api/breaks/v0.1.0.txt (- the file, + apidiff); a commit that removes or changes an exported name adds apidiff's line there",
			},
		},
	})
}

// The gate is green on a tree its files describe, as on go-proc's own tree at its wave's head.
func TestTheGateIsGreenOnATreeItsFilesDescribe(t *testing.T) {
	t.Parallel()
	check(t, []gateRow{
		{
			name: "a module with no tag yet",
			tree: func(r *repo) { r.commit() },
			lines: []string{
				"api: the three systems' surfaces differ only as api/systems.txt says",
				"api: no tag yet, so no break is held; the systems and the marks are",
				"api: api/marks.txt marks 0 names other than experimental",
			},
		},
		{
			name: "a difference api/systems.txt lists",
			tree: func(r *repo) {
				r.write(map[string]string{
					"w_windows.go":    windowsOnly,
					"api/systems.txt": "# The lines by which the systems differ.\nwindows\tCompatible changes:\nwindows\t- WinOnly: added\n",
				})
				r.commit()
			},
			lines: []string{"api: the three systems' surfaces differ only as api/systems.txt says"},
		},
		{
			name: "a break api/breaks/<tag>.txt declares",
			tree: func(r *repo) {
				r.commit("v0.1.0")
				r.write(map[string]string{
					"m.go":                  "// Package m is a module the gate reads.\npackage m\n\n// A is one.\nfunc A() int { return 1 }\n",
					"api/breaks/v0.1.0.txt": "- B: removed\n",
				})
				r.commit()
			},
			lines: []string{"api: the breaks since v0.1.0 are the 1 lines of api/breaks/v0.1.0.txt"},
		},
		{
			name: "a new tag, with nothing edited",
			tree: func(r *repo) {
				r.commit("v0.1.0")
				r.write(map[string]string{
					"m.go":                  "// Package m is a module the gate reads.\npackage m\n\n// A is one.\nfunc A() int { return 1 }\n",
					"api/breaks/v0.1.0.txt": "- B: removed\n",
				})
				r.commit("v0.2.0")
			},
			lines: []string{"api: the breaks since v0.2.0 are the 0 lines of api/breaks/v0.2.0.txt"},
		},
		{
			name: "a mark go doc finds",
			tree: func(r *repo) {
				r.write(map[string]string{"api/marks.txt": "# The marks.\n.\tA\tstable\n"})
				r.commit()
			},
			lines: []string{"api: api/marks.txt marks 1 names other than experimental"},
		},
		{
			name: "a module in a folder, held to its own tags and its break file named by the version",
			tree: func(r *repo) {
				subModule(r, "\n// Write writes.\nfunc Write() {}\n")
				r.commit("sub/v0.1.0")
				subModule(r, "")
				r.write(map[string]string{
					"sub/api/breaks/v0.1.0.txt": "- Write: removed\n",
					"m.go":                      "// Package m is a module the gate reads.\npackage m\n\n// A is one.\nfunc A() int { return 1 }\n",
				})
				r.commit("v0.2.0")
			},
			folder: "sub",
			lines: []string{
				"api: the three systems' surfaces differ only as api/systems.txt says",
				"api: the breaks since sub/v0.1.0 are the 1 lines of api/breaks/v0.1.0.txt",
			},
		},
	})
}

// onlyA is m.go with B removed, the break the -before-head rows make.
const onlyA = "// Package m is a module the gate reads.\npackage m\n\n// A is one.\nfunc A() int { return 1 }\n"

// beforeHead is the -before-head rows' argument.
var beforeHead = []string{"-before-head"} //nolint:gochecknoglobals // the rows' one argument

// With -before-head the gate's base leaves out the tags on the head: it is the newest tag that is
// an ancestor of the head and does not point at it, so a gate run at a tag holds the breaks since
// the tag before. Without it, the same head reads as before.
func TestTheGateBeforeTheHeadTakesTheNewestTagThatIsNotOnTheHead(t *testing.T) {
	t.Parallel()
	check(t, []gateRow{
		{
			name: "a head with its own tag, whose break since the tag before has no line",
			tree: func(r *repo) {
				r.commit("v0.1.0")
				r.write(map[string]string{"m.go": onlyA})
				r.commit("v0.2.0")
			},
			args: beforeHead,
			code: 1,
			lines: []string{
				"+- B: removed",
				"api: the breaks since v0.1.0 are not api/breaks/v0.1.0.txt (- the file, + apidiff); a commit that removes or changes an exported name adds apidiff's line there",
			},
		},
		{
			name: "a head with its own tag, whose break since the tag before has its line",
			tree: func(r *repo) {
				r.commit("v0.1.0")
				r.write(map[string]string{"m.go": onlyA, "api/breaks/v0.1.0.txt": "- B: removed\n"})
				r.commit("v0.2.0")
			},
			args:  beforeHead,
			lines: []string{"api: the breaks since v0.1.0 are the 1 lines of api/breaks/v0.1.0.txt"},
		},
		{
			name: "the same head without -before-head, read as before",
			tree: func(r *repo) {
				r.commit("v0.1.0")
				r.write(map[string]string{"m.go": onlyA})
				r.commit("v0.2.0")
			},
			lines: []string{"api: the breaks since v0.2.0 are the 0 lines of api/breaks/v0.2.0.txt"},
		},
		{
			name: "a head with two tags",
			tree: func(r *repo) {
				r.commit("v0.1.0")
				r.write(map[string]string{"m.go": onlyA, "api/breaks/v0.1.0.txt": "- B: removed\n"})
				r.commit("v0.2.0", "v0.2.1")
			},
			args:  beforeHead,
			lines: []string{"api: the breaks since v0.1.0 are the 1 lines of api/breaks/v0.1.0.txt"},
		},
		{
			name: "a head with no tag",
			tree: func(r *repo) {
				r.commit("v0.1.0")
				r.write(map[string]string{"m.go": onlyA, "api/breaks/v0.1.0.txt": "- B: removed\n"})
				r.commit()
			},
			args:  beforeHead,
			lines: []string{"api: the breaks since v0.1.0 are the 1 lines of api/breaks/v0.1.0.txt"},
		},
		{
			name: "a head whose only tag is its own",
			tree: func(r *repo) {
				r.commit()
				r.write(map[string]string{"m.go": onlyA})
				r.commit("v0.1.0")
			},
			args:  beforeHead,
			lines: []string{"api: no v* tag before the head's own, so no break is held; the systems and the marks are"},
		},
		{
			name:  "a first commit with its own tag",
			tree:  func(r *repo) { r.commit("v0.1.0") },
			args:  beforeHead,
			lines: []string{"api: no v* tag before the head's own, so no break is held; the systems and the marks are"},
		},
		{
			name: "a tagged merge whose parents reach two tags, the nearer the base",
			tree: func(r *repo) {
				r.commit("v0.1.0")
				r.git("checkout", "--quiet", "-b", "side")
				r.write(map[string]string{"m.go": onlyA, "api/breaks/v0.1.0.txt": "- B: removed\n"})
				r.commit("v0.2.0")
				r.git("checkout", "--quiet", "main")
				r.write(map[string]string{"other.txt": "other\n"})
				r.commit()
				r.git("merge", "--quiet", "--no-ff", "-m", "merged", "side")
				r.git("tag", "v0.3.0")
			},
			args:  beforeHead,
			lines: []string{"api: the breaks since v0.2.0 are the 0 lines of api/breaks/v0.2.0.txt"},
		},
		{
			name: "a module in a folder at its own tag, held to its tag before",
			tree: func(r *repo) {
				subModule(r, "\n// Write writes.\nfunc Write() {}\n")
				r.commit("sub/v0.1.0")
				subModule(r, "")
				r.write(map[string]string{"sub/api/breaks/v0.1.0.txt": "- Write: removed\n"})
				r.commit("sub/v0.2.0", "v0.2.0")
			},
			folder: "sub",
			args:   beforeHead,
			lines:  []string{"api: the breaks since sub/v0.1.0 are the 1 lines of api/breaks/v0.1.0.txt"},
		},
		{
			name: "git describe failing at a parent other than with no tag",
			tree: func(r *repo) {
				r.commit("v0.1.0")
				r.commit()
				r.commit("v0.2.0")
				// The parent's object goes: git rev-parse reads the parents from the head alone, and
				// git describe fails at the parent. Git makes the file read-only, which Windows will
				// not remove.
				parent := strings.TrimSpace(r.git("rev-parse", "HEAD^"))
				object := filepath.Join(r.dir, ".git", "objects", parent[:2], parent[2:])
				if err := os.Chmod(object, 0o600); err != nil {
					r.t.Fatal(err)
				}
				if err := os.Remove(object); err != nil {
					r.t.Fatal(err)
				}
			},
			args:  beforeHead,
			code:  1,
			lines: []string{"api: git describe could not read the v* tags, so the breaks cannot be held"},
		},
		{
			name: "git failing to read the head's parents",
			tree: func(r *repo) {
				r.commit("v0.1.0")
				r.commit("v0.2.0")
				r.git("pack-refs", "--all")
				f, err := os.OpenFile(filepath.Join(r.dir, ".git", "packed-refs"), os.O_APPEND|os.O_WRONLY, 0)
				if err != nil {
					r.t.Fatal(err)
				}
				if _, err := f.WriteString("not a ref line\n"); err != nil {
					r.t.Fatal(err)
				}
				if err := f.Close(); err != nil {
					r.t.Fatal(err)
				}
			},
			args:  beforeHead,
			code:  1,
			lines: []string{"api: git cannot read the head's parents, so the breaks cannot be held"},
		},
	})
}

// The gate takes -dir and -before-head alone, and exits 2 for anything else, before it reads a file.
func TestTheGateTakesDirAndBeforeHeadAloneAndExitsTwoOtherwise(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"extra"}, {"-tags", "x"}, {"-dir", ".", "extra"}, {"-before-head=x"}, {"-before-head", "extra"}} {
		var out, errOut bytes.Buffer
		if code := run(t.Context(), args, &out, &errOut); code != 2 || out.Len() != 0 {
			t.Errorf("apigate %q: exit %d with %q, want 2 and nothing on stdout", args, code, out.String())
		}
	}
}
