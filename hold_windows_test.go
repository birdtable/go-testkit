package testkit

import (
	"os"
	"syscall"
	"testing"
	"unsafe"
)

// TestHoldReadRefusesASecondReader: on Windows the pipe takes one open, and a second is refused, so no
// other process of this account takes the hold's place.
//
// What it costs when this fails: another process of the account opens the pipe before the test's
// child, and the child finds it taken, or it holds it in the child's place.
func TestHoldReadRefusesASecondReader(t *testing.T) {
	t.Parallel()
	path, release := HoldRead(t)
	first := readAll(t, path)
	f, err := os.Open(path)
	if err == nil {
		_ = f.Close()
		t.Fatalf("a second open of the pipe %s was let in", path)
	}
	release()
	ended(t, first)
}

// TestHoldReadIsThisAccountsAlone: on Windows the pipe's DACL, read back from an open of it, is
// protected, inheriting nothing, and holds one entry, which lets in this account alone. Both sides
// are SDDL written by Windows: the want is holdDACL read in and written out again, so an account
// Windows writes by an alias (LA for the machine's own Administrator, as on a hosted runner) is
// compared as Windows writes it, and not as its SID (go-proc f557278).
//
// What it costs when this fails: another account on the machine opens the pipe before the test's
// child, and holds or ends the child's wait.
func TestHoldReadIsThisAccountsAlone(t *testing.T) {
	t.Parallel()
	path, _ := HoldRead(t)
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	const seFileObject, daclSecurityInformation = 1, 0x4
	advapi := syscall.NewLazyDLL("advapi32.dll")
	var sd uintptr
	get := advapi.NewProc("GetSecurityInfo")
	if rc, _, _ := get.Call(f.Fd(), seFileObject, daclSecurityInformation, 0, 0, 0, 0, uintptr(unsafe.Pointer(&sd))); rc != 0 { //nolint:gosec // G103: the call writes the descriptor's address by its address
		t.Fatalf("reading the pipe's DACL: %v", syscall.Errno(rc))
	}
	defer syscall.LocalFree(syscall.Handle(sd))
	ours, err := holdDACL()
	if err != nil {
		t.Fatal(err)
	}
	p, err := syscall.UTF16PtrFromString(ours)
	if err != nil {
		t.Fatal(err)
	}
	var wantSD uintptr
	read := advapi.NewProc("ConvertStringSecurityDescriptorToSecurityDescriptorW")
	if ok, _, err := read.Call(uintptr(unsafe.Pointer(p)), sddlRevision1, uintptr(unsafe.Pointer(&wantSD)), 0); ok == 0 { //nolint:gosec // G103: the call reads the string and writes the descriptor's address by their addresses
		t.Fatalf("reading holdDACL's %s: %v", ours, err)
	}
	defer syscall.LocalFree(syscall.Handle(wantSD))
	if got, want := sddlOf(t, advapi, sd), sddlOf(t, advapi, wantSD); got != want {
		t.Fatalf("the pipe's DACL is %s, want %s (holdDACL's %s, as Windows writes it)", got, want, ours)
	}
}

// sddlOf is the DACL of the security descriptor at sd, as SDDL written by Windows.
func sddlOf(t *testing.T, advapi *syscall.LazyDLL, sd uintptr) string {
	t.Helper()
	const daclSecurityInformation = 0x4
	var sddl *uint16
	conv := advapi.NewProc("ConvertSecurityDescriptorToStringSecurityDescriptorW")
	if ok, _, err := conv.Call(sd, sddlRevision1, daclSecurityInformation, uintptr(unsafe.Pointer(&sddl)), 0); ok == 0 { //nolint:gosec // G103: the call writes the string's address by its address
		t.Fatalf("writing a DACL as SDDL: %v", err)
	}
	defer syscall.LocalFree(syscall.Handle(unsafe.Pointer(sddl))) //nolint:gosec // G103: LocalFree takes the string by its address
	n := 0
	for *(*uint16)(unsafe.Add(unsafe.Pointer(sddl), 2*n)) != 0 { //nolint:gosec // G103: the string ends at its first NUL, which Windows wrote
		n++
	}
	return syscall.UTF16ToString(unsafe.Slice(sddl, n)) //nolint:gosec // G103: the n code units before the NUL
}
