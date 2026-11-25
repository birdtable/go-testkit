//go:build darwin

package testkit

import (
	"os"
	"syscall"
	"unsafe"
)

// guardProfile is the system sandbox's profile of NoNetwork: every outbound connection is denied but
// to localhost, by address, and to a Unix socket, and the resolver's socket and service are denied, so
// a lookup does not carry the name out through mDNSResponder; /etc/hosts still answers localhost.
const guardProfile = `(version 1)
(allow default)
(deny network-outbound)
(allow network-outbound (remote ip "localhost:*"))
(allow network-outbound (remote unix-socket))
(deny network-outbound (remote unix-socket (path-literal "/private/var/run/mDNSResponder")))
(deny mach-lookup (global-name "com.apple.dnssd.service"))
`

// probeRefusal is the error the sandbox gives a denied connect.
const probeRefusal = syscall.EPERM

// syscall has no sandbox_init, so it is called as golang.org/x/sys/unix calls libc, with no cgo: the
// function is imported from libSystem by name, reached by a trampoline in nonetwork_darwin_*.s, and
// called by syscall.syscall6, linked by name, which runs it on the system stack as syscall's own libc
// calls run. go-proc's sandbox calls posix_spawn the same way. Go's linker allows the link because
// golang.org/x/sys makes it; a Go that stops allowing it fails the build, not the guard.

//go:linkname guardSyscall6 syscall.syscall6
func guardSyscall6(fn, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2 uintptr, err syscall.Errno) //nolint:revive // argument-limit: syscall's own signature, which the link must match

//go:cgo_import_dynamic libc_sandbox_init sandbox_init "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic libc_sandbox_free_error sandbox_free_error "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic libc_sandbox_check sandbox_check "/usr/lib/libSystem.B.dylib"

// The trampolines' addresses, set by nonetwork_darwin_*.s.
var (
	libcSandboxInitTrampolineAddr      uintptr //nolint:gochecknoglobals // the assembly's name, set by it and read here alone
	libcSandboxFreeErrorTrampolineAddr uintptr //nolint:gochecknoglobals // as above
	libcSandboxCheckTrampolineAddr     uintptr //nolint:gochecknoglobals // as above
)

// guardHere sets the sandbox on this process: why is "" when sandbox_init took the profile, and else
// the system's reason. The guard holds this process, so Main goes on to the probe and the tests.
func guardHere([]string) (code int, done bool, why string, err error) {
	return 0, false, sandboxInit(guardProfile), nil
}

// sandboxInit applies profile to this process, and is "" or why the system refused it. A process
// already in a sandbox is told so: macOS refuses one inside sandbox-exec a sandbox of its own.
func sandboxInit(profile string) string {
	p, err := syscall.BytePtrFromString(profile)
	if err != nil {
		return "the profile holds a NUL byte"
	}
	var msg *byte
	// sandbox_init's int and the message it writes are its whole answer; it sets no errno to read.
	r, _, _ := guardSyscall6(libcSandboxInitTrampolineAddr, uintptr(unsafe.Pointer(p)), 0, uintptr(unsafe.Pointer(&msg)), 0, 0, 0) //nolint:errcheck,gosec // G103: the call reads the profile and writes the message's address by their addresses
	if int32(r) == 0 {                                                                                                             //nolint:gosec // G115: a C int, returned in the register's low 32 bits
		return ""
	}
	why := "sandbox_init refused the profile"
	if msg != nil {
		why += ": " + cString(msg)
		guardSyscall6(libcSandboxFreeErrorTrampolineAddr, uintptr(unsafe.Pointer(msg)), 0, 0, 0, 0, 0) //nolint:errcheck,dogsled,gosec // a void call; G103: it frees the message by its address
	}
	if sandboxed() {
		why += " (this process already runs inside a sandbox, such as sandbox-exec or an agent's sandboxed shell, and macOS lets it add none)"
	}
	return why
}

// sandboxed is whether this process runs inside a sandbox, as sandbox_check(pid, NULL, 0) answers.
func sandboxed() bool {
	r, _, _ := guardSyscall6(libcSandboxCheckTrampolineAddr, uintptr(os.Getpid()), 0, 0, 0, 0, 0) //nolint:errcheck,dogsled // sandbox_check's int is its whole answer
	return int32(r) == 1                                                                          //nolint:gosec // G115: a C int, returned in the register's low 32 bits
}

// cString is the NUL-ended string at p, which libsandbox wrote.
func cString(p *byte) string {
	n := 0
	for *(*byte)(unsafe.Add(unsafe.Pointer(p), n)) != 0 { //nolint:gosec // G103: libsandbox's message, read to its NUL
		n++
	}
	return string(unsafe.Slice(p, n)) //nolint:gosec // G103: the n bytes before that NUL
}

// guardRerunEnv gives a child of Rerun nothing: the sandbox is inherited, and a guarded process may set
// it once more, which the child's Main does.
func guardRerunEnv() []string { return nil }
