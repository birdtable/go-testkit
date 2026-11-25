package fake

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec" //nolint:depguard // a stand-in starts the program a Run or an Exec step names, and testkit is a leaf with no runner to start it through
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// failed is the code a stand-in ends with when it cannot do a step or cannot read its steps: one no
// step of a test is likely to give, so a broken stand-in is not read as the program it stands in for.
const failed = 125

// A Handler does one Custom step of a product's own stand-in. arg is the step's own argument and args
// are the stand-in's. end is whether the step ends the stand-in, with code; a step that does not end
// it gives no code.
type handler = func(arg string, args []string) (code int, end bool)

// Main is a stand-in's whole main: it reads the steps written beside the program, does them with the
// program's arguments, and ends the process with the code they gave. custom holds the product's own
// steps by name, each called for a Custom step of that name; the default stand-in passes nil. It
// does not return.
//
//	func main() { fake.Main(map[string]func(string, []string) (int, bool){"lint": lint}) }
//
// The steps file is found from the program's own path with every link resolved, so a link to a
// stand-in runs that stand-in's steps and never a file beside the link. Steps that cannot be found
// or read, a step this build does not know and a Custom step with no handler each end the stand-in
// with code 125 before it does anything.
func Main(custom map[string]func(arg string, args []string) (code int, end bool)) {
	os.Exit(run(custom, os.Args[1:])) //nolint:forbidigo,revive // go.errors.4 and deep-exit: this is the main of every stand-in, which ends with its steps' code
}

// fail says on stderr what the stand-in could not do, and is the code it ends with.
func fail(what string, err error) int {
	fmt.Fprintln(os.Stderr, "fake:", what+":", err)
	return failed
}

// run is Main less the exit: the code the stand-in ends with.
func run(custom map[string]handler, args []string) int {
	path, err := stepsPath()
	if err != nil {
		return fail("its own path", err)
	}
	raw, err := os.ReadFile(path) //nolint:gosec // G304: the steps beside the program itself, written by the test that placed it
	if err != nil {
		return fail("its steps", err)
	}
	steps, err := parse(raw, custom)
	if err != nil {
		return fail("its steps in "+path, err)
	}
	s := &standIn{custom: custom, args: args}
	code, _ := s.do(steps)
	runtime.KeepAlive(s) // what a Hold step filled is the program's own until here
	return code
}

// stepsPath is where the steps of the running program are: beside the program itself, under the name
// it was placed by. A program started through a link is resolved first, and one whose path cannot be
// resolved has no steps: a file beside the link is never read in their place.
func stepsPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(exe), programName(filepath.Base(exe))+stepsSuffix), nil
}

// parse reads a steps file whole and checks each step before any is done: a field or a step this
// build does not know, a Custom step with no handler in custom, a Run step with no program and a step
// Program refuses as written (an Exit code or a Hold out of range) are each an error.
func parse(raw []byte, custom map[string]handler) ([]wire, error) {
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	var steps []wire
	if err := dec.Decode(&steps); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("more than one list of steps")
	}
	return steps, check(steps, custom)
}

// check is why the stand-in cannot do steps, those of a When or an IfExists among them, or nil.
func check(steps []wire, custom map[string]handler) error {
	for _, w := range steps {
		switch w.Op {
		case opOut, opErr, opOutBytes, opExit, opSleep, opWriteFile, opAppendFile, opAppendArgs, opPrintArgs,
			opPrintEnv, opWritePID, opReadStdin, opExec, opFlood, opSpin, opHold, opWhen, opIfExists:
		case opRun:
			if len(w.List) == 0 {
				return errors.New("a Run step names no program")
			}
		case opCustom:
			if custom[w.Path] == nil {
				return fmt.Errorf("the Custom step %q has no handler in this stand-in", w.Path)
			}
		default:
			return fmt.Errorf("the step %q is not one this stand-in knows", w.Op)
		}
		if err := refusal(w); err != nil {
			return err
		}
		if err := errors.Join(check(w.Then, custom), check(w.Else, custom)); err != nil {
			return err
		}
	}
	return nil
}

// A standIn is one run of a stand-in.
type standIn struct {
	custom map[string]handler
	args   []string
	held   []byte // what a Hold step filled, kept for the life of the run
	spun   uint64 // what a Spin step counts, so its loop is work and not an empty one
}

// do does steps in order. ended is whether one of them ended the stand-in, with code.
func (s *standIn) do(steps []wire) (code int, ended bool) {
	for _, w := range steps {
		if code, ended = s.step(w); ended {
			return code, true
		}
	}
	return 0, false
}

// step does one step. ended is whether it ended the stand-in, with code.
func (s *standIn) step(w wire) (code int, ended bool) {
	var err error
	switch w.Op {
	case opExit:
		return int(w.N), true
	case opWhen:
		if !fits(w.Text, strings.Join(s.args, " ")) {
			return 0, false
		}
		code, _ = s.do(w.Then)
		return code, true
	case opIfExists:
		branch := w.Else
		if _, err := os.Stat(w.Path); err == nil {
			branch = w.Then
		}
		return s.do(branch)
	case opCustom:
		return s.custom[w.Path](w.Text, s.args)
	case opExec:
		return s.exec(w.Path)
	case opFlood:
		// A write to a pipe whose reader is gone ends a process by SIGPIPE where there is one. Flood ends
		// with a code, the same on every system, so it takes the failed write instead. It is the stand-in's
		// last step, so no program a later step starts inherits the ignore.
		signal.Ignore(syscall.SIGPIPE)
		for err == nil {
			_, err = os.Stdout.WriteString(w.Text)
		}
		return 0, true
	case opSpin:
		for {
			s.spun++
		}
	case opSleep:
		<-time.After(time.Duration(w.N))
	case opHold:
		s.held = make([]byte, w.N<<20)
		for i := range s.held {
			s.held[i] = 'x'
		}
	case opRun:
		err = s.run(w.List)
	default:
		err = s.write(w)
	}
	if err != nil {
		return fail("its step "+w.Op, err), true
	}
	return 0, false
}

// write does a step that writes to a stream or to a file.
func (s *standIn) write(w wire) error {
	var err error
	switch w.Op {
	case opOut:
		_, err = os.Stdout.WriteString(w.Text)
	case opErr:
		_, err = os.Stderr.WriteString(w.Text)
	case opOutBytes:
		_, err = os.Stdout.Write(w.Bytes)
	case opPrintArgs:
		for _, a := range s.args {
			if _, err = os.Stdout.WriteString(a + "\n"); err != nil {
				break
			}
		}
	case opPrintEnv:
		_, err = os.Stdout.WriteString(environment(w.List))
	case opWriteFile:
		err = os.WriteFile(w.Path, []byte(w.Text), 0o600)
	case opWritePID:
		err = os.WriteFile(w.Path, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600)
	case opAppendFile:
		err = appendTo(w.Path, w.Text)
	case opAppendArgs:
		err = appendTo(w.Path, strings.Join(s.args, " ")+"\n")
	case opReadStdin:
		err = readStdin(w.Path)
	}
	return err
}

// environment is what a PrintEnv step writes: with no names the whole environment, sorted, a
// NAME=value to a line; with names their values joined by |, "unset" for one that is not set.
func environment(names []string) string {
	if len(names) == 0 {
		env := os.Environ()
		slices.Sort(env)
		var b strings.Builder
		for _, kv := range env {
			b.WriteString(kv + "\n")
		}
		return b.String()
	}
	values := make([]string, len(names))
	for i, name := range names {
		v, set := os.LookupEnv(name)
		if !set {
			v = "unset"
		}
		values[i] = v
	}
	return strings.Join(values, "|")
}

// appendTo adds text to the end of the file at path in one write, making the file when there is
// none, so two stand-ins that log to one file each keep their whole line.
func appendTo(path, text string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600) //nolint:gosec // G304: a path the test named
	if err != nil {
		return err
	}
	_, err = f.WriteString(text)
	return errors.Join(err, f.Close())
}

// readStdin writes stdin, read to its end, to the file at path.
func readStdin(path string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) //nolint:gosec // G304: a path the test named
	if err != nil {
		return err
	}
	_, err = io.Copy(f, os.Stdin)
	return errors.Join(err, f.Close())
}

// run starts the program at argv[0] with the rest as its arguments, its output dropped, and waits
// for it. A program that ran is no error, whatever its exit code; one that could not be started is.
// The path goes to the system as it is: nothing looks it up on PATH.
func (s *standIn) run(argv []string) error {
	cmd := &exec.Cmd{Path: argv[0], Args: argv}
	var exit *exec.ExitError
	if err := cmd.Run(); err != nil && !errors.As(err, &exit) {
		return err
	}
	return nil
}

// exec runs the program at path with the stand-in's own arguments and streams. A non-zero exit of it
// ends the stand-in with that code, and so does a program that could not be started, with the
// stand-in's own code.
func (s *standIn) exec(path string) (code int, ended bool) {
	cmd := &exec.Cmd{Path: path, Args: append([]string{path}, s.args...), Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr}
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return 0, false
	case errors.As(err, &exit):
		return exit.ExitCode(), true
	}
	return fail("its step "+opExec, err), true
}

// fits is whether s fits pattern, where * is any run of characters, none among them, and every other
// character is itself.
func fits(pattern, s string) bool {
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return s == pattern
	}
	if !strings.HasPrefix(s, parts[0]) {
		return false
	}
	s = s[len(parts[0]):]
	last := parts[len(parts)-1]
	for _, p := range parts[1 : len(parts)-1] {
		i := strings.Index(s, p)
		if i < 0 {
			return false
		}
		s = s[i+len(p):]
	}
	return strings.HasSuffix(s, last)
}
