// Command apigate is the API gate for one module: go-proc's gate/api.sh, in Go,
// so that a library's task api runs it with one go run line and no shell around it, and a fix to it is
// made and proved once. From the module's folder, or from the repository's top with -dir naming it:
//
//	go run github.com/birdtable/go-testkit/cmd/apigate
//	go run ./cmd/apigate -dir taskfile
//
// apidiff, built from the repository's tools/apidiff.mod, writes the module's export data once per
// system, darwin, linux and windows, all from this host with GOOS set and cgo off. Three things are then
// held, and each that fails says so; the gate is red if any fails:
//
//  1. The systems alike. apidiff's full report from darwin to linux and from darwin to windows, each
//     line led by the system and a tab, equals api/systems.txt less its # lines. The full report, not
//     -incompatible: a name added on one system is "compatible".
//  2. The breaks since the last tag. The base is the last tag git describe finds that matches v*, or
//     <folder>/v* for a module in a folder of its own. With -before-head, for a gate that runs at the
//     tag, the base leaves out the tags on the head: it is the newest such tag that is an ancestor of
//     the head and does not point at it, the run at v0.2.0 holding the breaks since v0.1.0, and a
//     head whose only tags are its own has none, as with no tag. The base's tree is written out of the
//     repository by git checkout-index, through an index of the gate's own, and read at the module's
//     folder with GOWORK=off. apidiff -incompatible from the base's export data to the head's, per
//     system, the three joined, sorted bytewise and each line once, equals api/breaks/<version>.txt
//     byte for byte (the base's version alone in the name), or nothing where that file is not there.
//     So a break with no line is red, and so is a line for a break the tree does not make. Without
//     -before-head, at a new tag the base moves, no file is there for it, and the same tree is green
//     with nothing edited.
//  3. The marks. A name api/marks.txt does not list is experimental. Each line it holds is
//     <package><tab><name><tab><mark>, the mark a word that is not experimental, and go doc must find
//     the name in the package and print no line holding Experimental, which the name's doc would then
//     say against its mark: the line and that doc line are named.
//
// apidiff exits 0 on a break, so its output decides, and it fails on a tree that does not build: every
// apidiff that fails stops the gate red, never an empty report. Only git's own answers that no tag is
// there ("No names found", "No tags can describe") let check 2 pass with a line that says so; any other
// failure of git describe stops the gate red, and a shallow clone is red, as its last tag may be one it
// never fetched.
//
// The exit code is 0 when all three hold, 1 when one does not or the gate could not finish, and 2 for
// arguments it does not take.
//
// It is built on apidiff and git describe, and adds what neither does alone: one gate for the three
// systems from one host, held to files in the repository. The repository configures it by
// api/systems.txt, api/breaks/<version>.txt and api/marks.txt, by -dir for a module in a folder
// of its own, and by -before-head for a gate that runs at its own tag.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}

// systems are the systems the gate reads, darwin first: the other two are compared with it.
var systems = []string{"darwin", "linux", "windows"} //nolint:gochecknoglobals // the three systems the gate reads

// errStop ends the gate red once it has said why.
var errStop = errors.New("apigate: stopped")

// run is the command: args are its arguments, and the gate's lines go to stdout, as go-proc's gate
// writes them. go's and git's own error output goes to stderr where that gate lets it through.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("apigate", flag.ContinueOnError)
	set.SetOutput(stderr)
	dir := set.String("dir", ".", "the module's `folder`: the repository's top, or a folder below it with a go.mod of its own, whose tags are <folder>/v*")
	beforeHead := set.Bool("before-head", false, "leave the head's own tags out of check 2's base, for a gate that runs at the tag: the base is the newest tag that is an ancestor of the head and does not point at it")
	if err := set.Parse(args); err != nil {
		return 2
	}
	if set.NArg() != 0 {
		if _, err := fmt.Fprintf(stderr, "apigate: takes no arguments but -dir and -before-head, and was given %q\n", set.Args()); err != nil {
			return 2
		}
		return 2
	}
	g := &gate{ctx: ctx, out: stdout, errOut: stderr, dir: *dir, beforeHead: *beforeHead}
	red, err := g.run()
	if err != nil || red || g.werr != nil {
		return 1
	}
	return 0
}

// gate is one run of the gate over one module.
type gate struct {
	ctx    context.Context
	out    io.Writer
	errOut io.Writer
	werr   error // the first write to out that failed

	dir    string // the module's folder
	top    string // the repository's top
	folder string // the module's folder below top, slash-separated and ending in /, or "" at the top
	bin    string // apidiff
	tmp    string // the gate's own folder, removed when it ends

	beforeHead bool // check 2's base leaves out the tags on the head
}

// say writes one line of the gate's.
func (g *gate) say(format string, args ...any) {
	g.write([]byte(fmt.Sprintf(format, args...) + "\n"))
}

// write writes b as it is, as go-proc's gate cats a program's error output.
func (g *gate) write(b []byte) {
	if _, err := g.out.Write(b); err != nil && g.werr == nil {
		g.werr = err
	}
}

// command is name with args, to run in dir, with env set over the gate's own environment.
func (g *gate) command(dir string, env []string, name string, args ...string) *exec.Cmd {
	c := exec.CommandContext(g.ctx, name, args...) //nolint:gosec // G204: apidiff, go and git, with the gate's own arguments
	c.Dir = dir
	c.Env = append(os.Environ(), env...)
	return c
}

// run is the gate: red is true when a check does not hold, and err is errStop when the gate could not
// finish, its reason said.
func (g *gate) run() (red bool, err error) {
	if err := g.find(); err != nil {
		return false, err
	}
	tmp, err := os.MkdirTemp("", "apigate-")
	if err != nil {
		g.say("api: the gate's own folder could not be made: %v", err)
		return false, errStop
	}
	g.tmp = tmp
	defer func() {
		if err := os.RemoveAll(tmp); err != nil {
			g.say("api: the gate's own folder %s could not be removed: %v", tmp, err)
		}
	}()
	if err := os.WriteFile(g.path("none"), nil, 0o600); err != nil {
		g.say("api: the gate's own folder could not be written: %v", err)
		return false, errStop
	}
	for _, goos := range systems {
		if !g.exportData(g.dir, goos, goos+".exp", nil) {
			g.say("api: apidiff cannot write the module's export data for %s, so nothing is held", goos)
			return false, errStop
		}
	}
	red, err = g.systems()
	if err != nil {
		return false, err
	}
	breaksRed, err := g.breaks()
	if err != nil {
		return false, err
	}
	marksRed, err := g.marks()
	if err != nil {
		return false, err
	}
	return red || breaksRed || marksRed, nil
}

// find sets the repository's top, the module's folder below it, and apidiff, built from
// tools/apidiff.mod at the top.
func (g *gate) find() error {
	c := g.command(g.dir, nil, "git", "rev-parse", "--show-cdup", "--show-prefix")
	c.Stderr = g.errOut
	b, err := c.Output()
	parts := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	if err != nil || len(parts) != 2 {
		g.say("api: git cannot read the repository %s is in, so no tag can be found", g.dir)
		return errStop
	}
	g.top = filepath.Join(g.dir, filepath.FromSlash(parts[0]))
	g.folder = parts[1]
	c = g.command(g.top, []string{"CGO_ENABLED=0", "GOWORK=off"}, "go", "tool", "-modfile="+filepath.Join("tools", "apidiff.mod"), "-n", "apidiff")
	c.Stderr = g.errOut
	b, err = c.Output()
	if err != nil {
		g.say("api: apidiff could not be built from tools/apidiff.mod")
		return errStop
	}
	g.bin = strings.TrimSpace(string(b))
	return nil
}

// path is name in the gate's own folder.
func (g *gate) path(name string) string {
	return filepath.Join(g.tmp, name)
}

// exportData has apidiff write the export data of the module in dir for goos to the gate's file
// named name, with env set too. On a failure it writes apidiff's error output and is false.
func (g *gate) exportData(dir, goos, name string, env []string) bool {
	var e bytes.Buffer
	c := g.command(dir, append([]string{"GOOS=" + goos, "CGO_ENABLED=0"}, env...), g.bin, "-m", "-w", g.path(name), ".")
	c.Stdout, c.Stderr = g.out, &e
	if err := c.Run(); err != nil {
		g.write(e.Bytes())
		return false
	}
	return true
}

// report is apidiff's report from the export data from to the export data to, the gate's files, with
// -incompatible when incompatible is true. apidiff's error output is not shown, as in go-proc's gate.
func (g *gate) report(from, to string, incompatible bool) ([]byte, error) {
	args := []string{"-m"}
	if incompatible {
		args = append(args, "-incompatible")
	}
	return g.command(g.dir, nil, g.bin, append(args, g.path(from), g.path(to))...).Output()
}

// records are b's lines as awk and sort read them: none for nothing, and a last line read whether a
// newline ends it or not.
func records(b []byte) []string {
	if len(b) == 0 {
		return nil
	}
	return strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
}

// diff shows git's diff of the files a and b, the lines a has as -, b's as +.
func (g *gate) diff(a, b string) {
	c := g.command(g.dir, nil, "git", "--no-pager", "diff", "--no-index", "--", a, b)
	c.Stdout, c.Stderr = g.out, g.errOut
	// git exits 1 where the files differ, which the caller has seen; any other failure is said.
	if err := c.Run(); err != nil {
		if exit, ok := errors.AsType[*exec.ExitError](err); !ok || exit.ExitCode() != 1 {
			g.say("api: git could not show how %s and %s differ: %v", a, b, err)
		}
	}
}

// keep writes b to the gate's file named name.
func (g *gate) keep(name string, b []byte) error {
	if err := os.WriteFile(g.path(name), b, 0o600); err != nil {
		g.say("api: the gate's own folder could not be written: %v", err)
		return errStop
	}
	return nil
}

// systems is check 1: true when the systems differ other than api/systems.txt says.
func (g *gate) systems() (bool, error) {
	var got bytes.Buffer
	for _, goos := range systems[1:] {
		one, err := g.report("darwin.exp", goos+".exp", false)
		if err != nil {
			g.say("api: apidiff cannot compare darwin with %s", goos)
			return false, errStop
		}
		for _, line := range records(one) {
			got.WriteString(goos + "\t" + line + "\n")
		}
	}
	file, err := os.ReadFile(filepath.Join(g.dir, "api", "systems.txt"))
	if err != nil {
		g.say("api: api/systems.txt cannot be read: %v", err)
		return false, errStop
	}
	var want bytes.Buffer
	for _, line := range records(file) {
		if !strings.HasPrefix(line, "#") {
			want.WriteString(line + "\n")
		}
	}
	if bytes.Equal(want.Bytes(), got.Bytes()) {
		g.say("api: the three systems' surfaces differ only as api/systems.txt says")
		return false, nil
	}
	if err := g.keep("systems.want", want.Bytes()); err != nil {
		return false, err
	}
	if err := g.keep("systems", got.Bytes()); err != nil {
		return false, err
	}
	g.diff(g.path("systems.want"), g.path("systems"))
	g.say("api: the systems' surfaces are not what api/systems.txt says (- the file, + apidiff from darwin to each system)")
	return true, nil
}

// breaks is check 2: true when the breaks since the last tag are not its file's.
func (g *gate) breaks() (bool, error) {
	c := g.command(g.dir, nil, "git", "rev-parse", "--is-shallow-repository")
	c.Stderr = g.errOut
	if b, err := c.Output(); err != nil || strings.TrimSpace(string(b)) != "false" {
		g.say("api: this clone is shallow, so its last tag is not known; fetch the whole history")
		return true, nil
	}
	base, err := g.base()
	if err != nil || base == "" {
		return false, err
	}
	if !g.writeBase(base) {
		return false, errStop
	}
	old := filepath.Join(g.path("old"), filepath.FromSlash(g.folder))
	var got []string
	for _, goos := range systems {
		if !g.exportData(old, goos, "old-"+goos+".exp", []string{"GOWORK=off"}) {
			g.say("api: apidiff cannot write %s's export data for %s", base, goos)
			return false, errStop
		}
		one, err := g.report("old-"+goos+".exp", goos+".exp", true)
		if err != nil {
			g.say("api: apidiff cannot compare %s with the tree for %s", base, goos)
			return false, errStop
		}
		got = append(got, records(one)...)
	}
	slices.Sort(got)
	got = slices.Compact(got)
	var gotFile bytes.Buffer
	for _, line := range got {
		gotFile.WriteString(line + "\n")
	}
	version := base[strings.LastIndex(base, "/")+1:]
	declared := "api/breaks/" + version + ".txt"
	// git runs in the module's folder, so the diff names the file as go-proc's gate does.
	wantPath := filepath.FromSlash(declared)
	want, err := os.ReadFile(filepath.Join(g.dir, wantPath)) //nolint:gosec // G304: the module's own break file, named by its last tag
	if errors.Is(err, fs.ErrNotExist) {
		want, wantPath, err = nil, g.path("none"), nil
	}
	if err != nil {
		g.say("api: %s cannot be read: %v", declared, err)
		return false, errStop
	}
	if bytes.Equal(want, gotFile.Bytes()) {
		g.say("api: the breaks since %s are the %d lines of %s", base, len(got), declared)
		return false, nil
	}
	if err := g.keep("breaks.got", gotFile.Bytes()); err != nil {
		return false, err
	}
	g.diff(wantPath, g.path("breaks.got"))
	g.say("api: the breaks since %s are not %s (- the file, + apidiff); a commit that removes or changes an exported name adds apidiff's line there", base, declared)
	return true, nil
}

// base is check 2's base: the last tag git describe finds from the head that matches the module's
// tags, or with -before-head the newest of those that git describe finds from the head's parents,
// newest being fewest commits from it to the head. A tag that points at the head is no parent's
// ancestor, so the head's own tags are left out with no list of them. base is "" where there is no
// such tag, the line that says so said; err is errStop where git could not tell, its reason said.
func (g *gate) base() (string, error) {
	tags := g.folder + "v*"
	if !g.beforeHead {
		base, ok := g.describe(tags, "HEAD")
		if ok && base == "" {
			g.say("api: no tag yet, so no break is held; the systems and the marks are")
		}
		return base, stopUnless(ok)
	}
	c := g.command(g.dir, nil, "git", "rev-parse", "HEAD^@")
	c.Stderr = g.errOut
	b, err := c.Output()
	if err != nil {
		g.say("api: git cannot read the head's parents, so the breaks cannot be held")
		return "", errStop
	}
	base, nearest := "", -1
	for _, parent := range strings.Fields(string(b)) {
		tag, ok := g.describe(tags, parent)
		if !ok {
			return "", errStop
		}
		if tag == "" || tag == base {
			continue
		}
		n, ok := g.commitsSince(tag)
		if !ok {
			return "", errStop
		}
		if nearest < 0 || n < nearest {
			base, nearest = tag, n
		}
	}
	if base == "" {
		g.say("api: no %s tag before the head's own, so no break is held; the systems and the marks are", tags)
	}
	return base, nil
}

// stopUnless is errStop unless ok.
func stopUnless(ok bool) error {
	if ok {
		return nil
	}
	return errStop
}

// describe is the last tag matching tags that git describe finds from the commit at, or "" where git
// answers that there is none. ok is false where git fails otherwise, its error output and a line
// said.
func (g *gate) describe(tags, at string) (tag string, ok bool) {
	var e bytes.Buffer
	c := g.command(g.dir, nil, "git", "describe", "--tags", "--abbrev=0", "--match", tags, at)
	c.Stderr = &e
	b, err := c.Output()
	if err == nil {
		return strings.TrimSpace(string(b)), true
	}
	if bytes.Contains(e.Bytes(), []byte("No names found")) || bytes.Contains(e.Bytes(), []byte("No tags can describe")) {
		return "", true
	}
	g.write(e.Bytes())
	g.say("api: git describe could not read the %s tags, so the breaks cannot be held", tags)
	return "", false
}

// commitsSince is how many commits the head has that the tag does not.
func (g *gate) commitsSince(tag string) (int, bool) {
	c := g.command(g.dir, nil, "git", "rev-list", "--count", tag+"..HEAD")
	c.Stderr = g.errOut
	b, err := c.Output()
	n, convErr := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || convErr != nil {
		g.say("api: git cannot count the commits since %s, so the breaks cannot be held", tag)
		return 0, false
	}
	return n, true
}

// writeBase writes the tree of the tag base into the gate's folder old, through an index of the gate's
// own, so the repository's own index and files are not touched. git's error output is let through.
func (g *gate) writeBase(base string) bool {
	env := []string{"GIT_INDEX_FILE=" + g.path("index")}
	for _, args := range [][]string{
		{"read-tree", base},
		{"checkout-index", "-a", "--prefix=" + filepath.ToSlash(g.path("old")) + "/"},
	} {
		c := g.command(g.top, env, "git", args...)
		c.Stdout, c.Stderr = g.out, g.errOut
		if err := c.Run(); err != nil {
			return false
		}
	}
	return true
}

// marks is check 3: true when a line of api/marks.txt is not a package, a name and a mark, or names a
// name go doc does not find or whose doc says Experimental.
func (g *gate) marks() (bool, error) {
	file, err := os.ReadFile(filepath.Join(g.dir, "api", "marks.txt"))
	if err != nil {
		g.say("api: api/marks.txt cannot be read: %v", err)
		return false, errStop
	}
	red, n := false, 0
	for _, line := range records(file) {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		n++
		f := strings.FieldsFunc(line, func(r rune) bool { return r == '\t' })
		if len(f) != 3 || f[2] == "experimental" {
			g.say("api: api/marks.txt line '%s' is not <package>, <name> and a mark other than experimental, tab-separated", line)
			red = true
			continue
		}
		doc, err := g.command(g.dir, []string{"CGO_ENABLED=0"}, "go", "doc", "./"+f[0], f[1]).Output()
		if err != nil {
			g.say("api: api/marks.txt marks %s %s, which go doc does not find", f[0], f[1])
			red = true
			continue
		}
		said := strings.Split(string(doc), "\n")
		if i := slices.IndexFunc(said, func(l string) bool { return strings.Contains(l, "Experimental") }); i >= 0 {
			g.say("api: api/marks.txt line '%s' marks %s %s, and its doc says '%s'", line, f[0], f[1], strings.TrimSpace(said[i]))
			red = true
		}
	}
	g.say("api: api/marks.txt marks %d names other than experimental", n)
	return red, nil
}
