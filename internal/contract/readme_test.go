package contract_test

import (
	"path/filepath"
	"strings"
	"testing"
)

// The README's quick start is internal/example/quickstart's test file, byte for byte, as a go block:
// the tiers compile and run that file, so the code a reader copies is code that builds and passes.
//
// What it costs when this fails: a reader copies a quick start that no longer compiles, or that
// shows a call testkit no longer has.
func TestTheReadmesQuickStartIsTheQuickstartPackagesTest(t *testing.T) {
	t.Parallel()
	readme := string(read(t, filepath.Join(root, "README.md")))
	file := filepath.Join(root, "internal", "example", "quickstart", "quickstart_test.go")
	if block := "```go\n" + string(read(t, file)) + "```\n"; !strings.Contains(readme, block) {
		t.Errorf("README.md holds no go block equal to %s; the quick start is that file, byte for byte", file)
	}
}
