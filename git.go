package testkit

import (
	"os/exec" //nolint:depguard // GitPath looks git up on PATH as the host does, and starts nothing
	"path/filepath"
	"testing"
)

// GitPath is the absolute path of the git this host puts on its PATH, found as the host decides, with no
// list of folders a host might keep it in. A test hands it to the code under test as its setting, as a
// person does. A host with no git fails t. It moved from go-proc's testenv at v0.11.0 unchanged; Git and Repo stay in go-proc beside gitenv.
func GitPath(t testing.TB) string {
	t.Helper()
	return gitPath(t, exec.LookPath)
}

// gitPath is GitPath with the lookup given, so a test can give one that finds no git.
func gitPath(t testing.TB, look func(string) (string, error)) string {
	t.Helper()
	p, err := look("git")
	if err != nil {
		t.Fatalf("this test runs the real git, and the host's PATH holds none: %v", err)
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}
