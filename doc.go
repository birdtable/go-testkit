// Package testkit gives a Go test process and its children an environment of their own on Linux,
// macOS and Windows: an allow-listed environment and home, a built program to run, a rerun of the
// test binary, probes for what the system refuses, and a deadline scaled to the machine. The
// containment of a test binary is the sub-module github.com/birdtable/go-testkit/contain, which plugs
// into Main by the options Before and RerunEnv.
//
// The isolation clears the environment of the process that calls Main or Isolate: one that cannot
// reach the person's home, their git or tool settings, their go env file, or a token or a hook's
// variables the run was started with. It is for tests only, so no program's own code imports it.
//
// The rule is an allow list. Only the names of kept pass from the run into a test or its child as
// the run set them; every other name leaks, whatever it is called, so a token named in no list and a
// variable git reads are dropped alike. Isolate then sets what a test needs: each folder of homes
// and git's global configuration under one folder of its own, removed after the tests, and the go
// tool's caches pinned where the run's go had them. Two safe defaults of the isolation are set
// whatever the run had: no system configuration of git's, and go's telemetry off in its folder. The
// go workspace is the caller's: a run's GOWORK passes or leaks as Keep and Drop say, like any other
// name, so a module whose tests want no workspace sets GOWORK=off and keeps it. GOENV is off, so a
// test's go reads no env file of the person's and cannot change it, and takes the settings of go env
// -w from the environment and the toolchain's own go.env.
//
// It is built on go test's own practice, a TestMain that calls Main, and adds the isolation go test
// leaves out. The app sets it by Main's and Isolate's options: Keep lets a named variable through
// the allow list, Drop makes a prefix leak, and GoOffline gives every go a test starts no module
// proxy, direct fetch, checksum server or toolchain download. NoNetwork holds the test process and
// its children to loopback on macOS and Linux, and fails the run where it cannot. A package's test of
// its own isolation calls Isolated. Build builds a program under test once and runs it; the isolation starts none.
// One test at the module's root calls RequireMain, which fails each package of the module whose
// TestMain does not call Main, so no package's tests are left in the person's environment.
//
// The module imports the standard library only. Its list of the home, temp and XDG_ names is its
// own, internal/homevars; go-proc keeps the same list for proc.HomeEnv, and a go-proc test holds the
// two equal.
package testkit
