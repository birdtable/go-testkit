package contain

import (
	"syscall"
	"unsafe"
)

// The rows read and change one locked thread's capabilities, securebits and no_new_privs, and nothing of
// the other threads of the tests: libcap/cap, which contain's own code calls, changes every thread of the
// process, as it must there, which in a row would reach the parallel tests' threads too. So the rows make
// these thread-local calls themselves, as go-proc's testenv did, with the declarations of
// linux/capability.h, linux/prctl.h and linux/securebits.h they need.
const (
	capSysAdmin       = 21         // CAP_SYS_ADMIN
	capVersion3       = 0x20080522 // _LINUX_CAPABILITY_VERSION_3: two 32-bit words per set
	prCapAmbientRaise = 2          // PR_CAP_AMBIENT_RAISE
	prGetSecurebits   = 27         // PR_GET_SECUREBITS
)

// sysAdmin is CAP_SYS_ADMIN's bit in the first word of a set.
const sysAdmin = uint32(1) << capSysAdmin

// capHeader and capData are capget's and capset's arguments, struct __user_cap_header_struct and
// struct __user_cap_data_struct; version 3 takes two capData, the low 32 capabilities and the high.
type capHeader struct {
	version uint32
	pid     int32
}

type capData struct {
	effective, permitted, inheritable uint32
}

// threadCaps is the calling thread's effective, permitted and inheritable sets.
func threadCaps() ([2]capData, error) {
	hdr, d := capHeader{version: capVersion3}, [2]capData{}
	if _, _, e := syscall.RawSyscall(syscall.SYS_CAPGET, uintptr(unsafe.Pointer(&hdr)), uintptr(unsafe.Pointer(&d[0])), 0); e != 0 { //nolint:gosec // G103: capget takes its header and sets by address
		return d, e
	}
	return d, nil
}

// setThreadCaps sets the calling thread's sets to d.
func setThreadCaps(d [2]capData) error {
	hdr := capHeader{version: capVersion3}
	if _, _, e := syscall.RawSyscall(syscall.SYS_CAPSET, uintptr(unsafe.Pointer(&hdr)), uintptr(unsafe.Pointer(&d[0])), 0); e != 0 { //nolint:gosec // G103: capset takes its header and sets by address
		return e
	}
	return nil
}

// prctl is the prctl call on the calling thread, with its result.
func prctl(option, arg2, arg3 uintptr) (uintptr, error) {
	r, _, e := syscall.RawSyscall6(syscall.SYS_PRCTL, option, arg2, arg3, 0, 0, 0)
	if e != 0 {
		return 0, e
	}
	return r, nil
}
