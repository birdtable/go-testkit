package testkit

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

// progWhere prints the folder it runs in on its first line, then its environment, one NAME=value a
// line, so a test reads both the folder and the variables a run was handed.
const progWhere = `package main

import (
	"os"
	"strings"
)

func main() {
	dir, err := os.Getwd()
	if err != nil {
		os.Stderr.WriteString(err.Error())
		os.Exit(1)
	}
	os.Stdout.WriteString(dir + "\n" + strings.Join(os.Environ(), "\n"))
}
`

// where is the folder and the environment a progWhere run printed. A name is matched with no
// regard to case on Windows, as the system reads one, so each name holds every entry of it.
type where struct {
	dir string
	env map[string][]string
}

// readWhere reads a progWhere run's stdout, failing t when it did not exit 0.
func readWhere(t *testing.T, r Result) where {
	t.Helper()
	if r.Code != 0 {
		t.Fatalf("the child: code %d, stdout %q, stderr %q", r.Code, r.Stdout, r.Stderr)
	}
	dir, rest, _ := strings.Cut(r.Stdout, "\n")
	w := where{dir: dir, env: map[string][]string{}}
	for line := range strings.SplitSeq(rest, "\n") {
		if name, value, ok := strings.Cut(line, "="); ok {
			w.env[envName(name)] = append(w.env[envName(name)], value)
		}
	}
	return w
}

// envName is name as the system compares one: folded to upper case where names fold, on Windows.
func envName(name string) string {
	if namesFoldHere {
		return strings.ToUpper(name)
	}
	return name
}

// sameDir fails t unless got and want are one folder, which the child may spell otherwise (a link
// such as macOS's /var, or a short name on Windows), so they are compared by the file each opens.
func sameDir(t *testing.T, what, got, want string) {
	t.Helper()
	g, err := os.Stat(got)
	if err != nil {
		t.Fatalf("%s: the child's folder %q: %v", what, got, err)
	}
	w, err := os.Stat(want)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(g, w) {
		t.Errorf("%s ran in %q, and want %q", what, got, want)
	}
}

// startWhere runs b's progWhere by Start under parent and is its Result.
func startWhere(t *testing.T, parent context.Context, b *Binary) Result {
	t.Helper()
	ctx, cancel := context.WithTimeout(parent, time.Hour)
	defer cancel()
	r, err := b.Start(ctx)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	return r.Wait()
}

// TestDirAndEnvGiveEachCopyItsOwnFolderAndVariables: two parallel subtests take a
// copy of one Binary by Dir and Env, each with a folder and a value of its own, and each run, by
// Run and by Start, sees only its own; the copies' Path is the receiver's, so nothing is built again;
// and the receiver, run in the test's Cleanup once both have ended, still runs in the test's folder
// without the variable.
//
// What it costs when this fails: a test under t.Parallel runs its program in another test's folder
// or with its variables, or a copy changes the Binary every other test shares, or each Dir and Env
// builds the program again.
func TestDirAndEnvGiveEachCopyItsOwnFolderAndVariables(t *testing.T) {
	t.Parallel()
	b := Build(plantProgram(t, progWhere))
	if b.Path() == "" {
		t.Fatalf("the build failed: %s", b.Run().Stderr)
	}
	here, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	ran := 0
	// A parent's Cleanup runs once its parallel subtests have ended, and after its Context is done.
	t.Cleanup(func() {
		if ran != 2 {
			t.Fatalf("started %d of the 2 copies", ran)
		}
		for how, r := range map[string]Result{"Run": b.Run(), "Start": startWhere(t, context.Background(), b)} {
			w := readWhere(t, r)
			sameDir(t, "the receiver's "+how, w.dir, here)
			if got, ok := w.env["X_WHO"]; ok {
				t.Errorf("the receiver's %s has X_WHO %q, which only its copies were given", how, got)
			}
		}
	})
	for _, who := range []string{"a", "b"} {
		t.Run(who, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			c := b.Dir(dir).Env("X_WHO=" + who)
			if c.Path() != b.Path() {
				t.Errorf("the copy's Path is %q, and want the receiver's %q: the same build", c.Path(), b.Path())
			}
			for how, r := range map[string]Result{"Run": c.Run(), "Start": startWhere(t, t.Context(), c)} {
				w := readWhere(t, r)
				sameDir(t, how+" of "+who, w.dir, dir)
				if got := w.env["X_WHO"]; len(got) != 1 || got[0] != who {
					t.Errorf("%s of %s: X_WHO is %q, and want only %q", how, who, got, who)
				}
			}
		})
		ran++
	}
}

// TestEnvOverridesTheCleanValue: a name the clean environment sets, GOPATH as Isolate
// pins it, given again by Env reaches the run once, with Env's value, by Run and by Start; on
// Windows it is given in lower case, which the system reads as the same name. A second Env adds to
// the first, and the last of a name wins.
//
// What it costs when this fails: a test cannot point its program at a folder or setting of its own
// where the clean environment already names one, or the program sees both values and reads either.
func TestEnvOverridesTheCleanValue(t *testing.T) {
	t.Parallel()
	b := Build(plantProgram(t, progWhere))
	clean := readWhere(t, b.Run()).env["GOPATH"]
	if len(clean) != 1 || clean[0] == "" {
		t.Fatalf("the clean environment's GOPATH is %q, and the test wants one value to override", clean)
	}
	name := "GOPATH"
	if namesFoldHere {
		name = "gopath"
	}
	c := b.Env(name+"=first", "X_OTHER=o").Env(name + "=over")
	for how, r := range map[string]Result{"Run": c.Run(), "Start": startWhere(t, t.Context(), c)} {
		w := readWhere(t, r)
		if got := w.env["GOPATH"]; len(got) != 1 || got[0] != "over" {
			t.Errorf("%s: GOPATH is %q, and want only %q over the clean %q", how, got, "over", clean[0])
		}
		if got := w.env["X_OTHER"]; len(got) != 1 || got[0] != "o" {
			t.Errorf("%s: X_OTHER is %q, and want %q from the first Env", how, got, "o")
		}
	}
}

// TestEnvRefusesAnEntryWithoutEquals: Env with an entry that holds no = gives a copy
// whose Run is code -1 with the reason on Stderr and whose Start is an ErrRefused, as is every copy
// made from it; its Path is still the build's, and the receiver still runs.
//
// What it costs when this fails: a test that wrote Env("NAME") for NAME=value runs its program
// without the variable it meant to set, and passes or fails on that.
func TestEnvRefusesAnEntryWithoutEquals(t *testing.T) {
	t.Parallel()
	b := Build(plantProgram(t, progWhere))
	bad := b.Env("X_OK=1", "X_NO_VALUE")
	for name, c := range map[string]*Binary{"the copy": bad, "a copy of it": bad.Env("X_LATER=1").Dir(t.TempDir())} {
		if r := c.Run(); r.Code != -1 || !strings.Contains(r.Stderr, `"X_NO_VALUE", which holds no =`) {
			t.Errorf("%s: Run gave code %d, stderr %q, and want -1 with the refusal", name, r.Code, r.Stderr)
		}
		ctx, cancel := context.WithTimeout(t.Context(), time.Hour)
		r, err := c.Start(ctx)
		cancel()
		if !errors.Is(err, ErrRefused) || r != nil {
			t.Errorf("%s: Start gave %v, and want an ErrRefused and no program", name, err)
		}
		if c.Path() == "" || c.Path() != b.Path() {
			t.Errorf("%s: Path is %q, and want the build's %q", name, c.Path(), b.Path())
		}
	}
	readWhere(t, b.Run())
}

// TestEnvDropsAnEntryWithNoName: Env with an entry that has no name, =v, gives a copy
// that runs, by Run and by Start, with Env's other entries and without the nameless one, as Clean
// drops one.
//
// What it costs when this fails: a nameless entry reaches exec, which hands the child a variable
// no program can name, or a test that passes Env(os.Environ()...) on Windows, where the system keeps
// =C:=C:\ entries, has its copy refused.
func TestEnvDropsAnEntryWithNoName(t *testing.T) {
	t.Parallel()
	c := Build(plantProgram(t, progWhere)).Env("=v", "X_OK=1")
	for how, r := range map[string]Result{"Run": c.Run(), "Start": startWhere(t, t.Context(), c)} {
		w := readWhere(t, r)
		if got := w.env["X_OK"]; len(got) != 1 || got[0] != "1" {
			t.Errorf("%s: X_OK is %q, and want %q", how, got, "1")
		}
		if slices.Contains(w.env[""], "v") {
			t.Errorf("%s: the run was handed the nameless entry =v", how)
		}
	}
}
