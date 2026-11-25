package testkit

import (
	"crypto/rand"
	"errors"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

// TestShortDirHoldsASocketOfThePathMax: a socket listens at a path of SocketPathMax bytes in
// ShortDir's folder, and a path one byte longer is refused, so the constant is this system's.
//
// What it costs when this fails: a product's test sizes a socket's path by a number that is not
// the system's, and its listen is refused on one system alone, or it keeps a margin it need not.
func TestShortDirHoldsASocketOfThePathMax(t *testing.T) {
	t.Parallel()
	t.Logf("ShortDir root branch: %s", rootBranch())
	if deepHostRefuses(t) {
		return
	}
	dir := ShortDir(t)
	room := SocketPathMax - len(dir) - 1
	if room < shortDirRoom {
		t.Fatalf("ShortDir %s leaves %d bytes under it, want at least %d", dir, room, shortDirRoom)
	}
	var lc net.ListenConfig
	fits := filepath.Join(dir, strings.Repeat("s", room))
	l, err := lc.Listen(t.Context(), "unix", fits)
	if err != nil {
		t.Fatalf("a socket at a path of %d bytes, SocketPathMax: %v", len(fits), err)
	}
	_ = l.Close()
	over := filepath.Join(dir, strings.Repeat("o", room+1))
	if l, err := lc.Listen(t.Context(), "unix", over); err == nil {
		_ = l.Close()
		t.Fatalf("a socket at a path of %d bytes, one past SocketPathMax, listened", len(over))
	}
}

// TestShortDirIsRemovedWithTheTest: the folder, and what the test put in it, are gone once the
// test's cleanups run.
//
// What it costs when this fails: every run leaves a folder in /tmp or the account's temporary
// folder, and a socket in it, until somebody clears them.
func TestShortDirIsRemovedWithTheTest(t *testing.T) {
	t.Parallel()
	t.Logf("ShortDir root branch: %s", rootBranch())
	if deepHostRefuses(t) {
		return
	}
	ct := &cleanupTB{TB: t}
	dir := ShortDir(ct)
	if err := os.WriteFile(filepath.Join(dir, "f"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, f := range slices.Backward(ct.cleanups) {
		f()
	}
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		t.Fatalf("ShortDir %s after its test's end: %v, want it gone", dir, err)
	}
}

// TestShortDirGivesANewFolderOfOneLength: two calls give two folders, each absolute, in the
// system's root, and of one length, so the room a product counts on is the same every run.
//
// What it costs when this fails: two tests share a folder and each other's sockets, or a run's
// path length moves with a name, and a socket fits on one run and not the next.
func TestShortDirGivesANewFolderOfOneLength(t *testing.T) {
	t.Parallel()
	t.Logf("ShortDir root branch: %s", rootBranch())
	if deepHostRefuses(t) {
		return
	}
	a, b := ShortDir(t), ShortDir(t)
	if a == b {
		t.Fatalf("two calls of ShortDir both gave %s", a)
	}
	root, _ := shortRoot()
	root = filepath.Clean(root)
	for _, d := range []string{a, b} {
		if !filepath.IsAbs(d) || filepath.Dir(d) != root || len(d) != len(root)+1+shortDirName {
			t.Errorf("ShortDir gave %s, want an absolute folder of %d bytes in %s", d, len(root)+1+shortDirName, root)
		}
	}
}

// TestShortDirRefusesARootItCannotUse: a root that is not absolute, one that leaves fewer than
// shortDirRoom bytes, and one that is not there are refused, and the long root is refused before
// anything is made in it.
//
// What it costs when this fails: a test is handed a folder its socket does not fit in, and fails
// at its listen on one system, naming the socket and not the root; or a relative root puts the
// folder wherever the test runs.
func TestShortDirRefusesARootItCannotUse(t *testing.T) {
	t.Parallel()
	long := filepath.Join(t.TempDir(), strings.Repeat("r", SocketPathMax))
	if err := os.Mkdir(long, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{".", "tmp", long} {
		if dir, err := shortDir(root, nil); err == nil {
			t.Errorf("shortDir(%q) gave %s, want it refused", root, dir)
		}
	}
	// A root that is not there is refused as not there where it fits, or over the budget where the
	// host's own root leaves it too few bytes; the test takes whichever applies to this host's root.
	sr, _ := shortRoot()
	absent := filepath.Join(sr, "n"+rand.Text()[:4])
	if dir, err := shortDir(absent, nil); err == nil || (!errors.Is(err, fs.ErrNotExist) && !strings.Contains(err.Error(), "a root may have here")) {
		t.Errorf("shortDir(%q) gave %q, %v, want it refused as not there or over the budget", absent, dir, err)
	}
	if names, err := os.ReadDir(long); err != nil || len(names) != 0 {
		t.Errorf("the refused root %s holds %v (%v), want nothing", long, names, err)
	}
}

// TestShortDirRefusesARootOverItsBudget: shortDir takes a root of exactly maxRootLen bytes and refuses
// one byte more, and a deep root through ShortDir's fake TB fails naming the budget and the remedy,
// with nothing made. The boundary is read from the constants, so it moves with them.
//
// What it costs when this fails: ShortDir hands back a folder a socket's path overruns on one system,
// or it refuses a root that fits and sends a host chasing a limit that is not the real one.
func TestShortDirRefusesARootOverItsBudget(t *testing.T) {
	t.Parallel()
	base, _ := shortRoot()
	base = filepath.Clean(base)
	// A root over the budget is refused, naming the budget and the remedy, before anything is made;
	// the root need not be there, as the length is read before the folder is tried.
	over := filepath.Join(base, strings.Repeat("o", maxRootLen))
	if _, err := shortDir(over, nil); err == nil || !strings.Contains(err.Error(), "a root may have here") || !strings.Contains(err.Error(), shortDirRemedy(nil)) {
		t.Errorf("shortDir of a root over the budget: %v, want it refused naming the budget and the remedy %q", err, shortDirRemedy(nil))
	}
	// A root of exactly the budget is taken, and one byte more refused. Build the exact-fit root by
	// padding a base short enough to leave room; where the host's own root is already exactly the
	// budget use it, and where it is over, the host is too deep to hold such a root, so this row says
	// so and the over-budget row above stands for it.
	var fit string
	switch {
	case len(base) < maxRootLen:
		// A new random name of the length that pads the root to maxRootLen, so parallel runs and a
		// run after one that left a folder behind never collide on it.
		pad := maxRootLen - len(base) - 1
		name := ""
		for len(name) < pad {
			name += rand.Text()
		}
		fit = filepath.Join(base, name[:pad])
		if err := os.Mkdir(fit, 0o700); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(fit) })
	case len(base) == maxRootLen:
		fit = base
	default:
		t.Logf("the host's root is %d bytes, over the %d budget, so the exact-fit row is not built here", len(base), maxRootLen)
		return
	}
	if len(fit) != maxRootLen {
		t.Fatalf("the exact-fit root is %d bytes, want maxRootLen %d", len(fit), maxRootLen)
	}
	if dir, err := shortDir(fit, nil); err != nil {
		t.Errorf("shortDir of a root of maxRootLen %d bytes: %v, want it taken", maxRootLen, err)
	} else if err := os.RemoveAll(dir); err != nil {
		t.Error(err)
	}
	if dir, err := shortDir(fit+"o", nil); err == nil {
		t.Errorf("shortDir of a root one over maxRootLen: gave %s, want it refused", dir)
	}
}

// deepHostRefuses, on a host whose temporary root is over the budget even in its short form, asserts
// ShortDir refuses through the fake TB, naming the budget and the remedy, and returns true, so a deep
// host runs every ShortDir test and skips none. Where the root fits it returns false and the test
// goes on to its socket rows.
func deepHostRefuses(t *testing.T) bool {
	t.Helper()
	if rootBranch() != "deep" {
		return false
	}
	ft := &fatalTB{TB: t}
	var wg sync.WaitGroup
	wg.Go(func() { ShortDir(ft) })
	wg.Wait()
	if !strings.Contains(ft.msg, "a root may have here") || !strings.Contains(ft.msg, shortDirRemedy(nil)) {
		t.Errorf("ShortDir on this deep host failed with %q, want the budget and the remedy", ft.msg)
	}
	return true
}
