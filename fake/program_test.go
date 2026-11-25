package fake

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec" //nolint:depguard // the rows start each stand-in as a child by its path, as a product's test does
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/birdtable/go-testkit"
)

// TestMain runs the rows under testkit's Main, which Program's Build is for.
func TestMain(m *testing.M) {
	code, err := testkit.Main(m)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fake: TestMain:", err) //nolint:forbidigo // cli.errors.3: the test binary's own error, on its stderr
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}

// childLimit bounds every child a row starts, so a stand-in that does not end fails its row and not
// the whole run.
const childLimit = time.Minute

// A ran is what one start of a stand-in left behind.
type ran struct {
	code        int
	stdout      string
	stderr      string
	pid         int
	took        time.Duration
	startedWith []string
}

// start runs the program at path as a child with args, stdin and the test's environment with env
// laid over it, in the folder dir when it is not empty, and is what it did. A child that could not
// be started, or did not end within childLimit, fails t.
func start(t *testing.T, path, dir, stdin string, env []string, args ...string) ran {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), childLimit)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...) //nolint:gosec // G204: the stand-in this row placed, by its path
	cmd.Dir = dir
	cmd.Env = append(cmd.Environ(), env...)
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	began := time.Now()
	err := cmd.Run()
	took := time.Since(began)
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		t.Fatalf("starting %s: %v", path, err)
	}
	if ctx.Err() != nil {
		t.Fatalf("%s did not end within %s:\nstdout %q\nstderr %q", path, childLimit, stdout.String(), stderr.String())
	}
	return ran{code: cmd.ProcessState.ExitCode(), stdout: stdout.String(), stderr: stderr.String(),
		pid: cmd.Process.Pid, took: took, startedWith: args}
}

// read is the text of the file at path, and fails t when it cannot be read.
func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading what the stand-in wrote: %v", err)
	}
	return string(b)
}

// A stepRow is one step, or a short list built round it, started as a child and read.
type stepRow struct {
	name  string
	steps func(dir string) []Step
	args  []string
	stdin string
	env   []string
	check func(t *testing.T, dir string, r ran)
}

// want fails t when r is not code with exactly stdout and stderr.
func want(t *testing.T, r ran, code int, stdout, stderr string) {
	t.Helper()
	if r.code != code || r.stdout != stdout || r.stderr != stderr {
		t.Errorf("code %d, stdout %q, stderr %q; want code %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr, code, stdout, stderr)
	}
}

// Each step does its work in a stand-in started as a child, on every system: what it wrote to a
// stream or a file, the code it ended with, and the steps after it that it left undone.
//
// What it costs when this fails: a test that plants a stand-in for git, go or a product's own tool
// reads a stand-in that did not do what its steps say, and passes or fails on that.
func TestEachStepDoesItsWorkInAStandInStartedAsAChild(t *testing.T) {
	t.Parallel()
	// other is a second stand-in for the Run and Exec rows: it logs its arguments, says so on both
	// streams and ends with the code its first argument names.
	otherSteps := func(dir string) []Step {
		log := filepath.Join(dir, "other.log")
		return []Step{AppendArgs(log), Out("other out\n"), Err("other err\n"),
			When("4 *", Exit(4)), When("5 *", Exit(5)), When("0 *", Exit(0))}
	}
	rows := []stepRow{
		{name: "Out, Err and OutBytes", steps: func(string) []Step {
			return []Step{Out("one\n"), Err("two\n"), OutBytes([]byte{0, 0xff, '\n'})}
		}, check: func(t *testing.T, _ string, r ran) { t.Helper(); want(t, r, 0, "one\n\x00\xff\n", "two\n") }},
		{name: "Exit ends it and leaves the steps after it", steps: func(string) []Step {
			return []Step{Out("before\n"), Exit(3), Out("after\n")}
		}, check: func(t *testing.T, _ string, r ran) { t.Helper(); want(t, r, 3, "before\n", "") }},
		{name: "no step left: code 0", steps: func(string) []Step { return nil },
			check: func(t *testing.T, _ string, r ran) { t.Helper(); want(t, r, 0, "", "") }},
		{name: "Sleep waits", steps: func(string) []Step { return []Step{Sleep(200 * time.Millisecond), Out("woke\n")} },
			check: func(t *testing.T, _ string, r ran) {
				t.Helper()
				want(t, r, 0, "woke\n", "")
				if r.took < 200*time.Millisecond {
					t.Errorf("the run took %s, less than the Sleep of 200ms", r.took)
				}
			}},
		{name: "WriteFile replaces what the file held", steps: func(dir string) []Step {
			p := filepath.Join(dir, "written")
			return []Step{WriteFile(p, "a much longer first text\n"), WriteFile(p, "second\n")}
		}, check: func(t *testing.T, dir string, r ran) {
			t.Helper()
			want(t, r, 0, "", "")
			if got := read(t, filepath.Join(dir, "written")); got != "second\n" {
				t.Errorf("the file holds %q, want %q", got, "second\n")
			}
		}},
		{name: "AppendFile makes the file and adds to it", steps: func(dir string) []Step {
			p := filepath.Join(dir, "appended")
			return []Step{AppendFile(p, "one\n"), AppendFile(p, "two\n")}
		}, check: func(t *testing.T, dir string, r ran) {
			t.Helper()
			want(t, r, 0, "", "")
			if got := read(t, filepath.Join(dir, "appended")); got != "one\ntwo\n" {
				t.Errorf("the file holds %q, want %q", got, "one\ntwo\n")
			}
		}},
		{name: "AppendArgs logs the call as one line", steps: func(dir string) []Step {
			return []Step{AppendArgs(filepath.Join(dir, "calls"))}
		}, args: []string{"status", "--short", "two words"}, check: func(t *testing.T, dir string, r ran) {
			t.Helper()
			want(t, r, 0, "", "")
			if got := read(t, filepath.Join(dir, "calls")); got != "status --short two words\n" {
				t.Errorf("the log holds %q", got)
			}
		}},
		{name: "PrintArgs writes one argument to a line", steps: func(string) []Step { return []Step{PrintArgs()} },
			args: []string{"a", "two words", ""}, check: func(t *testing.T, _ string, r ran) { t.Helper(); want(t, r, 0, "a\ntwo words\n\n", "") }},
		{name: "PrintEnv of names: their values, unset for one not given", steps: func(string) []Step {
			return []Step{PrintEnv("FAKE_ROW_SET", "FAKE_ROW_NOT_SET", "FAKE_ROW_EMPTY")}
		}, env: []string{"FAKE_ROW_SET=x y", "FAKE_ROW_EMPTY="}, check: func(t *testing.T, _ string, r ran) {
			t.Helper()
			want(t, r, 0, "x y|unset|", "")
		}},
		{name: "PrintEnv of no names: the whole environment, sorted", steps: func(string) []Step { return []Step{PrintEnv()} },
			env: []string{"FAKE_ROW_SET=x y"}, check: func(t *testing.T, _ string, r ran) {
				t.Helper()
				lines := strings.Split(strings.TrimSuffix(r.stdout, "\n"), "\n")
				if r.code != 0 || !slices.Contains(lines, "FAKE_ROW_SET=x y") || !slices.IsSorted(lines) {
					t.Errorf("code %d; want the whole environment, sorted, FAKE_ROW_SET among it:\n%s", r.code, r.stdout)
				}
			}},
		{name: "WritePID writes its own process id", steps: func(dir string) []Step {
			return []Step{WritePID(filepath.Join(dir, "pid"))}
		}, check: func(t *testing.T, dir string, r ran) {
			t.Helper()
			want(t, r, 0, "", "")
			if got := read(t, filepath.Join(dir, "pid")); got != strconv.Itoa(r.pid)+"\n" {
				t.Errorf("the file holds %q, want the child's id %d and a line end", got, r.pid)
			}
		}},
		{name: "ReadStdin writes stdin to its end", steps: func(dir string) []Step {
			return []Step{ReadStdin(filepath.Join(dir, "stdin")), Out("read\n")}
		}, stdin: "line one\nno line end", check: func(t *testing.T, dir string, r ran) {
			t.Helper()
			want(t, r, 0, "read\n", "")
			if got := read(t, filepath.Join(dir, "stdin")); got != "line one\nno line end" {
				t.Errorf("the file holds %q", got)
			}
		}},
		{name: "Run starts a program by its path, drops its output and goes on whatever its code", steps: func(dir string) []Step {
			other := Program(t, dir, "other", otherSteps(dir)...)
			return []Step{Run(other, "4", "x"), Out("went on\n")}
		}, check: func(t *testing.T, dir string, r ran) {
			t.Helper()
			want(t, r, 0, "went on\n", "")
			if got := read(t, filepath.Join(dir, "other.log")); got != "4 x\n" {
				t.Errorf("the program Run started logged %q, want %q", got, "4 x\n")
			}
		}},
		{name: "Exec passes its arguments and streams and ends with a non-zero code", steps: func(dir string) []Step {
			return []Step{Exec(Program(t, dir, "other", otherSteps(dir)...)), Out("not reached\n")}
		}, args: []string{"5", "y"}, check: func(t *testing.T, dir string, r ran) {
			t.Helper()
			want(t, r, 5, "other out\n", "other err\n")
			if got := read(t, filepath.Join(dir, "other.log")); got != "5 y\n" {
				t.Errorf("the program Exec started logged %q, want %q", got, "5 y\n")
			}
		}},
		{name: "Exec of a program that ends with 0 goes on", steps: func(dir string) []Step {
			return []Step{Exec(Program(t, dir, "other", otherSteps(dir)...)), Out("went on\n")}
		}, args: []string{"0"}, check: func(t *testing.T, _ string, r ran) { t.Helper(); want(t, r, 0, "other out\nwent on\n", "other err\n") }},
		{name: "Hold fills its memory and goes on", steps: func(string) []Step { return []Step{Hold(8), Out("held\n")} },
			check: func(t *testing.T, _ string, r ran) { t.Helper(); want(t, r, 0, "held\n", "") }},
		{name: "IfExists does then when the path is there", steps: func(dir string) []Step {
			return []Step{IfExists(dir, []Step{Out("there\n")}, []Step{Out("not there\n")}), Out("went on\n")}
		}, check: func(t *testing.T, _ string, r ran) { t.Helper(); want(t, r, 0, "there\nwent on\n", "") }},
		{name: "IfExists does else when the path is not there and its Exit ends it", steps: func(dir string) []Step {
			return []Step{IfExists(filepath.Join(dir, "none"), []Step{Out("there\n")}, []Step{Out("not there\n"), Exit(6)}), Out("not reached\n")}
		}, check: func(t *testing.T, _ string, r ran) { t.Helper(); want(t, r, 6, "not there\n", "") }},
	}
	whens := []Step{When("get *", Out("got\n"), Exit(2)), When("--version", Out("v1\n")), Out("no When fit\n")}
	for _, c := range []struct {
		args []string
		code int
		out  string
	}{
		{[]string{"get", "x"}, 2, "got\n"},
		{[]string{"--version"}, 0, "v1\n"},
		{[]string{"put", "x"}, 0, "no When fit\n"},
		{nil, 0, "no When fit\n"},
	} {
		rows = append(rows, stepRow{name: fmt.Sprintf("When, the first that fits wins: %q", c.args), args: c.args,
			steps: func(string) []Step { return whens },
			check: func(t *testing.T, _ string, r ran) { t.Helper(); want(t, r, c.code, c.out, "") }})
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			path := Program(t, dir, "tool", row.steps(dir)...)
			row.check(t, dir, start(t, path, dir, row.stdin, row.env, row.args...))
		})
	}
}

// Flood writes until the reader is gone and then ends with code 0, on every system: where a write to
// a pipe with no reader would end the process by SIGPIPE, the stand-in takes the failed write.
//
// What it costs when this fails: a test of a reader that stops early reads a writer killed by a
// signal on Linux and macOS and a clean end on Windows, and asserts one of them.
func TestFloodEndsWithCodeZeroOnceItsReaderIsGone(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := Program(t, dir, "flood", Flood("flood\n"))
	ctx, cancel := context.WithTimeout(t.Context(), childLimit)
	defer cancel()
	cmd := exec.CommandContext(ctx, path) //nolint:gosec // G204: the stand-in this row placed, by its path
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 64<<10)
	if _, err := io.ReadFull(stdout, got); err != nil {
		t.Fatalf("reading the flood: %v", err)
	}
	if !bytes.HasPrefix(got, []byte("flood\nflood\n")) {
		t.Errorf("the flood begins %q", got[:12])
	}
	if err := stdout.Close(); err != nil {
		t.Fatal(err)
	}
	err = cmd.Wait()
	if ctx.Err() != nil {
		t.Fatalf("the flood did not end within %s of its reader's close", childLimit)
	}
	if err != nil {
		t.Errorf("the flood ended by %v, want code 0", err)
	}
}

// Spin does not end by itself: the stand-in runs until it is killed.
//
// What it costs when this fails: a test of a deadline or a kill meets a program that ended on its own,
// and passes without its deadline or kill ever doing anything.
func TestSpinRunsUntilItIsKilled(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := Program(t, dir, "spin", WritePID(filepath.Join(dir, "pid")), Spin(), Out("not reached\n"))
	cmd := exec.Command(path) //nolint:gosec,noctx // G204: the stand-in this row placed; the row kills it itself
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	ended := make(chan error, 1)
	go func() { ended <- cmd.Wait() }()
	select {
	case err := <-ended:
		t.Fatalf("the spin ended by itself: %v", err)
	case <-time.After(500 * time.Millisecond):
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ended:
	case <-time.After(childLimit):
		t.Fatalf("the spin did not end within %s of its kill", childLimit)
	}
	if stdout.Len() != 0 {
		t.Errorf("a step after Spin ran: %q", stdout.String())
	}
}

// A step that cannot do its work, and steps that cannot be read, end the stand-in with code 125 and
// one line on stderr that starts "fake:", before any later step: a broken stand-in is never read as
// the program it stands in for.
//
// What it costs when this fails: a test reads a stand-in's half-done run, or its code 0, as the
// answer of the tool it stands in for.
func TestAStepThatCannotBeDoneEndsTheStandInWith125(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name  string
		steps func(dir string) []Step
		spoil func(t *testing.T, dir, path string)
		says  string
	}{
		{name: "a file it cannot write", steps: func(dir string) []Step {
			return []Step{WriteFile(filepath.Join(dir, "no-folder", "file"), "x"), Out("not reached\n")}
		}, says: "fake: its step write-file:"},
		{name: "a program Run cannot start", steps: func(dir string) []Step {
			return []Step{Run(filepath.Join(dir, "no-program")), Out("not reached\n")}
		}, says: "fake: its step run:"},
		{name: "a program Exec cannot start", steps: func(dir string) []Step {
			return []Step{Exec(filepath.Join(dir, "no-program")), Out("not reached\n")}
		}, says: "fake: its step exec:"},
		{name: "steps that are gone", steps: func(string) []Step { return []Step{Out("not reached\n")} },
			spoil: func(t *testing.T, _, path string) {
				t.Helper()
				if err := os.Remove(stepsFile(path)); err != nil {
					t.Fatal(err)
				}
			}, says: "fake: its steps:"},
		{name: "steps of another build", steps: func(string) []Step { return []Step{Out("not reached\n")} },
			spoil: func(t *testing.T, _, path string) {
				t.Helper()
				if err := os.WriteFile(stepsFile(path), []byte(`[{"op":"out","text":"x"},{"op":"shell"}]`), 0o600); err != nil {
					t.Fatal(err)
				}
			}, says: "fake: its steps in "},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			path := Program(t, dir, "tool", c.steps(dir)...)
			if c.spoil != nil {
				c.spoil(t, dir, path)
			}
			r := start(t, path, dir, "", nil)
			if r.code != failed || r.stdout != "" || !strings.HasPrefix(r.stderr, c.says) || strings.Count(r.stderr, "\n") != 1 {
				t.Errorf("code %d, stdout %q, stderr %q; want code %d, no output and one line that starts %q", r.code, r.stdout, r.stderr, failed, c.says)
			}
		})
	}
}

// stepsFile is the steps file beside the stand-in at path.
func stepsFile(path string) string {
	return filepath.Join(filepath.Dir(path), programName(filepath.Base(path))+stepsSuffix)
}

// A stand-in started through a symbolic link, from another folder, finds its own steps and never a
// steps file beside the link.
//
// What it costs when this fails: a product that puts a link to its tool on PATH, as a package manager
// does, reads a stand-in that cannot find its steps, or one that does a planted file's.
func TestAStandInStartedThroughALinkFindsItsOwnSteps(t *testing.T) {
	t.Parallel()
	testkit.Links(t)
	dir := t.TempDir()
	path := Program(t, dir, "tool", Out("its own steps\n"))
	other := t.TempDir()
	link := filepath.Join(other, filepath.Base(path))
	testkit.Link(t, path, link)
	if err := os.WriteFile(stepsFile(link), []byte(`[{"op":"out","text":"the link's\n"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	want(t, start(t, link, other, "", nil), 0, "its own steps\n", "")
}

// A refusing records what Program refused in place of ending the row, so a row reads the refusal.
type refusing struct {
	testing.TB
	refused string
}

func (r *refusing) Helper() {}

func (r *refusing) Fatalf(format string, args ...any) { r.refused = fmt.Sprintf(format, args...) }

// Program refuses, and places nothing for, a name that is not one local file name, a name already in
// the folder as a program or as a steps file, a Custom step, which only a stand-in built by From does,
// and a step the stand-in could not do as written: an Exit code outside 0 to 255 or 125, a Hold outside
// 0 to maxHold MiB, and text, a path or an argument that is not UTF-8, each in a When or an IfExists
// too. The edges of those ranges, and OutBytes' bytes, are placed.
//
// What it costs when this fails: a test replaces a program or a steps file it did not place, writes
// a stand-in outside its folder, or places one that ends with code 125 at its first call; a row that
// wants a failure passes on Exit(256), which ends 0; Hold(-1) panics in place of the stand-in's own
// failure; or the stand-in writes U+FFFD where the test gave other bytes.
func TestProgramRefusesWhatItCannotPlace(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	Program(t, dir, "placed", Out("x"))
	if err := os.WriteFile(filepath.Join(dir, "steps-only"+stepsSuffix), []byte("[]"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := names(t, dir)
	for _, c := range []struct {
		name, program, says string
		steps               []Step
	}{
		{name: "a path", program: filepath.Join("sub", "tool"), says: "refused the name"},
		{name: "..", program: "..", says: "refused the name"},
		{name: "empty", program: "", says: "refused the name"},
		{name: "a name already placed", program: "placed", says: "placing the program"},
		{name: "a steps file already there", program: "steps-only", says: "refused its steps file"},
		{name: "a Custom step", program: "custom", steps: []Step{Custom("shout", "x")}, says: `refused the Custom step "shout"`},
		{name: "a Custom step in a When", program: "custom", steps: []Step{When("*", Custom("shout", "x"))}, says: `refused the Custom step "shout"`},
		{name: "a Custom step in an else", program: "custom", steps: []Step{IfExists(dir, nil, []Step{Custom("shout", "x")})}, says: `refused the Custom step "shout"`},
		{name: "an Exit past 255", program: "exit", steps: []Step{Exit(256)}, says: "refused an Exit with code 256"},
		{name: "an Exit below 0", program: "exit", steps: []Step{Exit(-1)}, says: "refused an Exit with code -1"},
		{name: "an Exit of the stand-in's own failure", program: "exit", steps: []Step{Exit(failed)}, says: "refused an Exit with code 125"},
		{name: "an Exit past 255 in a When", program: "exit", steps: []Step{When("*", Exit(256))}, says: "refused an Exit with code 256"},
		{name: "an Exit past 255 in an else", program: "exit", steps: []Step{IfExists(dir, nil, []Step{Exit(256)})}, says: "refused an Exit with code 256"},
		{name: "a Hold below 0", program: "hold", steps: []Step{Hold(-1)}, says: "refused a Hold of -1 MiB"},
		{name: "a Hold past the most", program: "hold", steps: []Step{Hold(maxHold + 1)}, says: "refused a Hold of 65537 MiB"},
		{name: "an Out that is not UTF-8", program: "text", steps: []Step{Out("a\xffb")}, says: "refused the out step whose text is not UTF-8"},
		{name: "an Err that is not UTF-8", program: "text", steps: []Step{Err("\xc3")}, says: "refused the err step whose text is not UTF-8"},
		{name: "a WriteFile path that is not UTF-8", program: "text", steps: []Step{WriteFile("f\xff", "x")}, says: "refused the write-file step whose path is not UTF-8"},
		{name: "an AppendFile text that is not UTF-8", program: "text", steps: []Step{AppendFile("f", "\xff")}, says: "refused the append-file step whose text is not UTF-8"},
		{name: "a Run argument that is not UTF-8", program: "text", steps: []Step{Run("/bin/true", "\xff")}, says: "refused the run step with an entry"},
		{name: "a PrintEnv name that is not UTF-8", program: "text", steps: []Step{PrintEnv("N\xff")}, says: "refused the print-env step with an entry"},
		{name: "a When pattern that is not UTF-8", program: "text", steps: []Step{When("\xff*", Out("x"))}, says: "refused the when step whose text is not UTF-8"},
	} {
		r := &refusing{TB: t}
		if path := Program(r, dir, c.program, c.steps...); path != "" || !strings.Contains(r.refused, c.says) {
			t.Errorf("%s: Program gave %q and said %q; want no path and a refusal holding %q", c.name, path, r.refused, c.says)
		}
	}
	if after := names(t, dir); !slices.Equal(after, before) {
		t.Errorf("the folder held %q and holds %q after the refusals", before, after)
	}
	// The edges of what is refused are placed.
	for i, steps := range [][]Step{{Exit(0)}, {Exit(124)}, {Exit(126)}, {Exit(255)}, {Hold(0)}, {Hold(maxHold)}, {Out("é ✓"), OutBytes([]byte{0xff})}} {
		if path := Program(t, dir, fmt.Sprintf("edge%d", i), steps...); path == "" {
			t.Errorf("the steps %v at the edge of a refusal were not placed", steps)
		}
	}
}

// names is what folder holds, sorted.
func names(t *testing.T, folder string) []string {
	t.Helper()
	entries, err := os.ReadDir(folder)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range entries {
		got = append(got, e.Name())
	}
	return got
}

// A stand-in built by From from a product's own main package does that package's Custom steps among
// the generic ones, in a When too, and a step's end gives its code.
//
// What it costs when this fails: a product that needs one step of its own cannot use the package, and
// keeps its own copy of every generic step beside it.
func TestAStandInBuiltFromAProductsMainDoesItsCustomSteps(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := From("./fake/testdata/custom").Program(t, dir, "tool",
		Custom("shout", "hello"), When("* stop *", Custom("refuse", "stop"), Out("not reached\n")), Out("went on\n"))
	want(t, start(t, path, dir, "", nil, "a", "b"), 0, "HELLO a,b\nwent on\n", "")
	want(t, start(t, path, dir, "", nil, "a", "stop", "b"), 7, "HELLO a,stop,b\n", "")
}

// Twenty tests at once each place a stand-in and start it at once, and each passes: no start meets
// the copy of another still open for writing in a child forked meanwhile (text file busy on Linux).
//
// What it costs when this fails: a package of parallel tests with stand-ins fails now and then on
// Linux alone, as it did before Install held the fork lock.
func TestTwentyTestsAtOnceEachPlaceAndStartAStandIn(t *testing.T) {
	t.Parallel()
	for i := range 20 {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			path := Program(t, dir, "tool", PrintArgs())
			want(t, start(t, path, dir, "", nil, strconv.Itoa(i)), 0, strconv.Itoa(i)+"\n", "")
		})
	}
}

// Program's path is the stand-in's, under the name given, with .exe added on Windows alone.
//
// What it costs when this fails: a test that builds its own command line from the name meets a path
// that is not there on one system.
func TestProgramsPathIsTheNameGivenWithExeOnWindows(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := Program(t, dir, "tool")
	wantName := "tool" + exe
	if path != filepath.Join(dir, wantName) {
		t.Errorf("Program gave %s, want %s", path, filepath.Join(dir, wantName))
	}
}
