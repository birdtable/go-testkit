// Package homevars is testkit's list of the variables each system reads for the person's home,
// configuration, cache, state and temp folders: Isolate points a test's at a folder of its own from
// this list alone. go-proc keeps the same list for proc.HomeEnv, and a go-proc test, which runs under
// testkit's isolation, fails when the two differ. It imports the
// standard library only.
package homevars

import (
	"path/filepath"
	"strings"
)

// Folders is each variable a program on goos reads for the person's home, configuration, cache,
// state or temp folder, each naming one folder, in a fixed order. On every system: HOME, the five
// XDG_ folders, which git reads on every system, and TMPDIR, TMP and TEMP, which a portable runtime
// such as Python's reads on every system. On windows also USERPROFILE, APPDATA and LOCALAPPDATA,
// from which Windows and os read the home, the configuration and the cache.
func Folders(goos string) []string {
	names := []string{"HOME", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_RUNTIME_DIR", "TMPDIR", "TMP", "TEMP"}
	if goos == "windows" {
		names = append(names, "USERPROFILE", "APPDATA", "LOCALAPPDATA")
	}
	return names
}

// Halves is each variable a program on goos reads that names USERPROFILE's folder in two parts,
// none on any system but windows: HOMEDRIVE, its volume, and HOMEPATH, the rest.
func Halves(goos string) []string {
	if goos == "windows" {
		return []string{"HOMEDRIVE", "HOMEPATH"}
	}
	return nil
}

// Under is NAME=value for each of Folders on this system, each a folder of its own beneath root
// named for the variable in lower case, then each of Halves, naming USERPROFILE's. Root is taken as
// given, so a relative one gives relative values; it makes no folder.
func Under(root string) []string {
	var env []string
	profile := ""
	for _, k := range Folders(Here) {
		v := filepath.Join(root, strings.ToLower(k))
		if k == "USERPROFILE" {
			profile = v
		}
		env = append(env, k+"="+v)
	}
	if halves := Halves(Here); len(halves) == 2 {
		vol := filepath.VolumeName(profile)
		env = append(env, halves[0]+"="+vol, halves[1]+"="+profile[len(vol):])
	}
	return env
}
