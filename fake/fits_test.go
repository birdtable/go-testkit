package fake

import (
	"strings"
	"testing"
)

// A pattern fits by its * alone: any run of characters, none among them, and every other character
// is itself, a ? and a [ too.
//
// What it costs when this fails: a When step answers a call it was not written for, or misses its own.
func TestAPatternFitsByItsStarsAlone(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		pattern, s string
		want       bool
	}{
		{"", "", true},
		{"", "a", false},
		{"get x", "get x", true},
		{"get x", "get x y", false},
		{"*", "", true},
		{"*", "anything at all", true},
		{"get *", "get x y", true},
		{"get *", "get ", true},
		{"get *", "get", false},
		{"* --version", "tool --version", true},
		{"* --version", "--version", false},
		{"a*b*c", "a-b-c", true},
		{"a*b*c", "abc", true},
		{"a*b*c", "a-c-b", false},
		{"a*a", "a", false},
		{"a*a", "aa", true},
		{"get ?", "get x", false},
		{"get [x]", "get x", false},
		{"get [x]", "get [x]", true},
	} {
		if got := fits(c.pattern, c.s); got != c.want {
			t.Errorf("fits(%q, %q) = %v, want %v", c.pattern, c.s, got, c.want)
		}
	}
}

// A steps file is checked whole before a step is done: what is not JSON, a second list, a field or a
// step this build does not know, a Custom step with no handler (in a When and an IfExists too) and a
// Run step with no program are each refused; the same steps with the handler are read.
//
// What it costs when this fails: a stand-in does half of a list it cannot finish, or reads a steps
// file written for another build as fewer steps than it holds.
func TestAStepsFileIsCheckedWholeBeforeAStepIsDone(t *testing.T) {
	t.Parallel()
	shout := map[string]handler{"shout": func(string, []string) (int, bool) { return 0, false }}
	for _, c := range []struct {
		name, raw, want string
		custom          map[string]handler
	}{
		{name: "not JSON", raw: `[{"op":"out"`, want: "unexpected EOF"},
		{name: "not a list", raw: `{"op":"out"}`, want: "cannot unmarshal"},
		{name: "a second list", raw: `[] []`, want: "more than one list"},
		{name: "an unknown field", raw: `[{"op":"out","shell":"sh"}]`, want: `unknown field "shell"`},
		{name: "an unknown step", raw: `[{"op":"out","text":"x"},{"op":"shell"}]`, want: `the step "shell" is not one`},
		{name: "no step", raw: `[{"text":"x"}]`, want: `the step "" is not one`},
		{name: "an unknown step in a When", raw: `[{"op":"when","text":"*","then":[{"op":"shell"}]}]`, want: `the step "shell" is not one`},
		{name: "an unknown step in an else", raw: `[{"op":"if-exists","path":"x","else":[{"op":"shell"}]}]`, want: `the step "shell" is not one`},
		{name: "a Custom step and no handler", raw: `[{"op":"custom","path":"shout"}]`, want: `the Custom step "shout" has no handler`},
		{name: "a Custom step in a When and no handler", raw: `[{"op":"when","text":"*","then":[{"op":"custom","path":"shout"}]}]`, want: `the Custom step "shout" has no handler`},
		{name: "a Custom step of another name", raw: `[{"op":"custom","path":"whisper"}]`, want: `the Custom step "whisper" has no handler`, custom: shout},
		{name: "a Run step with no program", raw: `[{"op":"run"}]`, want: "a Run step names no program"},
		{name: "an Exit past 255", raw: `[{"op":"exit","n":256}]`, want: "an Exit with code 256"},
		{name: "an Exit below 0", raw: `[{"op":"exit","n":-1}]`, want: "an Exit with code -1"},
		{name: "an Exit of the stand-in's own failure", raw: `[{"op":"exit","n":125}]`, want: "an Exit with code 125"},
		{name: "a Hold below 0", raw: `[{"op":"hold","n":-1}]`, want: "a Hold of -1 MiB"},
		{name: "a Hold past the most", raw: `[{"op":"hold","n":8796093022208}]`, want: "a Hold of 8796093022208 MiB"},
		{name: "an Exit past 255 in a When", raw: `[{"op":"when","text":"*","then":[{"op":"exit","n":256}]}]`, want: "an Exit with code 256"},
		{name: "an Exit of 255", raw: `[{"op":"exit","n":255}]`},
		{name: "a Custom step with its handler", raw: `[{"op":"custom","path":"shout"}]`, custom: shout},
		{name: "no steps", raw: `[]`},
	} {
		steps, err := parse([]byte(c.raw), c.custom)
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s: refused: %v", c.name, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%s: %v, %v; want an error holding %q", c.name, steps, err, c.want)
		}
	}
}

// Every step a function of this package makes is one the stand-in knows, with its fields: what
// Program writes, the default stand-in reads, less the Custom step it has no handler for.
//
// What it costs when this fails: a step added to the package and not to the stand-in ends every
// stand-in that holds it with code 125.
func TestEveryStepThePackageMakesIsOneTheStandInKnows(t *testing.T) {
	t.Parallel()
	steps := []Step{
		Out("o"), Err("e"), OutBytes([]byte{0xff}), Exit(1), Sleep(1), WriteFile("p", "t"), AppendFile("p", "t"),
		AppendArgs("p"), PrintArgs(), PrintEnv(), PrintEnv("A"), WritePID("p"), ReadStdin("p"), Run("p", "a"), Exec("p"),
		Flood("f"), Spin(), Hold(1), When("*", Out("o")), IfExists("p", []Step{Out("o")}, []Step{Err("e")}),
	}
	if err := check(wires(steps), nil); err != nil {
		t.Errorf("the default stand-in refuses a step the package makes: %v", err)
	}
	seen := map[string]bool{opCustom: true}
	for _, s := range steps {
		seen[s.w.Op] = true
	}
	for _, op := range []string{opOut, opErr, opOutBytes, opExit, opSleep, opWriteFile, opAppendFile, opAppendArgs, opPrintArgs,
		opPrintEnv, opWritePID, opReadStdin, opRun, opExec, opFlood, opSpin, opHold, opWhen, opIfExists, opCustom} {
		if !seen[op] {
			t.Errorf("no step above is a %q", op)
		}
	}
}
