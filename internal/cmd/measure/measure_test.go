package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The tests read trees they write in t.TempDir. The package has no TestMain of testkit's: it starts
// no process and reads no person's folder, as go-proc's measure has it.

// tree writes files, each a path from the top and its text, into a new folder and gives it.
func tree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, text := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// lines gives n lines of Go comment, each ended by a newline.
func lines(n int) string { return strings.Repeat("// a line\n", n) }

// What it costs when this fails: the size a tag is held to counts tests, test support or test data,
// gives one module's lines to another, or misses a system's file, and a tag passes or fails on lines
// no program links.
func TestSizeCountsEachModulesNonTestLinesAndRefusesAboveTheCeiling(t *testing.T) {
	t.Parallel()
	dir := tree(t, map[string]string{
		"go.mod":                  "module example.com/m\n",
		"m.go":                    lines(1000),
		"a/b_windows.go":          lines(7),
		"a/internal/i.go":         lines(3),
		"m_test.go":               lines(5000),
		"testdata/t.go":           lines(5000),
		"internal/x/x.go":         lines(5000),
		".hidden/h.go":            lines(5000),
		"_old/o.go":               lines(5000),
		"notes.txt":               lines(5000),
		"sub/go.mod":              "module example.com/m/sub\n",
		"sub/s_linux.go":          lines(Ceiling - 1010 - 20),
		"sub/internal/x/x.go":     lines(5000),
		"sub/s_test.go":           lines(5000),
		"sub/deep/go.mod":         "module example.com/m/sub/deep\n",
		"sub/deep/d.go":           lines(20),
		"sub/deep/internal/i.go":  lines(5000),
		"sub/deep/testdata/td.go": lines(5000),
	})
	var out bytes.Buffer
	if code := run([]string{"size"}, dir, &out); code != 0 {
		t.Errorf("size exited %d at the ceiling, and want 0: %s", code, &out)
	}
	want := fmt.Sprintf("module .: 1010\nmodule sub: %d\nmodule sub/deep: 20\n", Ceiling-1030) +
		fmt.Sprintf("non-test Go lines outside internal/: %d (the ceiling is %d)\n", Ceiling, Ceiling)
	if out.String() != want {
		t.Errorf("size said %q, and want %q", &out, want)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "deep", "d.go"), []byte(lines(21)), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if code := run([]string{"size"}, dir, &out); code != 1 {
		t.Errorf("size exited %d one line above the ceiling, and want 1: %s", code, &out)
	}
	if want := fmt.Sprintf("%d lines is 1 above the ceiling\n", Ceiling+1); !strings.HasSuffix(out.String(), want) {
		t.Errorf("size did not end with %q:\n%s", want, &out)
	}
}

// What it costs when this fails: a run from the wrong folder counts some tree that is not a module,
// passes, and holds nothing.
func TestSizeRefusesATopWithNoGoMod(t *testing.T) {
	t.Parallel()
	dir := tree(t, map[string]string{"sub/go.mod": "module example.com/sub\n", "sub/s.go": lines(1)})
	var out bytes.Buffer
	if code := run([]string{"size"}, dir, &out); code != 2 {
		t.Errorf("size exited %d with no go.mod at the top, and want 2: %s", code, &out)
	}
	if want := "measure: the repository's top holds no go.mod, so it is not a module\n"; out.String() != want {
		t.Errorf("size said %q, and want %q", &out, want)
	}
}

// What it costs when this fails: a task run with no word or a misspelt one passes and checks
// nothing.
func TestRunTakesTheOneWordSize(t *testing.T) {
	t.Parallel()
	dir := tree(t, map[string]string{"go.mod": "module example.com/m\n", "m.go": "package m\n"})
	for _, args := range [][]string{nil, {"size", "size"}, {"notice-check"}, {"Size"}} {
		var out bytes.Buffer
		if code := run(args, dir, &out); code != 2 {
			t.Errorf("run(%q) exited %d, and want 2: %s", args, code, &out)
		}
	}
}
