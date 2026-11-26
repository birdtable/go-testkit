//go:build !windows

package canary

import (
	"os"
	"testing"
)

// unreadable makes the file at path one this account cannot open, by its mode, until t ends. It fails
// t when the file can still be opened, as it can for root, so the row never passes on a file it read.
func unreadable(t *testing.T, path string) {
	t.Helper()
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(path, 0o600); err != nil {
			t.Error(err)
		}
	})
	if f, err := os.Open(path); err == nil {
		_ = f.Close()
		t.Fatalf("%s opens with mode 000, as for root: this row needs an account the mode holds", path)
	}
}
