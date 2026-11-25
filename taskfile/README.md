# testkit/taskfile

Holds a repository's Taskfile.yml to Go: a task's commands call a Go program, or one binary with its
arguments, and keep their logic, paths and temporary folders in Go. go-task runs the shell in
Taskfile.yml through its own interpreter, which runs it differently on Windows (it cannot append an
external program's output with `>>` there, and its `tar` takes no `-C`), and no vet, build or lint
reads it.

`github.com/birdtable/go-testkit/taskfile` is a module of its own beside `github.com/birdtable/go-testkit`,
so that the root keeps to the standard library. It reads YAML with `go.yaml.in/yaml/v3` and shell with
`mvdan.cc/sh/v3`'s parser, the one go-task's interpreter is built on (NOTICE). A repository takes it as
a test-only requirement and calls it from a test:

```go
func TestTheTaskfileCallsGo(t *testing.T) {
	t.Parallel()
	taskfile.Check(t, "../..") // the folder that holds Taskfile.yml
}
```

It is built on `mvdan.cc/sh/v3`'s parser and adds the check go-task leaves out: that a command keeps
no logic in shell. The repository configures the folder it passes to `Check` and its
`taskfile-exceptions.txt`, below.

## What it refuses

`Check` reads each command of each task's `cmds` (a `cmd:`, a plain command and a `defer:`), with every
`{{.NAME}}` of a plain var put in first, and fails the test for each of these:

- a shell utility run as a command: `mktemp`, `grep`, `sed`, `awk`, `cut`, `tar`, `find` or `xargs`;
- a pipe;
- a redirection to or from a file, or a here-document. One to or from the null device (`/dev/null`,
  `NUL`) passes, and so does one that points a descriptor at another (`2>&1`);
- a `for` loop in shell. go-task's own `for:` is not shell, and passes;
- a `/tmp` path;
- a path joined in shell: a variable's, a command's or a template's value with `/` and more after it,
  as `"$d/out"` or `{{.ROOT_DIR}}/contain`, which Go joins with `path/filepath`.

Shell go-task could not read is refused too. A var that holds a tool's command, as
`TOOL: go tool -modfile=tools`, reads as the literal path it puts in, so `{{.TOOL}}/task.mod` passes.
go-task's own variables and a var set by `sh:` read as a variable.

Not read: a task's `preconditions`, `status`, `sources` and `generates`, a var's `sh:`, an included
Taskfile, and a script a command runs or sources.

## What a repository keeps in shell

`taskfile-exceptions.txt`, beside Taskfile.yml, lists each command a repository keeps in shell, a line
each: the task, the command's place in its `cmds` counted from 1, and the reason, tab-separated. A line
that starts with `#` and a blank line are not read. A line passes everything the check finds in its
command, and no other command. A line that names no command that runs shell, or a command where the
check finds nothing, fails the test, so the file lists what is still shell and no more.

## Working here

taskfile's members are the root Taskfile's `taskfile-*` tasks, which run in this folder. Its API gate
files are in `api/`, and its tags are `taskfile/v*`.

Licensed under Apache-2.0; see LICENSE.
