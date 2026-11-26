package canary

import (
	"os"
	"os/exec" //nolint:depguard // making a junction in a test, which the standard library cannot do, as the root's mode_windows_test.go does
	"path/filepath"
	"strings"
	"testing"
)

// TestFilesReadsAJunctionsTargetAndFollowsItNot: a junction under the folder, made by cmd's mklink /J,
// to a folder outside named with a planted value that holds a file holding it, is one hit in the
// place "current (target)" at the value's offset in the target's text, and Files reads nothing at the
// target.
//
// What it costs when this fails: a program on Windows that points a junction at a token, as a release
// or a session is often named, passes its test; or Files follows a junction out of the folder.
func TestFilesReadsAJunctionsTargetAndFollowsItNot(t *testing.T) {
	t.Parallel()
	rec := newRecorder(t)
	var s *Set
	if rec.do(func() { s = New(rec, "token") }) {
		t.Fatalf("planting: %s", rec.said())
	}
	v := s.Get("token")
	dir, target := t.TempDir(), filepath.Join(t.TempDir(), v)
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(s.redact(err.Error()))
	}
	if err := os.WriteFile(filepath.Join(target, "state.json"), []byte(v), 0o600); err != nil {
		t.Fatal(s.redact(err.Error()))
	}
	link := filepath.Join(dir, "current")
	if out, err := exec.CommandContext(t.Context(), "cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil { //nolint:gosec // making a junction in a test, which the standard library cannot do, as the root's mode_windows_test.go does
		t.Fatalf("mklink /J: %v: %s", err, s.redact(string(out)))
	}
	to, err := os.Readlink(link)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(to, v) {
		t.Fatalf("the junction's target, %q, does not end in the token", s.redact(to))
	}
	sameHits(t, "a folder with a junction", s.Files(dir), []Hit{{Name: "token", Check: CheckFiles, Place: "current (target)", Offset: int64(len(to) - len(v)), Encoding: Raw, Count: 1}})
}
