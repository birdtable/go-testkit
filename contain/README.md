# testkit/contain

Runs a test binary inside a containment, so a test that sends a signal reaches no process outside it.

`github.com/birdtable/go-testkit/contain` is a module of its own beside `github.com/birdtable/go-testkit`. It is
an option of testkit's `Main`, and requires `github.com/birdtable/go-testkit` at main's ebf77cb until
v0.5.0 is tagged; its release v0.1.2 requires v0.3.1:

    go get github.com/birdtable/go-testkit/contain


```go
func TestMain(m *testing.M) {
	code, err := testkit.Main(m, contain.Contain())
	if err != nil {
		panic(err)
	}
	os.Exit(code)
}
```

It is built on testkit's `Main`, and adds, on Linux, an init that reaps what the tests orphan, tests
that hold no capability, and a skip, with the reason, of each test that needs the containment where
there is none. The app configures `Contain` or `ContainMounts` in its `TestMain`, and `Contained` or
`ContainedMounts` in each test that signals or mounts.

It is a module apart so that a test binary that only isolates links none of it. On Linux it calls
`kernel.org/pub/linux/libs/security/libcap/cap`, and in a build with cgo, as every `-race` build is, the C
of `libcap/psx`. It takes no other module, and no `golang.org/x/sys`. The two are taken under the
BSD-3-Clause half of their licence (NOTICE).

## Containing the tests that signal

`testkit.Main(m, contain.Contain())` reruns the whole test binary inside a containment before any test
runs, so a signal a test sends reaches no process outside it. It gives the rerun the process's arguments,
working folder, streams and environment, and returns its exit code without running a test itself.

| system | the containment | `ContainMounts` | refused, and `Contained` skips with `Refused` |
|---|---|---|---|
| Linux | The binary reruns as PID 1 of a user and PID namespace with a private mount and a fresh `/proc`, mapping the account it ran as to itself. PID 1 starts the tests as its child, forwards the signals sent it, reaps every process the tests orphan, and ends with the tests' code, which ends the namespace. The tests hold no capability. | The tests keep `CAP_SYS_ADMIN` of the namespace, and pass it to nothing they start. | A host that refuses a user or PID namespace or a fresh `/proc`, as many CI containers do, and Ubuntu's AppArmor does unless `kernel.apparmor_restrict_unprivileged_userns` is 0. In a run as uid 0, a test binary a test starts. Under `ContainMounts`, a capability that could not be set on every thread, and a process a test started (`ContainedMounts`). |
| Windows | The binary reruns in a Job Object that kills its processes when the Job closes, in a process group of its own and a console with no window. The child waits until it is in the Job before it runs. | `Contain`; `ContainedMounts` skips with `NoContainment`. | A Job Object that cannot be made, a rerun that cannot start, or a child that cannot be put in the Job. |
| macOS, and any other system | Nothing is made, and `Contained` skips with `NoContainment`. | `Contain`; `ContainedMounts` skips with `NoContainment`. | none |

A test that signals a process calls `contain.Contained(t)` first. It checks the containment itself, never a
variable the test could set, and skips with the reason where there is none: `NotAsked` (`Main` was not
given `Contain`), `NoContainment`, or `Refused` and the system's own words, as on a host that refuses user
namespaces, as many CI containers do. A host that refuses the containment runs the tests that do not call
`Contained` uncontained, and skips those that do, so no signalling test runs without it. A child of
testkit's `Rerun` inside a contained run is inside the same containment, not in a new one.

On Linux the tests of a contained run hold no capability of the namespace, as uid 0 too:

- PID 1 drops what it held on the one thread that starts the tests, a `cap.Launcher`'s, and sets and locks
  `SECBIT_NOROOT` and `SECBIT_NO_SETUID_FIXUP` first. So a test binary run as root, or under
  `unshare --map-root-user`, gives its tests and what they start nothing back on execve.
- It empties the bounding set, so a program with file capabilities that a test starts gains none of them.
  One whose file capabilities carry the effective flag, as a distribution's `ping` or `newuidmap` does, is
  refused at execve with `EPERM` instead.
- PID 1 makes itself not dumpable, so no test and nothing it starts opens its memory or maps, or ptraces it.
- `no_new_privs` is left as the run has it: a program a test starts has the test's own.

A test of such a uid-0 run cannot contain a test binary it starts (Linux maps uid 0 of a new user namespace
to uid 0 only with `CAP_SETFCAP`), and that binary's `Contained` skips with `Refused`.

### Tests that mount

`testkit.Main(m, contain.ContainMounts())` is `Contain`, and on Linux the tests keep `CAP_SYS_ADMIN` of the
namespace's own user namespace, effective and permitted, so a test can mount a tmpfs or a bind mount in the
run's private mount namespace. It holds in a test binary built with cgo or without, and so under `-race`:
libcap/cap sets a capability on every thread in both.

It reaches no host mount and no host capability: any process the host lets make a user namespace can hold
that capability in one of its own. No other capability is kept, and nothing the tests start inherits this
one:

- From contain's initialization, which runs before any package that imports contain and before `TestMain`,
  the ambient and inheritable sets are empty on every thread, and `SECBIT_NO_CAP_AMBIENT_RAISE` is set and
  locked. A package that does not import contain may initialize before it, and a program that package's
  initialization starts holds the capability.
- PID 1 also sets `no_new_privs` before it starts the tests, so no execve in the run gains from file
  capabilities or set-id bits. Plain `Contain` does not, so that a test of a program's own `no_new_privs`
  reads what the test set.

A test that unmounts the run's `/proc` leaves a process started after it (a `Rerun` child) finding itself
not contained.

A test that mounts calls `contain.ContainedMounts(t)` first, which skips, with the reason, unless the run
holds the capability: `Contain` was given and not `ContainMounts`, the run is not contained, or `Refused`
and why. Elsewhere than Linux, `ContainMounts` is `Contain` and `ContainedMounts` skips with
`NoContainment`.
