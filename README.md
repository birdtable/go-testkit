<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/mark/go-testkit-banner-dark.svg">
    <source media="(prefers-color-scheme: light)" srcset="docs/mark/go-testkit-banner-light.svg">
    <img src="docs/mark/go-testkit-banner-light.svg" width="327" alt="go-testkit">
  </picture>
</p>

# go-testkit

Go tests that run in an environment of their own.

<p>
  <a href="https://github.com/birdtable/go-testkit/releases"><img src="https://img.shields.io/github/v/release/birdtable/go-testkit?filter=v*" alt="Latest Release"></a>
  <a href="https://pkg.go.dev/github.com/birdtable/go-testkit"><img src="https://pkg.go.dev/badge/github.com/birdtable/go-testkit.svg" alt="Go Reference"></a>
  <a href="LICENSE"><img src="https://img.shields.io/github/license/birdtable/go-testkit.svg" alt="License"></a>
</p>

Your tests should not see your home folder, your git settings or the token in your shell, and they
should behave the same on Linux, macOS and Windows. go-testkit gives a test process and its children
a clean, allow-listed environment, and adds the helpers that tests of programs keep needing:
stand-in programs, a bounded rerun of the test binary, probes for what the system refuses, and
containment.

## Install

```bash
go get github.com/birdtable/go-testkit
```

The root module needs nothing beyond the standard library.

## Quick start

Call `Main` from your package's `TestMain`. Every test then runs where the person's home, git
settings and tokens cannot reach it.

```go
package quickstart_test

import (
	"os"
	"testing"

	"github.com/birdtable/go-testkit"
)

// TestMain runs the package's tests in an environment of their own,
// and fails the run if a test leaks a goroutine.
func TestMain(m *testing.M) {
	code, err := testkit.Main(m)
	if err != nil {
		panic(err)
	}
	os.Exit(testkit.NoLeaks(code))
}

// Each test sees a home, a temp folder and git settings of the run's
// own, and none of the tokens or settings of the person who ran it.
func TestTheEnvironmentIsTheRunsOwn(t *testing.T) {
	t.Parallel()
	testkit.Isolated(t)
}
```

Need a variable through? Pass `testkit.Keep("MY_VAR")` to `Main`. Want no go a test starts to
fetch anything? Pass `testkit.GoOffline()`.

## What's inside

| Package | What it does |
|---|---|
| [`testkit`](https://pkg.go.dev/github.com/birdtable/go-testkit) | Runs a package's tests in an environment of their own, with checks (`NoLeaks`, `NoOpenFiles`, `Isolated`), a bounded rerun (`Rerun`), probes (`Link`, `Modes`, `OtherAccount`) and `Scale` for waits under `-race` and load. |
| [`fake`](https://pkg.go.dev/github.com/birdtable/go-testkit/fake) | Builds a stand-in program that prints, writes, runs and exits as told, the same on every system, in place of a shell script. |
| [`contain`](contain) | A module of its own: reruns the test binary inside a containment, so a test that signals or mounts reaches nothing outside it. |
| [`taskfile`](taskfile) | A module of its own: checks in a test that a repository's Taskfile.yml calls Go and keeps no logic in shell. |
| [`cmd/apigate`](https://pkg.go.dev/github.com/birdtable/go-testkit/cmd/apigate) | The API gate of one module: the systems differ only as declared, breaking changes are listed, and the stability marks hold. |

`contain` and `taskfile` are separate modules, so a test binary that only isolates links none of
what they need:

```bash
go get github.com/birdtable/go-testkit/contain    # tests that signal or mount
go get github.com/birdtable/go-testkit/taskfile   # the test that checks Taskfile.yml
```

### Built on

The root module, `testkit`, `fake` and `cmd/apigate`, imports the standard library only. The other
two modules require what they are built on:

- [`kernel.org/pub/linux/libs/security/libcap/cap`](https://pkg.go.dev/kernel.org/pub/linux/libs/security/libcap/cap),
  in `contain`: the capability calls on Linux.
- [`github.com/birdtable/go-testkit`](https://pkg.go.dev/github.com/birdtable/go-testkit), in `contain`:
  the `Main` that `contain` is given to.
- [`go.yaml.in/yaml/v3`](https://pkg.go.dev/go.yaml.in/yaml/v3), in `taskfile`: reads Taskfile.yml.
- [`mvdan.cc/sh/v3`](https://pkg.go.dev/mvdan.cc/sh/v3), in `taskfile`: parses each command's shell,
  as go-task's interpreter does.

## A stand-in program

`fake` puts a small built program where your code expects a real one:

```go
tool := fake.Program(t, t.TempDir(), "tool", fake.PrintArgs(), fake.Exit(3))
```

Run `tool` as you would the real thing. It prints its arguments and exits 3, and no shell reads it.
A product adds steps of its own through `fake.Custom` and `fake.From`.

## Systems

Each release is tested on macOS (arm64), Linux (amd64) and Windows (amd64), and built and vetted
for arm64 on Linux and Windows. AIX, illumos and Solaris are built, vetted and linted too, and no
test runs there.

Where a system cannot do something, go-testkit skips the test with a named reason, and never fails
it. The main ones:

- **Windows:** `Link` skips with `NoLinks` where Developer Mode is off, `Modes` skips with `NoModes`,
  and `Running.Terminate` returns an error that wraps `errors.ErrUnsupported`.
- **AIX, illumos and Solaris:** `HoldRead` skips with `NoHold`.
- **`contain`** works on Linux and Windows. On macOS and any other system, and where a host refuses
  user namespaces, `contain.Contained` skips with the reason. `ContainMounts` keeps the mount
  capability on Linux alone: elsewhere it is `Contain`, and `contain.ContainedMounts` skips with
  `NoContainment`. [contain/README.md](contain/README.md) has the details.

Each call's doc on [pkg.go.dev](https://pkg.go.dev/github.com/birdtable/go-testkit) says what
differs by system.

## Stability

v0: the API may change between minor versions.

## Contributing

Issues and pull requests are welcome. [CONTRIBUTING.md](CONTRIBUTING.md) says how to run the tests.

## Security

Please report a vulnerability privately. [SECURITY.md](SECURITY.md) says how.

## License

[Apache-2.0](LICENSE)
