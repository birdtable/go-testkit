package canary

import (
	"fmt"
	"strings"
	"testing"
)

// TestExpectRefusesAWindowsPathWithADriveOrAShare: Expect stops the test for a path with a drive,
// absolute or not, a path with a share, and a path rooted at the drive's top, naming the name and
// never a value. A path with a drive and no slash after it, which filepath.IsAbs calls relative, is
// refused with the rest.
//
// What it costs when this fails: a test on Windows expects a value in a file on another drive or
// share, or at the top of this one, and the check takes a path outside the folder.
func TestExpectRefusesAWindowsPathWithADriveOrAShare(t *testing.T) {
	t.Parallel()
	rec, s, _ := expectSet(t)
	for _, p := range []string{`C:\secrets.json`, `C:secrets.json`, `\\host\share\secrets.json`, `\secrets.json`} {
		if !rec.do(func() { s.Expect("token", p) }) {
			t.Errorf("Expect of %q did not stop the test", p)
		}
		want := fmt.Sprintf(`canary: Expect of "token" refuses the path %q: it names no file below the folder`, p)
		if said := rec.said(); !strings.Contains(said, want) {
			t.Errorf("Expect of %q said %q, want %q", p, said, want)
		}
	}
	holdsNoRun(t, s, rec.said())
}
