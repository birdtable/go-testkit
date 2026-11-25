package testkit

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// sourceEnv is what the run's go env file holds in these tests.
const sourceEnv = "GOPRIVATE=example.com/*\n"

// goSees is what a go started from Clean(os.Environ()) prints for GOENV, GOPRIVATE and GOTELEMETRY, as
// `go env` prints them, one to a line. It fails by its error, never the test, since it runs inside
// run, whose restore a test's Fatal would skip.
func goSees(ctx context.Context) (goenv, private, telemetry string, err error) {
	cmd := exec.CommandContext(ctx, "go", "env", goEnvName, "GOPRIVATE", "GOTELEMETRY")
	cmd.Env = Clean(os.Environ())
	b, err := cmd.Output()
	if err != nil {
		return "", "", "", fmt.Errorf("go env %s GOPRIVATE GOTELEMETRY: %w", goEnvName, err)
	}
	lines := strings.Split(strings.TrimSuffix(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n"), "\n")
	if len(lines) != 3 {
		return "", "", "", fmt.Errorf("go env %s GOPRIVATE GOTELEMETRY printed %q, want three lines", goEnvName, b)
	}
	return lines[0], lines[1], lines[2], nil
}

// counterFolder is the folder where go's telemetry keeps its counter files, in the configuration
// folder the process has, when it is there or cannot be told absent, and "" when it is not there.
func counterFolder() string {
	config, err := os.UserConfigDir()
	if err != nil {
		return "the configuration folder: " + err.Error()
	}
	local := filepath.Join(config, "go", "telemetry", "local")
	if _, err := os.Stat(local); errors.Is(err, os.ErrNotExist) {
		return ""
	}
	return local
}

// configHere points the person's configuration folder, as os.UserConfigDir reads it on each system
// (APPDATA on Windows, HOME's Library/Application Support on macOS, XDG_CONFIG_HOME elsewhere), at
// a new folder of the test's, and is go's env file there, which holds sourceEnv when withFile is set.
func configHere(t *testing.T, withFile bool) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("APPDATA", filepath.Join(root, "appdata"))
	dir, err := os.UserConfigDir()
	if err != nil || !beneath(dir, root) {
		t.Fatalf("the configuration folder is %q, %v, want one beneath %s", dir, err, root)
	}
	file := filepath.Join(dir, "go", "env")
	if withFile {
		if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(sourceEnv), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return file
}

// startWithout is the process's environment with no GOENV, as a run that names no env file starts.
func startWithout() []string {
	var out []string
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); !strings.EqualFold(k, goEnvName) {
			out = append(out, kv)
		}
	}
	return out
}

// TestCleanPassesGOENVOnlyAsTheOffIsolateGave: under an Isolate, Clean passes GOENV, in any case,
// only as off, the value Isolate pins, with or without a Keep of GOENV. It drops every other GOENV:
// the person's file, a path beneath Isolate's folder that a test wrote, and one that climbs out.
// Outside an Isolate it drops every one.
//
// What it costs when this fails: a child of a test reads an env file, the person's among them, and
// with it a GOFLAGS, a GOPROXY with a password or a GOAUTH the app never kept.
func TestCleanPassesGOENVOnlyAsTheOffIsolateGave(t *testing.T) {
	t.Parallel()
	dir := abs("tmp/testkit-1")
	in := isolated{set: ownSet(dir), own: dir}
	kept := isolated{set: ownSet(dir), own: dir, conf: confOf([]Option{Keep(goEnvName)})}
	person := abs("person/.config/go/env")
	ran := 0
	for _, tc := range []struct {
		in     isolated
		k, v   string
		passes bool
	}{
		{in, goEnvName, "off", true},
		{in, "goenv", "off", true},
		{in, goEnvName, person, false},
		{in, goEnvName, filepath.Join(dir, "goenv"), false},
		{in, "goenv", filepath.Join(dir, "go", "env"), false},
		{in, goEnvName, filepath.Join(dir, "..", "go", "env"), false},
		{in, goEnvName, "", false},
		{kept, goEnvName, "off", true},
		{kept, goEnvName, person, false},
		{kept, "goenv", person, false},
		{kept, goEnvName, filepath.Join(dir, "goenv"), false},
		{isolated{}, goEnvName, "off", false},
		{isolated{conf: kept.conf}, goEnvName, person, false},
	} {
		ran++
		if got := tc.in.passes(tc.k, tc.v); got != tc.passes {
			t.Errorf("under %v with Keep %q, %s=%s passes: %v, want %v", tc.in.set[goEnvName], tc.in.conf.keep, tc.k, tc.v, got, tc.passes)
		}
	}
	if ran != 13 {
		t.Fatalf("ran %d cases, want 13", ran)
	}
}

// TestTelemetryIsTurnedOffOnlyBeneathIsolatesFolder: telemetryOff writes go's telemetry mode file,
// off, in a configuration folder beneath the tests' folder, and refuses one outside it, a relative
// one and one that climbs out, writing nothing there.
//
// What it costs when this fails: Isolate turns off the person's own go telemetry, or a go a test
// starts on Windows holds a file open in the tests' folder and restore cannot remove it.
func TestTelemetryIsTurnedOffOnlyBeneathIsolatesFolder(t *testing.T) {
	t.Parallel()
	own, person := t.TempDir(), t.TempDir()
	ran := 0
	for _, tc := range []struct {
		config string
		ok     bool
	}{
		{filepath.Join(own, "appdata"), true},
		{filepath.Join(own, "home", "Library", "Application Support"), true},
		{person, false},
		{own + string(filepath.Separator) + ".." + string(filepath.Separator) + filepath.Base(person), false},
		{filepath.Join("relative", "config"), false},
	} {
		ran++
		err := telemetryOff(tc.config, own)
		b, read := os.ReadFile(filepath.Join(tc.config, "go", "telemetry", "mode"))
		switch {
		case tc.ok && (err != nil || read != nil || string(b) != "off"):
			t.Errorf("telemetryOff(%s): %v, and the mode file holds %q, %v; want off", tc.config, err, b, read)
		case !tc.ok && (err == nil || read == nil):
			t.Errorf("telemetryOff(%s): %v, and the mode file holds %q, %v; want it refused and nothing written", tc.config, err, b, read)
		}
	}
	if ran != 5 {
		t.Fatalf("ran %d cases, want 5", ran)
	}
}

// userinfo is a URL's userinfo and its @, a user and a password. The tests build every URL with
// userinfo from it at run time, so none is written out in this package, and a failure names such a
// URL in words, never in bytes, so a failure prints no credential.
func userinfo(user, password string) string {
	return user + ":" + password + "@"
}

// TestGOENVIsFoundInAnyCaseOnlyWhereNamesFold: lookupIn finds goenv=off for GOENV when names fold,
// as Windows reads them, and only GOENV itself when they do not; the last match wins either way. And
// foldNames, which goSettings reads the run's GOENV by, is the namesFoldHere the tests hold apart.
//
// What it costs when this fails: on Windows a run that turned go's env file off has the go tool's
// folders read from the file anyway, or off Windows a variable go does not read decides which file.
func TestGOENVIsFoundInAnyCaseOnlyWhereNamesFold(t *testing.T) {
	t.Parallel()
	environ := []string{"GOENV=/a/env", "goenv=off", "Other=x"}
	if got := lookupIn(environ, goEnvName, true); got != "off" {
		t.Errorf("lookupIn(%q, GOENV, fold) is %q, want off", environ, got)
	}
	if got := lookupIn(environ, goEnvName, false); got != "/a/env" {
		t.Errorf("lookupIn(%q, GOENV, exact) is %q, want /a/env", environ, got)
	}
	if foldNames != namesFoldHere {
		t.Errorf("foldNames is %v, and this system reads names folded: %v", foldNames, namesFoldHere)
	}
}
