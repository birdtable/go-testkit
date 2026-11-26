package sizecap

import (
	"os"
	"testing"

	"github.com/birdtable/go-testkit"
)

// TestMain runs the package's tests in an environment of their own, as testkit.RequireMain asks of
// every package of the module.
func TestMain(m *testing.M) {
	code, err := testkit.Main(m)
	if err != nil {
		panic(err)
	}
	os.Exit(code)
}
