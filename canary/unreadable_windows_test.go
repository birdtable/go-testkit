package canary

import (
	"os"
	"syscall"
	"testing"
	"unsafe"
)

// The LockFileEx flags: an exclusive lock, and an error at once in place of a wait.
const (
	lockExclusive = 0x2
	lockNoWait    = 0x1
)

// unreadable makes the file at path one no other handle can read until t ends, by an exclusive lock
// over all of it on a handle the test holds, so a read through any other handle, this process's
// included, fails as a lock violation. A handle held with no sharing does not do it: on the hosted
// Windows runner an open beside such a handle succeeded.
func unreadable(t *testing.T, path string) {
	t.Helper()
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	h, err := syscall.CreateFile(name, syscall.GENERIC_READ, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := syscall.CloseHandle(h); err != nil {
			t.Error(err)
		}
	})
	// kernel32's LockFileEx, which the syscall package does not export, over every byte.
	lockFileEx := syscall.NewLazyDLL("kernel32.dll").NewProc("LockFileEx")
	var o syscall.Overlapped
	if ok, _, err := lockFileEx.Call(uintptr(h), lockExclusive|lockNoWait, 0, 0xFFFFFFFF, 0xFFFFFFFF, uintptr(unsafe.Pointer(&o))); ok == 0 { //nolint:gosec // G103: LockFileEx takes its OVERLAPPED by pointer, and o outlives the call
		t.Fatalf("locking %s: %v", path, err)
	}
	if _, err := os.ReadFile(path); err == nil {
		t.Fatalf("%s reads though it is locked", path)
	}
}
