package testkit

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestShortDirOnADeepRootUsesTheSameFolderByItsShortName: where os.TempDir is too deep and the
// volume keeps 8.3 names, ShortDir's root is the short form of the same folder, so os.SameFile holds
// of it and os.TempDir, and a socket of SocketPathMax bytes listens in ShortDir's folder. Where the
// host takes the long-form or the refused branch, the test says which and runs no socket row, since
// no test can turn 8.3 names on.
//
// What it costs when this fails: ShortDir reaches for a second folder on a deep host, one with an
// access list of its own that testkit does not set, or hands back a path a socket overruns.
func TestShortDirOnADeepRootUsesTheSameFolderByItsShortName(t *testing.T) {
	t.Parallel()
	if b := rootBranch(); b != "short" {
		t.Logf("root branch: %s: the short-form row did not run on this host", b)
		return
	}
	t.Log("root branch: short (os.TempDir's 8.3 form)")
	root, err := shortRoot()
	if err != nil {
		t.Fatal(err)
	}
	rootInfo, err := os.Stat(filepath.Clean(root))
	if err != nil {
		t.Fatal(err)
	}
	tmpInfo, err := os.Stat(os.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(rootInfo, tmpInfo) {
		t.Errorf("ShortDir's short-form root %s and os.TempDir %s are not the same folder", root, os.TempDir())
	}
	dir := ShortDir(t)
	var lc net.ListenConfig
	at := filepath.Join(dir, strings.Repeat("s", SocketPathMax-len(dir)-1))
	l, err := lc.Listen(t.Context(), "unix", at)
	if err != nil {
		t.Fatalf("a socket at SocketPathMax (%d bytes) in the short-form folder: %v", len(at), err)
	}
	_ = l.Close()
}

// TestShortDirNamesTheShortCallsErrorOnADeepRoot: a root too deep and not there gives
// GetShortPathName its own read error, which pickRoot returns and shortDir's refusal names. This
// holds on every Windows host, with no 8.3 setting: GetShortPathName reads the disk, so a root that
// is not there always fails the read.
//
// What it costs when this fails: the refusal hides why the short form did not stand in for the deep
// root, so a host cannot tell a volume with no 8.3 name from a read that failed.
func TestShortDirNamesTheShortCallsErrorOnADeepRoot(t *testing.T) {
	t.Parallel()
	deep := filepath.Join(t.TempDir(), strings.Repeat("d", maxRootLen)) // over the budget, and not made
	root, pickErr := pickRoot(deep)
	if pickErr == nil {
		t.Fatalf("pickRoot(%q) returned no error, want GetShortPathName's own on a deep root that is not there", deep)
	}
	_, err := shortDir(root, pickErr)
	if err == nil || !strings.Contains(err.Error(), pickErr.Error()) {
		t.Errorf("shortDir's refusal %v does not name the short call's error %v", err, pickErr)
	}
}

// rootBranch names which root branch ShortDir takes on Windows, for the log each
// ShortDir test to carry: "deep" where os.TempDir is over the budget even in its 8.3 form, "long"
// where it fits as it is, and "short" where its 8.3 form stands in for it.
func rootBranch() string {
	picked, _ := shortRoot()
	root := filepath.Clean(picked)
	switch {
	case len(root) > maxRootLen:
		return "deep"
	case root == filepath.Clean(os.TempDir()):
		return "long"
	default:
		return "short"
	}
}
