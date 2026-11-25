package testkit

import (
	"os"
	"path/filepath"
	"testing"
)

// NoLinks is the one message of every test skipped for a link: the system refuses this account a
// symbolic link, as Windows does with Developer Mode off, so the test is not measured there and says so.
const NoLinks = "not measured: this system refuses this account a symbolic link (on Windows, Developer Mode is off), and this test plants one"

// Links skips t with NoLinks on a system that refuses this account a symbolic link. A test whose every
// case plants a link calls it before its subtests, since a parent whose subtests all skip is reported
// as passed.
func Links(t testing.TB) {
	t.Helper()
	skipRefused(t, probeLinks(t.TempDir()))
}

// Link plants a symbolic link at link whose text is target, as os.Symlink does, for t. On a system that
// refuses this account a link it skips t with NoLinks; a link the system makes for the probe but not
// for t fails t. It is the one place a test makes a link.
func Link(t testing.TB, target, link string) {
	t.Helper()
	plant(t, probeLinks(t.TempDir()), target, link)
}

// probeLinks makes one link in dir, a temp folder of the test's own, and is the system's refusal of it.
func probeLinks(dir string) error {
	return os.Symlink("target", filepath.Join(dir, "link"))
}

// skipRefused skips t with NoLinks when the probe was refused.
func skipRefused(t testing.TB, refused error) {
	t.Helper()
	if refused != nil {
		t.Skip(NoLinks)
	}
}

// plant is Link with the probe's answer given.
func plant(t testing.TB, refused error, target, link string) {
	t.Helper()
	skipRefused(t, refused)
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("the link %s could not be planted: %v", link, err)
	}
}
