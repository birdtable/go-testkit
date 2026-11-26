package link

import (
	"os"
	"testing"

	"github.com/birdtable/go-testkit"
)

// TestMain runs the package's tests in an environment of their own, with no network for a go they
// start, as testkit.RequireMain asks of every package of the module.
func TestMain(m *testing.M) {
	code, err := testkit.Main(m, testkit.GoOffline())
	if err != nil {
		panic(err)
	}
	os.Exit(code)
}
