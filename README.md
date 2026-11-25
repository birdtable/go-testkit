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

## No network

`testkit.NoNetwork()` holds the test process, and every process it starts, to loopback:

```go
code, err := testkit.Main(m, testkit.NoNetwork())
```

A dial to any other address fails with an error that names it, and an `httptest` server works.
Main sets the guard before any test runs, and proves it: a connect to `192.0.2.1:9` must be refused
by the guard, and one to a listener on `127.0.0.1` must succeed. On macOS the guard is the system
sandbox, set on the test process with `sandbox_init`. On Linux the test binary reruns in a user and
network namespace of its own, with only loopback up. The guard is tested on macOS arm64 and Linux
amd64. On macOS amd64 it is built and vetted only.

Where the guard cannot be set, no test runs, and Main's error says why. `NoNetwork("windows")` names
a system where you accept running the tests with the network open. There Main prints one line to
stderr saying so, and runs them. The refusals:

| Refusal | Where | Main's error says | Test |
|---|---|---|---|
| A name in `NoNetwork(...)` that is no GOOS | every system | `"macos", which is no GOOS` | `TestNoNetworkRefusesANameThatIsNoGOOS` |
| The system has no guard | Windows, and all but macOS and Linux | `Windows gives an unprivileged process no way to refuse outbound connections and keep loopback` | `TestNoNetworkFailsMainNamingTheSystemWhereTheGuardCannotBeSet` |
| The process already runs in a sandbox | macOS (`sandbox-exec`, an agent's sandboxed shell) | `sandbox_init refused the profile`, and that the process already runs inside a sandbox | `TestNoNetworkFailsMainNamingTheSystemWhereTheGuardCannotBeSet` |
| The host refuses an unprivileged user namespace | Linux | `the system refused the namespace` | `TestNoNetworkFailsMainNamingTheSystemWhereTheGuardCannotBeSet` |
| Loopback does not come up in the new namespace | Linux (Ubuntu's `kernel.apparmor_restrict_unprivileged_userns=1`) | `loopback could not be brought up in the new network namespace` | `TestALoopbackThatWillNotComeUpRefusesTheGuard` |
| The new namespace is the run's own | Linux | `the new network namespace is the one the run started in` | `TestAGuardLeftInTheRunsNetworkNamespaceRefuses` |
| The probe's connect was made, or failed some other way | every guarded system | `the probe's connection to 192.0.2.1:9 was made`, or `failed, but not with the guard's` | `TestTheProbeFailsAGuardThatHoldsNothing` |
| The probe cannot reach loopback | every guarded system | `the probe could not reach its listener on loopback` | none: no guard of testkit's closes loopback |

What it does not promise: a red test where the code under test swallows the error. Also outside the
guard is a daemon reached over a Unix socket, which connects for itself: Docker's socket, or on Linux
the C library's resolver through nscd or systemd-resolved. On macOS the sandbox counts every address
of the host as localhost, so a connect to one of the host's own addresses is allowed. No packet of it
leaves the machine.

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
