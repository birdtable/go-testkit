package testkit

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// GitPath gives the git on the host's PATH as an absolute path to a file, and a lookup that finds no git
// fails the test with the reason, before any path is given. A relative path the lookup gives is made
// absolute.
//
// What it costs when this fails: the code under test is handed a git it resolves against its own
// working folder, or no git at all, and a test that needs the real git runs on without it.
func TestGitPathIsTheHostsGitAsAnAbsolutePathAndFailsWhereThereIsNone(t *testing.T) {
	t.Parallel()
	p := GitPath(t)
	if !filepath.IsAbs(p) {
		t.Errorf("GitPath is %q, want an absolute path", p)
	}
	if fi, err := os.Stat(p); err != nil || !fi.Mode().IsRegular() {
		t.Errorf("GitPath is %q, which is no regular file: %v", p, err)
	}
	if base := strings.TrimSuffix(strings.ToLower(filepath.Base(p)), ".exe"); base != "git" {
		t.Errorf("GitPath is %q, whose name is not git", p)
	}

	var got string
	r := record(t, func(tb testing.TB) {
		tb.Helper()
		got = gitPath(tb, func(string) (string, error) { return "", errors.New("not found on PATH") })
	})
	if !strings.Contains(r.failed, "the host's PATH holds none: not found on PATH") || got != "" {
		t.Errorf("gitPath with no git failed with %q and gave %q, want the failure and no path", r.failed, got)
	}

	rel := record(t, func(tb testing.TB) {
		tb.Helper()
		got = gitPath(tb, func(string) (string, error) { return filepath.Join("bin", "git"), nil })
	})
	if rel.failed != "" || !filepath.IsAbs(got) || filepath.Base(got) != "git" {
		t.Errorf("gitPath of the relative bin/git failed with %q and gave %q, want an absolute path", rel.failed, got)
	}
}
