package homevars

//testkit:unisolated the root package imports homevars, so its tests cannot import testkit without a cycle; they read lists of names and start nothing

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Each system's list holds the names it reads, each once: HOME, the XDG_ folders and the temp names
// on every system, and on windows alone the profile, roaming and local folders and the halves of
// the profile. The list is read for any system on any host, as goos is an argument.
//
// What it costs when this fails: a test under Isolate reads a folder of
// the person's by a name the list leaves out, on a system no host of the tier runs.
func TestEachSystemListsTheNamesItReads(t *testing.T) {
	t.Parallel()
	every := []string{"HOME", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_RUNTIME_DIR", "TMPDIR", "TMP", "TEMP"}
	windows := []string{"USERPROFILE", "APPDATA", "LOCALAPPDATA"}
	for _, goos := range []string{"linux", "darwin", "freebsd", "windows"} {
		got := Folders(goos)
		want := every
		if goos == "windows" {
			want = slices.Concat(every, windows)
		}
		if !slices.Equal(got, want) {
			t.Errorf("Folders(%s) is %v, want %v", goos, got, want)
		}
		if halves := Halves(goos); goos == "windows" != slices.Equal(halves, []string{"HOMEDRIVE", "HOMEPATH"}) || goos != "windows" && halves != nil {
			t.Errorf("Halves(%s) is %v", goos, halves)
		}
	}
}

// Under gives each of this system's folders a folder of its own beneath root, named for it in lower
// case, then the halves, which on Windows name USERPROFILE's folder joined.
//
// What it costs when this fails: two names share one folder, a name points outside root, or a
// Windows program that reads HOMEDRIVE and HOMEPATH finds the person's home.
func TestUnderPointsEachNameBeneathRoot(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	got := map[string]string{}
	for _, kv := range Under(root) {
		k, v, _ := strings.Cut(kv, "=")
		got[k] = v
	}
	for _, k := range Folders(Here) {
		if want := filepath.Join(root, strings.ToLower(k)); got[k] != want {
			t.Errorf("%s is %q, want %q", k, got[k], want)
		}
	}
	if halves := Halves(Here); halves != nil && got[halves[0]]+got[halves[1]] != got["USERPROFILE"] {
		t.Errorf("%s %q and %s %q do not name USERPROFILE %q", halves[0], got[halves[0]], halves[1], got[halves[1]], got["USERPROFILE"])
	}
	if len(got) != len(Folders(Here))+len(Halves(Here)) {
		t.Errorf("Under gives %v, a name twice or one more", got)
	}
}
