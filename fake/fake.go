// Package fake puts a stand-in program where a test needs one: a built program that does what a list
// of steps says when it is run, on Linux, macOS and Windows alike, so no test writes a shell script.
// It is built on testkit's Build and Binary.Install, and adds one stand-in, started by its path and
// read by no shell, where a test would write a script or rerun itself as a helper. The test configures
// the folder, the name and the steps it passes to Program; a product's own steps come through Custom:
// its own main package calls Main with a handler for each name, and From builds the stand-in from it.
//
//	tool := fake.Program(t, bin, "tool", fake.AppendArgs(log), fake.Out("done\n"), fake.Exit(3))
//
// Program builds the stand-in once with Build, which is for use under testkit's Main, and copies it
// into the folder with Install. The steps are a JSON file beside the copy, <name>.fake, found from the
// stand-in's own path with any link resolved, so a start through a symbolic link finds it too.
//
// A stand-in does its steps in order and ends with code 0 after the last, unless a step ended it
// sooner. A step that cannot do its work (a file it cannot write, a program it cannot start, steps it
// cannot read) ends the stand-in with code 125 and one line on stderr that starts "fake:". A path in
// a step is read by the stand-in, so a relative one is relative to the folder the stand-in runs in.
// No step names a shell, and no step looks a program up by name: Run and Exec take a path.
package fake

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/birdtable/go-testkit"
)

// defaultPkg is the stand-in Program builds: a main that calls Main(nil).
const defaultPkg = "github.com/birdtable/go-testkit/internal/fakeprogram"

// stepsSuffix is what a stand-in's steps file adds to its name.
const stepsSuffix = ".fake"

// A Step is one thing a stand-in does when it is run. The functions of this package make them.
type Step struct{ w wire }

// wire is a Step as the steps file holds it: op names the step, and the other fields are what that
// step reads.
type wire struct {
	Op    string   `json:"op"`
	Text  string   `json:"text,omitempty"`
	Bytes []byte   `json:"bytes,omitempty"`
	Path  string   `json:"path,omitempty"`
	N     int64    `json:"n,omitempty"`
	List  []string `json:"list,omitempty"`
	Then  []wire   `json:"then,omitempty"`
	Else  []wire   `json:"else,omitempty"`
}

// The ops of a steps file, one to a step.
const (
	opOut        = "out"
	opErr        = "err"
	opOutBytes   = "out-bytes"
	opExit       = "exit"
	opSleep      = "sleep"
	opWriteFile  = "write-file"
	opAppendFile = "append-file"
	opAppendArgs = "append-args"
	opPrintArgs  = "print-args"
	opPrintEnv   = "print-env"
	opWritePID   = "write-pid"
	opReadStdin  = "read-stdin"
	opRun        = "run"
	opExec       = "exec"
	opFlood      = "flood"
	opSpin       = "spin"
	opHold       = "hold"
	opWhen       = "when"
	opIfExists   = "if-exists"
	opCustom     = "custom"
)

// Out is a step that writes s to stdout.
func Out(s string) Step { return Step{wire{Op: opOut, Text: s}} }

// Err is a step that writes s to stderr.
func Err(s string) Step { return Step{wire{Op: opErr, Text: s}} }

// OutBytes is a step that writes b to stdout, for bytes that are not text.
func OutBytes(b []byte) Step { return Step{wire{Op: opOutBytes, Bytes: b}} }

// Exit is a step that ends the stand-in with code, from 0 to 255, the codes every system ends a
// process with alike, and not 125, which is the stand-in's own failure. Program refuses any other.
func Exit(code int) Step { return Step{wire{Op: opExit, N: int64(code)}} }

// Sleep is a step that waits d.
func Sleep(d time.Duration) Step { return Step{wire{Op: opSleep, N: int64(d)}} }

// WriteFile is a step that writes text to the file at path, in place of what it held.
func WriteFile(path, text string) Step { return Step{wire{Op: opWriteFile, Path: path, Text: text}} }

// AppendFile is a step that adds text to the end of the file at path, which it makes when there is
// none.
func AppendFile(path, text string) Step {
	return Step{wire{Op: opAppendFile, Path: path, Text: text}}
}

// AppendArgs is a step that adds the stand-in's arguments, joined by spaces, as one line to the end
// of the file at path: a log of each call.
func AppendArgs(path string) Step { return Step{wire{Op: opAppendArgs, Path: path}} }

// PrintArgs is a step that writes the stand-in's arguments to stdout, one to a line.
func PrintArgs() Step { return Step{wire{Op: opPrintArgs}} }

// PrintEnv is a step that writes the stand-in's environment to stdout. With no names it is the whole
// environment, sorted, a NAME=value to a line. With names it is their values in that order, joined by
// |, with "unset" for one the stand-in was not given, and no line end.
func PrintEnv(names ...string) Step { return Step{wire{Op: opPrintEnv, List: names}} }

// WritePID is a step that writes the stand-in's process id and a line end to the file at path.
func WritePID(path string) Step { return Step{wire{Op: opWritePID, Path: path}} }

// ReadStdin is a step that reads stdin to its end and writes what it read to the file at path.
func ReadStdin(path string) Step { return Step{wire{Op: opReadStdin, Path: path}} }

// Run is a step that starts the program at the path argv[0] with the arguments after it, drops its
// output, waits for its end and goes on, whatever its exit code. The path is started as it is given
// and never looked up on PATH.
func Run(argv ...string) Step { return Step{wire{Op: opRun, List: argv}} }

// Exec is a step that runs the program at path with the stand-in's own arguments and its standard
// streams, then goes on; a non-zero exit of that program ends the stand-in with the same code. The
// path is started as it is given and never looked up on PATH.
func Exec(path string) Step { return Step{wire{Op: opExec, Path: path}} }

// Flood is a step that writes s to stdout over and over until a write fails, as it does once the
// reader is gone, and then ends the stand-in with code 0.
func Flood(s string) Step { return Step{wire{Op: opFlood, Text: s}} }

// Spin is a step that never ends: a busy loop on one thread.
func Spin() Step { return Step{wire{Op: opSpin}} }

// Hold is a step that fills mib MiB of memory, from 0 to 65536, and keeps it until the stand-in ends.
// Program refuses any other.
func Hold(mib int) Step { return Step{wire{Op: opHold, N: int64(mib)}} }

// When is a step that, when the stand-in's arguments joined by spaces fit pattern, does then and
// ends the stand-in, with the code a step of then gave or 0. In pattern a * is any run of characters
// and every other character is itself. When they do not fit, the stand-in goes on with the next step,
// so a list of When steps is read as a shell's case: the first that fits wins.
func When(pattern string, then ...Step) Step {
	return Step{wire{Op: opWhen, Text: pattern, Then: wires(then)}}
}

// IfExists is a step that does then when something exists at path, els when nothing does, and goes
// on with the next step unless one of them ended the stand-in.
func IfExists(path string, then, els []Step) Step {
	return Step{wire{Op: opIfExists, Path: path, Then: wires(then), Else: wires(els)}}
}

// Custom is a step the product's own stand-in does: the handler its Main was given under name is
// called with arg and the stand-in's arguments. Only a stand-in built by From does it; Program
// refuses it, since the default stand-in has no handlers.
func Custom(name, arg string) Step { return Step{wire{Op: opCustom, Path: name, Text: arg}} }

// wires is the steps as the steps file holds them.
func wires(steps []Step) []wire {
	ws := make([]wire, len(steps))
	for i, s := range steps {
		ws[i] = s.w
	}
	return ws
}

// maxHold is the most MiB a Hold step fills: 64 GiB, past any limit a test holds a program to, and
// far under what the stand-in can ask the runtime for, so no Hold panics in make.
const maxHold = 64 << 10

// refusal is why the stand-in cannot do the step w as it is written, or nil: an Exit code outside
// 0 to 255, which systems end a process with differently, or 125, the stand-in's own failure; a
// Hold of less than nothing or past maxHold; and text, a path or a list entry that is not UTF-8,
// which the steps file, JSON, cannot carry and would hold as U+FFFD. OutBytes carries bytes. The
// steps of a When or an IfExists are not its; whoever walks the steps reads them.
func refusal(w wire) error {
	switch {
	case w.Op == opExit && (w.N < 0 || w.N > 255 || w.N == failed):
		return fmt.Errorf("an Exit with code %d: a code is 0 to 255, alike on every system, and not %d, the stand-in's own failure", w.N, failed)
	case w.Op == opHold && (w.N < 0 || w.N > maxHold):
		return fmt.Errorf("a Hold of %d MiB: a Hold is 0 to %d MiB", w.N, maxHold)
	case !utf8.ValidString(w.Text):
		return fmt.Errorf("the %s step whose text is not UTF-8, which its steps file cannot carry (OutBytes writes bytes)", w.Op)
	case !utf8.ValidString(w.Path):
		return fmt.Errorf("the %s step whose path is not UTF-8, which its steps file cannot carry", w.Op)
	}
	for _, s := range w.List {
		if !utf8.ValidString(s) {
			return fmt.Errorf("the %s step with an entry %q that is not UTF-8, which its steps file cannot carry", w.Op, s)
		}
	}
	return nil
}

// refuse is why Program does not place a stand-in with the steps ws, in the steps of a When or an
// IfExists too, or "": a step refusal refuses, or a Custom step, when handlers is false.
func refuse(ws []wire, handlers bool) string {
	for _, w := range ws {
		if w.Op == opCustom && !handlers {
			return fmt.Sprintf("refused the Custom step %q: the default stand-in has no handlers, so build one with From", w.Path)
		}
		if err := refusal(w); err != nil {
			return "refused " + err.Error()
		}
		for _, inner := range [][]wire{w.Then, w.Else} {
			if why := refuse(inner, handlers); why != "" {
				return why
			}
		}
	}
	return ""
}

// A Builder places stand-ins built from one main package. From makes one.
type Builder struct{ pkg string }

// From is a Builder of stand-ins built from pkg, a product's own main package whose main calls Main
// with its handlers. pkg is what testkit's Build takes: an import path, or a folder read from the
// module the test runs in.
func From(pkg string) Builder { return Builder{pkg: pkg} }

// Program puts the default stand-in in folder under name, which does steps in order when it is run,
// and is its path, with .exe added on Windows. name is one file name in folder, without the .exe.
//
// It fails t, and places nothing, for a name that is not one local file name, as Binary.Install reads
// it; for a name already in folder, as a program or as a steps file; for a Custom step, which only a
// stand-in built by From does; and for a step the stand-in could not do as written: an Exit code
// outside 0 to 255 or 125, a Hold outside 0 to 65536 MiB, or text, a path or an argument that is not
// UTF-8. A step in a When or an IfExists is refused alike.
func Program(t testing.TB, folder, name string, steps ...Step) string {
	t.Helper()
	return Builder{pkg: defaultPkg}.Program(t, folder, name, steps...)
}

// Program is the package's Program with the stand-in built from the Builder's package, so a Custom
// step is one of its steps.
func (b Builder) Program(t testing.TB, folder, name string, steps ...Step) string {
	t.Helper()
	ws := wires(steps)
	if why := refuse(ws, b.pkg != defaultPkg); why != "" {
		t.Fatalf("fake: Program of %s: %s", name, why)
		return ""
	}
	raw, err := json.Marshal(ws)
	if err != nil {
		t.Fatalf("fake: Program of %s: its steps: %v", name, err)
		return ""
	}
	path := testkit.Build(b.pkg).Install(t, folder, name)
	if path == "" {
		return ""
	}
	if err := writeSteps(filepath.Join(folder, name+stepsSuffix), raw); err != nil {
		// Install made the program, so it is this call's own to remove.
		t.Fatalf("fake: Program of %s: %v", name, errors.Join(err, os.Remove(path)))
		return ""
	}
	return path
}

// writeSteps writes raw to a new file at path, and replaces nothing: a file already there is another
// stand-in's steps, or a file of the test's own.
func writeSteps(path string, raw []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // G304: a file name Install accepted, in the caller's folder
	if err != nil {
		return fmt.Errorf("refused its steps file: %w", err)
	}
	if _, err = f.Write(raw); err != nil {
		// O_EXCL made the file, so the half write is this call's own to remove.
		return errors.Join(fmt.Errorf("writing its steps: %w", err), f.Close(), os.Remove(path))
	}
	if err = f.Close(); err != nil {
		return errors.Join(fmt.Errorf("writing its steps: %w", err), os.Remove(path))
	}
	return nil
}
