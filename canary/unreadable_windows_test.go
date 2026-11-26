package canary

import (
	"os"
	"syscall"
	"testing"
)

// unreadable makes the file at path one no other open can share until t ends, by holding it open
// with no sharing, so a read of it fails as a sharing violation.
func unreadable(t *testing.T, path string) {
	t.Helper()
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	h, err := syscall.CreateFile(name, syscall.GENERIC_READ, 0, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := syscall.CloseHandle(h); err != nil {
			t.Error(err)
		}
	})
	if f, err := os.Open(path); err == nil {
		_ = f.Close()
		t.Fatalf("%s opens though it is held open with no sharing", path)
	}
}
