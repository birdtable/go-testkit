package testkit

import (
	"fmt"
	"strings"
	"testing"
)

// TestEveryPackageOfTheModuleCallsMain: every package of testkit's root module that has tests calls
// Main from its TestMain on darwin, linux and windows, and none opts out: internal/homevars, which the
// root package imports, calls it from its external test package.
//
// What it costs when this fails: a package of testkit's own runs its tests in the person's
// environment, with their home, their git settings and their tokens.
func TestEveryPackageOfTheModuleCallsMain(t *testing.T) {
	t.Parallel()
	r := record(t, func(tb testing.TB) {
		tb.Helper()
		RequireMain(tb)
	})
	if r.failed != "" || r.skipped != "" {
		t.Errorf("RequireMain of testkit's module failed with %q and skipped %q, want neither", r.failed, r.skipped)
	}
	if strings.Contains(r.logged, "unisolated") {
		t.Errorf("RequireMain logged %q, want no package of testkit's module to opt out", r.logged)
	}
}

// requireTree is a module of a package for each thing RequireMain finds: a TestMain that calls Main
// under the import's own name, under an alias in the external test package, and under a dot import;
// none; one that calls Main through a wrapper; one that calls the Main of another package imported as
// testkit; one only behind a build tag, one only in a _linux_test.go file, and one only in an
// _amd64_test.go file; one whose call to Main never runs; a package built only on linux; an opt-out,
// one with no reason, and one only in a _linux_test.go file; and packages with no tests, one of them
// built only on windows. None of it is built: go list and the parser read it.
func requireTree() map[string]string {
	const imp = "\"github.com/birdtable/go-testkit\""
	main := func(pkg, imports, call string) string {
		return "package " + pkg + "\n\nimport (\n\t\"os\"\n\t\"testing\"\n\n\t" + imports + "\n)\n\nfunc TestMain(m *testing.M) {\n\tcode, _ := " + call + "(m)\n\tos.Exit(code)\n}\n"
	}
	test := func(pkg, above string) string {
		return above + "package " + pkg + "\n\nimport \"testing\"\n\nfunc TestX(t *testing.T) {}\n"
	}
	return map[string]string{
		"go.mod":                       "module example.invalid/m\n\ngo 1.27\n",
		"good/good.go":                 "package good\n",
		"good/good_test.go":            main("good", imp, "testkit.Main"),
		"alias/alias.go":               "package alias\n",
		"alias/alias_test.go":          main("alias_test", "tk "+imp, "tk.Main"),
		"dot/dot_test.go":              main("dot", ". "+imp, "Main"),
		"none/none.go":                 "package none\n",
		"none/none_test.go":            test("none", ""),
		"wrapper/wrapper_test.go":      main("wrapper", imp, "run") + "\nfunc run(m *testing.M) (int, error) {\n\treturn testkit.Main(m)\n}\n",
		"fakekit/fakekit.go":           "package fakekit\n\nfunc Main(any) (int, error) { return 0, nil }\n",
		"impostor/impostor_test.go":    main("impostor", "testkit \"example.invalid/m/fakekit\"", "testkit.Main"),
		"tagged/tagged.go":             "package tagged\n",
		"tagged/main_test.go":          "//go:build integration\n\n" + main("tagged", imp, "testkit.Main"),
		"tagged/x_test.go":             test("tagged", ""),
		"linuxonly/linuxonly.go":       "package linuxonly\n",
		"linuxonly/main_linux_test.go": main("linuxonly", imp, "testkit.Main"),
		"linuxonly/x_test.go":          test("linuxonly", ""),
		"amdonly/main_amd64_test.go":   main("amdonly", imp, "testkit.Main"),
		"amdonly/x_test.go":            test("amdonly", ""),
		"neverruns/neverruns_test.go":  "package neverruns\n\nimport (\n\t\"os\"\n\t\"testing\"\n\n\t" + imp + "\n)\n\nfunc TestMain(m *testing.M) {\n\tos.Exit(m.Run())\n\t_, _ = testkit.Main(m)\n}\n",
		"lin/lin_linux.go":             "package lin\n",
		"lin/lin_linux_test.go":        main("lin", imp, "testkit.Main"),
		"optout/optout_test.go":        test("optout", "//testkit:unisolated it reads only strings\n\n"),
		"emptyopt/emptyopt_test.go":    test("emptyopt", "//testkit:unisolated\n\n"),
		"optlinux/x_test.go":           test("optlinux", ""),
		"optlinux/opt_linux_test.go":   test("optlinux", "// Its linux tests start nothing.\n//\n//testkit:unisolated its linux tests start nothing\n\n"),
		"notests/notests.go":           "package notests\n",
		"winonly/winonly_windows.go":   "package winonly\n",
	}
}

// TestRequireMainFailsEachPackageWhoseTestMainIsMissingOrDoesNotCallMain: RequireMain of a planted
// module fails, for each system it holds on, a package with no TestMain, one whose TestMain calls Main
// through a wrapper or calls another package's Main imported as testkit, one whose only TestMain a
// build tag leaves out, naming the file and its line, one whose only TestMain is in a _linux_test.go
// file, on darwin and windows alone, naming the file, and an opt-out with no reason. It passes a
// TestMain that calls Main by the import's own name, an alias in an external test package and a dot
// import, one in an _amd64_test.go file on any host, one whose call never runs, an opt-out, which it
// logs, and a package with no tests on a system, and patterns narrow it, one naming a package built
// only on linux.
// The planted red of each failing row is its package passing; of each passing row, its package named.
//
// What it costs when this fails: a package whose TestMain is missing, left out on a system or a
// wrapper's passes the check, and its tests run in the person's environment; or one that calls Main
// fails it, and the check is turned off.
func TestRequireMainFailsEachPackageWhoseTestMainIsMissingOrDoesNotCallMain(t *testing.T) {
	// serial: t.Chdir into the planted module.
	dir := t.TempDir()
	writeTree(t, dir, requireTree())
	t.Chdir(dir)
	r := record(t, func(tb testing.TB) {
		tb.Helper()
		RequireMain(tb)
	})
	if r.skipped != "" {
		t.Errorf("RequireMain skipped with %q, and it never skips", r.skipped)
	}
	const all = "darwin, linux, windows"
	want := []string{
		"example.invalid/m/emptyopt on " + all + ": the //testkit:unisolated line in emptyopt_test.go gives no reason",
		"example.invalid/m/impostor on " + all + ": its TestMain, in impostor_test.go, does not call testkit.Main itself",
		"example.invalid/m/linuxonly on darwin, windows: no TestMain is built for it there: the one in main_linux_test.go is left out by its file name",
		"example.invalid/m/none on " + all + ": it has tests and no TestMain",
		"example.invalid/m/optlinux on darwin, windows: it has tests and no TestMain",
		"example.invalid/m/tagged on " + all + ": no TestMain is built for it there: the one in main_test.go is left out by //go:build integration",
		"example.invalid/m/wrapper on " + all + ": its TestMain, in wrapper_test.go, does not call testkit.Main itself",
	}
	for _, w := range want {
		if !strings.Contains(r.failed, "testkit: RequireMain: "+w) {
			t.Errorf("RequireMain failed with:\n%s\nwant it to hold %q", r.failed, w)
		}
	}
	if n := strings.Count(r.failed, "testkit: RequireMain: "); n != len(want) {
		t.Errorf("RequireMain failed %d times, want %d:\n%s", n, len(want), r.failed)
	}
	for _, pkg := range []string{"good", "alias", "dot", "amdonly", "neverruns", "lin", "optout", "fakekit", "notests", "winonly"} {
		if strings.Contains(r.failed, "example.invalid/m/"+pkg+" ") {
			t.Errorf("RequireMain failed example.invalid/m/%s, which calls Main, opts out or has no tests there:\n%s", pkg, r.failed)
		}
	}
	for _, w := range []string{
		"testkit: RequireMain: example.invalid/m/optout is unisolated on " + all + ": it reads only strings\n",
		"testkit: RequireMain: example.invalid/m/optlinux is unisolated on linux: its linux tests start nothing\n",
	} {
		if !strings.Contains(r.logged, w) {
			t.Errorf("RequireMain logged:\n%s\nwant it to hold %q", r.logged, w)
		}
	}
	narrowed := record(t, func(tb testing.TB) {
		tb.Helper()
		RequireMain(tb, "./good", "./alias/...", "./optout", "./lin")
	})
	if narrowed.failed != "" || narrowed.skipped != "" || !strings.Contains(narrowed.logged, "example.invalid/m/optout is unisolated") {
		t.Errorf("RequireMain of ./good, ./alias/..., ./optout and ./lin failed with %q, skipped %q and logged %q, want only optout's opt-out logged", narrowed.failed, narrowed.skipped, narrowed.logged)
	}
}

// TestRequireMainRefusesWhatItCannotReadAndNeverSkips: RequireMain fails the test, and never skips
// it, for a pattern that starts with -, an empty pattern, a folder with no go.mod above it, and no
// Isolate in force. The planted red of each row is RequireMain returning.
//
// What it costs when this fails: go list reads a pattern as a flag such as -toolexec, a check that
// lists nothing passes, or go list reads the person's settings.
func TestRequireMainRefusesWhatItCannotReadAndNeverSkips(t *testing.T) {
	// serial: t.Chdir into each row's folder, and t.Setenv for the last.
	mod := t.TempDir()
	writeTree(t, mod, map[string]string{"go.mod": "module example.invalid/m\n\ngo 1.27\n", "a/a.go": "package a\n"})
	refused := func(row string, patterns []string, want string) {
		t.Helper()
		returned := false
		r := record(t, func(tb testing.TB) {
			tb.Helper()
			RequireMain(tb, patterns...)
			returned = true
		})
		if returned || r.skipped != "" || !strings.Contains(r.failed, want) {
			t.Errorf("%s: RequireMain returned %v, skipped %q and failed with %q, want it to fail the test with %q", row, returned, r.skipped, r.failed, want)
		}
	}
	t.Chdir(mod)
	refused("a pattern that starts with -", []string{"./a", "-toolexec=elsewhere"}, fmt.Sprintf("refused the pattern %q", "-toolexec=elsewhere"))
	refused("an empty pattern", []string{""}, `refused the pattern ""`)
	t.Chdir(t.TempDir())
	refused("no go.mod", nil, "has no go.mod above it")
	t.Chdir(mod)
	t.Setenv("HOME", t.TempDir())
	refused("no Isolate", nil, "no Isolate is in force")
}

// TestRequireMainUnderGoOfflineFailsAModuleTheCacheLacksAndNamesIt: a module that needs a module the
// module cache lacks, checked under Main given GoOffline, fails the test with go's GOPROXY=off line
// naming the module, never skipping it, and nothing is fetched.
//
// What it costs when this fails: the check reaches the network for the module's requirements, or
// passes a module whose packages go list could not read.
func TestRequireMainUnderGoOfflineFailsAModuleTheCacheLacksAndNamesIt(t *testing.T) {
	// serial: t.Chdir into the module, and run clears the process's environment.
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"go.mod":      "module example.invalid/needs\n\ngo 1.27\n\nrequire example.invalid/x v1.0.0\n",
		"go.sum":      "example.invalid/x v1.0.0 h1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=\nexample.invalid/x v1.0.0/go.mod h1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=\n",
		"a/a.go":      "package a\n\nimport _ \"example.invalid/x\"\n",
		"a/a_test.go": "package a\n\nimport \"testing\"\n\nfunc TestX(t *testing.T) {}\n",
	})
	t.Chdir(dir)
	var r *recorder
	code, err := run(runner(func() int {
		r = record(t, func(tb testing.TB) {
			tb.Helper()
			RequireMain(tb)
		})
		return 0
	}), offlineRun(), []Option{GoOffline()})
	if err != nil || code != 0 {
		t.Fatalf("run: code %d, %v", code, err)
	}
	if r.skipped != "" || !strings.Contains(r.failed, "example.invalid/x") || !strings.Contains(r.failed, "module lookup disabled by GOPROXY=off") {
		t.Errorf("RequireMain under GoOffline failed with %q and skipped %q, want go's GOPROXY=off line naming example.invalid/x and no skip", r.failed, r.skipped)
	}
	for _, d := range []string{"go-get=1", "https://"} {
		if strings.Contains(r.failed, d) {
			t.Errorf("RequireMain printed %q, and GoOffline fetches nothing:\n%s", d, r.failed)
		}
	}
}
