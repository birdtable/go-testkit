package testkit

import (
	"crypto/rand"
	"fmt"
	"syscall"
	"unsafe"
)

// The named pipe's flags, which syscall does not name. Its mode is PIPE_TYPE_BYTE and PIPE_WAIT,
// both 0, with PIPE_REJECT_REMOTE_CLIENTS, and it has one instance.
const (
	pipeAccessOutbound        = 0x2
	fileFlagFirstPipeInstance = 0x80000
	pipeRejectRemoteClients   = 0x8
	pipeOneInstance           = 1
	sddlRevision1             = 1
)

// holdRead makes one instance of a pipe of a random name, for writing by this process and reading by
// a client. FILE_FLAG_FIRST_PIPE_INSTANCE fails the make if the name is anyone's already, so the hold
// never joins a pipe another process made. Its one instance takes one open, PIPE_REJECT_REMOTE_CLIENTS
// keeps that open on this machine, and its protected DACL (holdDACL) gives this account alone access.
// A client's open meets the listening instance and does not block; its read waits for bytes this
// process never writes. let closes the instance, which ends that read, as Go reads a broken pipe,
// with nothing read, and takes the name away. dir is not used: a pipe is in no folder.
// CreateNamedPipeW is kernel32's and ConvertStringSecurityDescriptorToSecurityDescriptorW advapi32's,
// which syscall does not name; both are KnownDLLs, which Windows loads from its own system folder.
func holdRead(string) (string, func() error, error) {
	path := `\\.\pipe\testkit-hold-` + rand.Text()
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return "", nil, err
	}
	dacl, err := holdDACL()
	if err != nil {
		return "", nil, err
	}
	sddl, err := syscall.UTF16PtrFromString(dacl)
	if err != nil {
		return "", nil, err
	}
	var sd uintptr
	conv := syscall.NewLazyDLL("advapi32.dll").NewProc("ConvertStringSecurityDescriptorToSecurityDescriptorW")
	if ok, _, err := conv.Call(uintptr(unsafe.Pointer(sddl)), sddlRevision1, uintptr(unsafe.Pointer(&sd)), 0); ok == 0 { //nolint:gosec // G103: the call reads the SDDL and writes the descriptor's address, each by its address
		return "", nil, fmt.Errorf("making the pipe's DACL: %w", err)
	}
	defer syscall.LocalFree(syscall.Handle(sd)) //nolint:errcheck // the descriptor is copied into the pipe at its make
	sa := syscall.SecurityAttributes{Length: uint32(unsafe.Sizeof(syscall.SecurityAttributes{})), SecurityDescriptor: sd}
	h, _, err := syscall.NewLazyDLL("kernel32.dll").NewProc("CreateNamedPipeW").Call(uintptr(unsafe.Pointer(name)), //nolint:gosec // G103: the call reads the pipe's name by its address
		pipeAccessOutbound|fileFlagFirstPipeInstance, pipeRejectRemoteClients, pipeOneInstance, 0, 0, 0,
		uintptr(unsafe.Pointer(&sa))) //nolint:gosec // G103: the call reads the struct Windows defines by its address
	if syscall.Handle(h) == syscall.InvalidHandle {
		return "", nil, fmt.Errorf("making the pipe %s: %w", path, err)
	}
	return path, func() error { return syscall.CloseHandle(syscall.Handle(h)) }, nil
}

// holdDACL is the pipe's DACL in SDDL: protected, so it inherits nothing, and one entry, which gives
// this process's account all of a file's access.
func holdDACL() (string, error) {
	tok, err := syscall.OpenCurrentProcessToken()
	if err != nil {
		return "", fmt.Errorf("reading this account: %w", err)
	}
	defer tok.Close() //nolint:errcheck // a token only read
	user, err := tok.GetTokenUser()
	if err != nil {
		return "", fmt.Errorf("reading this account: %w", err)
	}
	sid, err := user.User.Sid.String()
	if err != nil {
		return "", fmt.Errorf("reading this account: %w", err)
	}
	return "D:P(A;;FA;;;" + sid + ")", nil
}
