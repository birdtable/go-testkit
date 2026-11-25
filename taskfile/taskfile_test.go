package taskfile

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The tests write a Taskfile.yml, and an exceptions file where a row needs one, in t.TempDir, and read
// them. The package has no TestMain of testkit's: it starts no process and reads no person's folder.

// repo writes taskfile, and exceptions where it is not "", into a new folder and gives it.
func repo(t *testing.T, taskfile, exceptions string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, taskfileName), []byte(taskfile), 0o600); err != nil {
		t.Fatal(err)
	}
	if exceptions != "" {
		if err := os.WriteFile(filepath.Join(dir, exceptionsName), []byte(exceptions), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// oneTask is a Taskfile.yml whose one task, t, runs the command cmd, a block scalar from line 5.
func oneTask(cmd string) string {
	return "version: \"3\"\n\ntasks:\n  t:\n    cmds:\n      - cmd: |\n" + indent(cmd, "          ") + "\n"
}

// indent puts prefix before each line of s.
func indent(s, prefix string) string {
	return prefix + strings.ReplaceAll(s, "\n", "\n"+prefix)
}

// refused reads dir and fails the test on an error.
func refused(t *testing.T, dir string) []string {
	t.Helper()
	got, err := read(dir)
	if err != nil {
		t.Fatalf("read(%s): %v", dir, err)
	}
	return got
}

// What it costs when this fails: a Taskfile runs a shell utility, a pipe, a redirection to a file, a
// shell loop, /tmp or a path joined in shell, which go-task's interpreter runs differently on Windows,
// and a commit tier on macOS or Linux passes it to the Windows run.
func TestEachShapeTheCheckRefusesFailsItsCommandByTaskPlaceAndLine(t *testing.T) {
	t.Parallel()
	for _, row := range []struct{ name, cmd, want string }{
		{"mktemp", `d=$(mktemp -d)`, "the shell utility mktemp"},
		{"grep", `go list ./... > /dev/null && grep -q x go.mod`, "the shell utility grep"},
		{"sed", `sed -n 1p go.mod`, "the shell utility sed"},
		{"awk", `LC_ALL=C awk '{ print $1 }' go.mod`, "the shell utility awk"},
		{"cut", `cut -d' ' -f1 go.sum`, "the shell utility cut"},
		{"tar", `/usr/bin/tar -x -f a.tar`, "the shell utility tar"},
		{"find", `if true; then find . -name '*.go'; fi`, "the shell utility find"},
		{"xargs", `xargs -n1 echo`, "the shell utility xargs"},
		{"pipe", `go list -m all | go run ./cmd/x`, "a pipe (go list -m all | go run ./cmd/x)"},
		{"pipe of stderr", `go vet ./... |& go run ./cmd/x`, "a pipe (go vet ./... |& go run ./cmd/x)"},
		{"redirection to a file", `go run ./cmd/x > out.txt`, "a redirection (> out.txt)"},
		{"append", `go run ./cmd/x >> out.txt`, "a redirection (>> out.txt)"},
		{"redirection from a file", `go run ./cmd/x < in.txt`, "a redirection (< in.txt)"},
		{"stderr to a file", `go vet ./... 2> err.txt`, "a redirection (2> err.txt)"},
		{"here-document", "go run ./cmd/x <<EOF\nline\nEOF", "a redirection (<<EOF)"},
		{"for loop", `for os in linux windows; do GOOS=$os go vet ./...; done`, "a for loop in shell"},
		{"/tmp", `go test -o /tmp/x.test ./...`, "a /tmp path (/tmp/x.test)"},
		{"/tmp as a default", `d="${TMPDIR:-/tmp}"`, "a /tmp path (/tmp)"},
		{"/tmp quoted", `go run ./cmd/x '/tmp'`, "a /tmp path ('/tmp')"},
		{"joined path", `go run ./cmd/x "$dir/out.txt"`, `a path joined in shell ("$dir/out.txt")`},
		{"joined braced", `go run ./cmd/x ${dir}/out.txt`, "a path joined in shell (${dir}/out.txt)"},
		{"joined output", `go run ./cmd/x "$(go env GOPATH)"/bin`, `a path joined in shell ("$(go env GOPATH)"/bin)`},
		{"joined template", `go run ./cmd/x {{.ROOT_DIR}}/contain`, "a path joined in shell (" + templateVar + "/contain)"},
		{"joined past a string of }}", `go run ./cmd/x {{"}}"}}/x`, "a path joined in shell (" + templateVar + "/x)"},
		{"not shell", `go run ./cmd/x "unclosed`, "go-task's interpreter would not read it as shell"},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			got := refused(t, repo(t, oneTask(row.cmd), ""))
			prefix := fmt.Sprintf("Taskfile.yml:6: task t, command 1 (%s): ", first(row.cmd))
			if len(got) != 1 || !strings.HasPrefix(got[0], prefix) || !strings.Contains(got[0], row.want) {
				t.Errorf("the check said %q, and want one line that begins %q and says %q", got, prefix, row.want)
			}
		})
	}
}

// What it costs when this fails: a repository whose Taskfile calls only Go, or sends a program's
// output to the null device, is held red, and every repository lists exceptions for lines that are fine.
func TestATaskfileOfGoLinesPassesWithTheNullDeviceAndDescriptorsPointedAtEachOther(t *testing.T) {
	t.Parallel()
	dir := repo(t, `version: "3"

env:
  GOWORK: "off"

vars:
  TOOL: go tool -modfile=tools

tasks:
  vet:
    desc: go vet for each system
    cmds:
      - for: { matrix: { OS: [linux, darwin, windows] } }
        cmd: CGO_ENABLED=0 GOOS={{.ITEM.OS}} go vet ./...
        timeout: 5m
  warm:
    cmds:
      - CGO_ENABLED=0 go list -export -test -deps ./... > /dev/null
      - cmd: go build ./... 2>/dev/null >NUL
      - cmd: echo warm built >&2
      - cmd: go vet ./... 2>&1
  lint:
    cmds:
      - cmd: '{{.TOOL}}/golangci-lint.mod golangci-lint run ./...'
      - task: vet
      - defer: { task: warm }
      - defer: go clean -testcache
  short: go test -short ./...
  both: [go vet ./..., go test ./...]
  # A word that holds a utility's name, a path written out in full and a comment are not shapes.
  words:
    cmds:
      - cmd: |
          # grep, sed and a | in a comment
          go run ./internal/cmd/find-grep --tar ./gate/api.sh internal/x.txt
`, "")
	Check(t, dir)
}

// What it costs when this fails: a repository cannot keep a command in shell for a reason, or an
// exception passes more than the command it names.
func TestAnExceptionPassesTheCommandItNamesAndNoOther(t *testing.T) {
	t.Parallel()
	dir := repo(t, `version: "3"
tasks:
  t:
    cmds:
      - go vet ./...
      - cmd: go list -m all | go run ./cmd/x
      - cmd: go run ./cmd/y >> out.txt
`, "# What the repository keeps in shell.\n\nt\t2\tthe list goes to x by a pipe until x reads go list itself\n")
	got := refused(t, dir)
	want := []string{"Taskfile.yml:7: task t, command 3 (go run ./cmd/y >> out.txt): a redirection (>> out.txt)"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("the check said %q, and want %q", got, want)
	}
}

// What it costs when this fails: the exceptions file keeps a line for a command that is gone, moved or
// already Go, and lists shell that is not there; or a line with no reason passes.
func TestAnExceptionThatMatchesNothingFailsAndSoDoesALineOfAnyOtherShape(t *testing.T) {
	t.Parallel()
	dir := repo(t, `version: "3"
tasks:
  t:
    cmds:
      - go vet ./...
      - task: u
      - go list -m all | go run ./cmd/x
  u: go test ./...
`, strings.Join([]string{
		"t\t1\tgo vet was shell once",
		"t\t2\ta task: runs no shell",
		"t\t3\tthe list goes to x by a pipe",
		"t\t3\tthe same line twice",
		"gone\t1\tthe task is gone",
		"t\t9\tthe command is gone",
		"t\t0\tthere is no command 0",
		"t\t3",
		"t\tthree\ta place that is not a number",
		"t\t3\t ",
	}, "\n")+"\n")
	got := refused(t, dir)
	want := []string{
		`taskfile-exceptions.txt:4: task t, command 3 is listed on line 3 already`,
		`taskfile-exceptions.txt:7: the line is not a task, a command's place from 1 and a reason, tab-separated: "t\t0\tthere is no command 0"`,
		`taskfile-exceptions.txt:8: the line is not a task, a command's place from 1 and a reason, tab-separated: "t\t3"`,
		`taskfile-exceptions.txt:9: the line is not a task, a command's place from 1 and a reason, tab-separated: "t\tthree\ta place that is not a number"`,
		`taskfile-exceptions.txt:10: the line is not a task, a command's place from 1 and a reason, tab-separated: "t\t3\t "`,
		`taskfile-exceptions.txt:1: task t, command 1 (go vet ./...): the check finds nothing in it, so the line is not needed`,
		`taskfile-exceptions.txt:2: task t has no command 2 that runs shell`,
		`taskfile-exceptions.txt:5: task gone has no command 1 that runs shell`,
		`taskfile-exceptions.txt:6: task t has no command 9 that runs shell`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("the check said:\n%s\nand want:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// What it costs when this fails: shell kept in a var, or a command shared by a YAML anchor, passes
// unread; or a var that names a tool reads as a path joined in shell, and every Taskfile is red.
func TestAVarAndAnAnchorAreReadWhereTheyAreUsed(t *testing.T) {
	t.Parallel()
	dir := repo(t, `version: "3"
vars:
  WORK: |
    w="${TMPDIR:-/tmp}/x-$$"
    mkdir -p "$w"
  TOOL: go tool -modfile=tools
  DYN:
    sh: go env GOPATH
tasks:
  a:
    cmds:
      - cmd: |
          {{.WORK}}
          go vet ./...
      - cmd: &shared go test ./... >> out.txt
      - cmd: '{{.TOOL}}/hatch.mod hatch tests ./...'
      - cmd: go run ./cmd/x {{.DYN}}/bin
  b:
    vars:
      TOOL: '{{.ROOT}}/tools'
    cmds:
      - cmd: *shared
      - cmd: go tool -modfile={{.TOOL}}/task.mod task
`, "")
	got := refused(t, dir)
	want := []string{
		`Taskfile.yml:12: task a, command 1 ({{.WORK}}): a path joined in shell ("${TMPDIR:-/tmp}/x-$$")`,
		`Taskfile.yml:12: task a, command 1 ({{.WORK}}): a /tmp path (/tmp)`,
		`Taskfile.yml:15: task a, command 2 (go test ./... >> out.txt): a redirection (>> out.txt)`,
		`Taskfile.yml:17: task a, command 4 (go run ./cmd/x {{.DYN}}/bin): a path joined in shell (` + templateVar + `/bin)`,
		`Taskfile.yml:15: task b, command 1 (go test ./... >> out.txt): a redirection (>> out.txt)`,
		`Taskfile.yml:23: task b, command 2 (go tool -modfile={{.TOOL}}/task.mod task): a path joined in shell (-modfile=` + templateVar + `/tools/task.mod)`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("the check said:\n%s\nand want:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// recorder is a testing.TB that keeps what Check reports.
type recorder struct {
	testing.TB
	errors []string
	fatal  string
}

func (r *recorder) Helper()                           {}
func (r *recorder) Error(args ...any)                 { r.errors = append(r.errors, fmt.Sprint(args...)) }
func (r *recorder) Fatalf(format string, args ...any) { r.fatal = fmt.Sprintf(format, args...) }

// What it costs when this fails: a repository's test passes over a Taskfile the check refuses, or over
// one it cannot read.
func TestCheckFailsItsTestOnceForEachRefusalAndAtOnceOnAFileItCannotRead(t *testing.T) {
	t.Parallel()
	r := &recorder{TB: t}
	Check(r, repo(t, oneTask("go list -m all | go run ./cmd/x > out.txt"), ""))
	if len(r.errors) != 2 || r.fatal != "" {
		t.Errorf("Check reported %q and fatal %q, and want two errors, the pipe and the redirection", r.errors, r.fatal)
	}
	for name, text := range map[string]string{
		"missing":     "",
		"not yaml":    "tasks: [\n",
		"not a map":   "- a\n",
		"tasks a seq": "tasks: [a]\n",
	} {
		r := &recorder{TB: t}
		dir := t.TempDir()
		if text != "" {
			if err := os.WriteFile(filepath.Join(dir, taskfileName), []byte(text), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		Check(r, dir)
		if r.fatal == "" || len(r.errors) != 0 {
			t.Errorf("%s: Check reported %q and fatal %q, and want it fatal and nothing else", name, r.errors, r.fatal)
		}
	}
}

// What it costs when this fails: testkit, which holds the check, keeps shell in its own Taskfile that
// neither passes the check nor is listed with its reason.
func TestTestkitsOwnTaskfilePassesOrListsItsExceptions(t *testing.T) {
	t.Parallel()
	Check(t, "..")
}
