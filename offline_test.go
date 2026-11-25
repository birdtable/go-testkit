package testkit

import (
	"archive/zip"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// offlineRun is a start a run of these rows is given: planted, less any go setting of offline's,
// with each of them set to a value of the run's own that reaches the network.
func offlineRun() []string {
	start := slices.DeleteFunc(planted(), func(kv string) bool {
		k, _, _ := strings.Cut(kv, "=")
		return isOffline(k)
	})
	return append(start, "GOPROXY=https://proxy.example", "GONOPROXY=example.com", "GOSUMDB=sum.example", "GOTOOLCHAIN=go1.27.4+path")
}

// offlineNames is the name of each setting of offline, in its order.
func offlineNames() []string {
	var out []string
	for _, kv := range offline() {
		k, _, _ := strings.Cut(kv, "=")
		out = append(out, k)
	}
	return out
}

// TestGoOfflineWinsOverTheRunAKeepAndADropAndRestorePutsTheRunsBack: under Main given GoOffline, the
// test process holds GOPROXY=off, GONOPROXY=none, GOSUMDB=off and GOTOOLCHAIN=local whatever the run
// held and whatever a Keep of the four or a Drop of GO says, and a child Clean gives them holds the
// same; a test that changes one or unsets another in its process still gives its child offline's
// value of each; and after the run each holds the value it had before. The planted red is the same
// run with the Keep and no GoOffline, whose child holds the run's four values.
//
// What it costs when this fails: a go a test starts asks a proxy, a module's own host, a checksum
// server or the toolchain download of the run's choosing, or a test binary leaves GOPROXY=off in the
// process after its tests.
func TestGoOfflineWinsOverTheRunAKeepAndADropAndRestorePutsTheRunsBack(t *testing.T) {
	// serial: run clears the process's environment, which every test reads; its restore puts it back.
	start, names := offlineRun(), offlineNames()
	before := byName(os.Environ())
	ran := 0
	for row, tc := range map[string]struct {
		opts    []Option
		offline bool
	}{
		"GoOffline and a Keep of the four":    {[]Option{GoOffline(), Keep(names...)}, true},
		"a Keep in Options, then GoOffline":   {[]Option{Options(Keep(names...)), GoOffline()}, true},
		"GoOffline and a Drop of GO":          {[]Option{Drop("GO"), GoOffline()}, true},
		"a Keep of the four and no GoOffline": {[]Option{Keep(names...)}, false},
	} {
		ran++
		var inside, given, changed map[string]string
		code, err := run(runner(func() int {
			inside, given = byName(os.Environ()), child(t, Clean(os.Environ()))
			if err := os.Setenv("GOPROXY", "https://changed.example"); err != nil {
				t.Fatal(err)
			}
			if err := os.Unsetenv("GOSUMDB"); err != nil {
				t.Fatal(err)
			}
			changed = byName(Clean(os.Environ()))
			return 0
		}), start, tc.opts)
		if err != nil || code != 0 {
			t.Fatalf("%s: run: code %d, %v", row, code, err)
		}
		run := byName(start)
		for _, kv := range offline() {
			k, off, _ := strings.Cut(kv, "=")
			want := run[k]
			if tc.offline {
				want = off
			}
			if inside[k] != want || given[k] != want {
				t.Errorf("%s: %s is %q in the test process and %q in its child, want %q", row, k, inside[k], given[k], want)
			}
			if tc.offline && changed[k] != off {
				t.Errorf("%s: after the test changed GOPROXY and unset GOSUMDB, Clean gives %s=%q, want %q", row, k, changed[k], off)
			}
		}
		after := byName(os.Environ())
		for _, k := range names {
			if after[k] != before[k] {
				t.Errorf("%s: after the run %s is %q, want %q, what it was", row, k, after[k], before[k])
			}
		}
	}
	if ran != 4 {
		t.Fatalf("ran %d rows, want 4", ran)
	}
}

// TestCleanGivesNoOfflineSettingWithNoGoOfflineOrNoIsolation: Clean adds a setting of offline only
// under an isolation given GoOffline; with no GoOffline it passes the four by the leak rule, and with
// no isolation in force it adds none.
//
// What it costs when this fails: a run that never asked for GoOffline finds its children offline,
// or a child of a test outside any isolation is handed settings no one gave.
func TestCleanGivesNoOfflineSettingWithNoGoOfflineOrNoIsolation(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	environ := []string{"PATH=/planted/bin", "GOPROXY=https://proxy.example"}
	for row, in := range map[string]isolated{
		"no GoOffline":                  {set: ownSet(dir), own: dir},
		"no isolation, GoOffline given": {conf: confOf([]Option{GoOffline()})},
	} {
		if got := in.clean(environ); !slices.Equal(got, []string{"PATH=/planted/bin"}) {
			t.Errorf("%s: Clean gives %q, want PATH alone", row, got)
		}
	}
	in := isolated{set: withOffline(ownSet(dir), confOf([]Option{GoOffline()})), own: dir, conf: confOf([]Option{GoOffline()})}
	if got, want := in.clean(environ), append([]string{"PATH=/planted/bin"}, offline()...); !slices.Equal(got, want) {
		t.Errorf("under GoOffline Clean gives %q, want %q", got, want)
	}
}

// plantCachedModule writes example.invalid/dep v1.0.0 into the module cache cache as go's download
// leaves it, its .info, .mod and .zip and no .ziphash, which go makes again from the zip with no
// download: a module only the cache holds.
func plantCachedModule(t *testing.T, cache string) {
	t.Helper()
	at := filepath.Join(cache, "cache", "download", "example.invalid", "dep", "@v")
	if err := os.MkdirAll(at, 0o700); err != nil {
		t.Fatal(err)
	}
	gomod := "module example.invalid/dep\n\ngo 1.27\n"
	for name, body := range map[string]string{
		"v1.0.0.info": `{"Version":"v1.0.0","Time":"2026-01-01T00:00:00Z"}`,
		"v1.0.0.mod":  gomod,
	} {
		if err := os.WriteFile(filepath.Join(at, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	f, err := os.Create(filepath.Join(at, "v1.0.0.zip"))
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(f)
	for name, body := range map[string]string{"go.mod": gomod, "dep.go": "package dep\n\n// Answer is the dependency's one value.\nconst Answer = 42\n"} {
		w, err := z.Create("example.invalid/dep@v1.0.0/" + name)
		if err == nil {
			_, err = w.Write([]byte(body))
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// plantModule writes a main package whose go.mod is gomod, importing imp unless it is empty, into a
// folder of the test's own, and returns the folder. go looks a requirement up only for a package
// a build imports, and adds a go.sum line for it only under -mod=mod.
func plantModule(t *testing.T, gomod, imp string) string {
	t.Helper()
	dir := t.TempDir()
	src := "package main\n\nfunc main() {}\n"
	if imp != "" {
		src = "package main\n\nimport _ \"" + imp + "\"\n\nfunc main() {}\n"
	}
	for name, body := range map[string]string{"go.mod": gomod, "main.go": src} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestAGoUnderGoOfflineFetchesNothingAndBuildsFromTheCache: the go on PATH, started by a test under
// Main given GoOffline with the environment Clean gives, fails a module whose requirement the cache
// lacks with go's GOPROXY=off line, the same with GOPRIVATE naming the module and kept, where go
// would otherwise fetch it from its own host; fails a go.mod whose go line names a release the run
// lacks with GOTOOLCHAIN=local and no download; builds a module whose requirement only the run's
// pinned module cache holds; and reports GOPROXY=off though the run kept a proxy of its own. The
// planted red of the GOPRIVATE row is GONOPROXY=none left out, with which go asks
// https://example.invalid/x?go-get=1.
//
// What it costs when this fails: a test's go reaches the network, on a host with none it hangs or
// fails on a lookup, and a module the cache holds is downloaded again or not found.
func TestAGoUnderGoOfflineFetchesNothingAndBuildsFromTheCache(t *testing.T) {
	// serial: run clears the process's environment, and the module cache is the test's.
	cache := t.TempDir()
	plantCachedModule(t, cache)
	start := append(slices.DeleteFunc(offlineRun(), func(kv string) bool {
		k, _, _ := strings.Cut(kv, "=")
		return strings.EqualFold(k, "GOMODCACHE") || strings.EqualFold(k, "GOPRIVATE") || strings.EqualFold(k, "GOFLAGS")
	}), "GOMODCACHE="+cache, "GOPRIVATE=example.invalid")
	missing := "module example.invalid/app\n\ngo 1.27\n\nrequire example.invalid/x v1.0.0\n"
	ran := 0
	for row, tc := range map[string]struct {
		opts   []Option
		dir    string
		args   []string
		ok     bool
		want   string
		denied []string
	}{
		"a requirement the cache lacks": {[]Option{GoOffline()}, plantModule(t, missing, "example.invalid/x"), []string{"build", "-mod=mod", "-modcacherw", "./..."}, false,
			"module lookup disabled by GOPROXY=off", []string{"go-get=1", "https://"}},
		"the same, GOPRIVATE kept": {[]Option{GoOffline(), Keep("GOPRIVATE")}, plantModule(t, missing, "example.invalid/x"), []string{"build", "-mod=mod", "-modcacherw", "./..."}, false,
			"module lookup disabled by GOPROXY=off", []string{"go-get=1", "https://"}},
		"a release the run lacks": {[]Option{GoOffline(), Keep("GOTOOLCHAIN")}, plantModule(t, "module example.invalid/app\n\ngo 1.99.0\n\ntoolchain go1.99.0\n", ""),
			[]string{"build", "./..."}, false, "GOTOOLCHAIN=local", []string{"downloading"}},
		"a requirement the run's cache holds": {[]Option{GoOffline()}, plantModule(t, "module example.invalid/app\n\ngo 1.27\n\nrequire example.invalid/dep v1.0.0\n", "example.invalid/dep"),
			[]string{"build", "-mod=mod", "-modcacherw", "./..."}, true, "", nil},
		"a proxy the run kept": {[]Option{GoOffline(), Keep("GOPROXY", "GOSUMDB")}, t.TempDir(), []string{"env", "GOPROXY", "GONOPROXY", "GOSUMDB", "GOTOOLCHAIN"}, true,
			"off\nnone\noff\nlocal\n", nil},
	} {
		ran++
		var out []byte
		var goErr error
		code, err := run(runner(func() int {
			cmd := exec.CommandContext(t.Context(), "go", tc.args...) //nolint:gosec // G204: the go on PATH, with each row's own fixed arguments
			cmd.Dir, cmd.Env = tc.dir, Clean(os.Environ())
			out, goErr = cmd.CombinedOutput()
			return 0
		}), start, tc.opts)
		if err != nil || code != 0 {
			t.Fatalf("%s: run: code %d, %v", row, code, err)
		}
		if (goErr == nil) != tc.ok || !strings.Contains(string(out), tc.want) {
			t.Errorf("%s: go %s: %v\n%s\nwant it to pass %v, with %q", row, strings.Join(tc.args, " "), goErr, out, tc.ok, tc.want)
		}
		for _, d := range tc.denied {
			if strings.Contains(string(out), d) {
				t.Errorf("%s: go %s printed %q, and GoOffline fetches nothing:\n%s", row, strings.Join(tc.args, " "), d, out)
			}
		}
	}
	if ran != 5 {
		t.Fatalf("ran %d rows, want 5", ran)
	}
}
