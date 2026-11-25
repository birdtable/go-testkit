package testkit

import (
	"errors"
	"fmt"
	"os"
	"os/exec" //nolint:depguard // the child Rerun starts, given the start this system's interrupt needs
	"syscall"
)

// ownGroup starts the child in a process group of its own, so CTRL_BREAK_EVENT reaches it and its
// children alone, and never the test run that started it.
func ownGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

// held is a child RerunInterrupted may interrupt, by a handle of its own. Wait closes os.Process's
// handle, after which Windows may give the child's id, which is its group's id too, to a new process;
// while this handle is open it gives it to none, so the event reaches this child's group and no other.
type held struct {
	h   syscall.Handle
	pid uint32
}

func hold(p *os.Process) (held, error) {
	pid := uint32(p.Pid) //nolint:gosec // a process id on Windows is a DWORD, so it fits
	h, err := syscall.OpenProcess(syscall.SYNCHRONIZE, false, pid)
	if err != nil {
		return held{}, fmt.Errorf("opening process %d: %w", pid, err)
	}
	return held{h: h, pid: pid}, nil
}

// interrupt sends CTRL_BREAK_EVENT to the child's group, which Go's os/signal hands the child as
// os.Interrupt. CTRL_C_EVENT is not sent: a group of its own ignores it.
// GenerateConsoleCtrlEvent is kernel32's, which syscall does not name. kernel32.dll is one of Windows'
// KnownDLLs, which it loads from its own system folder and never from a path a caller set.
func (h held) interrupt() error {
	send := syscall.NewLazyDLL("kernel32.dll").NewProc("GenerateConsoleCtrlEvent")
	if ok, _, err := send.Call(syscall.CTRL_BREAK_EVENT, uintptr(h.pid)); ok == 0 {
		return fmt.Errorf("sending CTRL_BREAK_EVENT to group %d: %w", h.pid, err)
	}
	return nil
}

// terminate sends nothing: Windows has no SIGTERM, and its Process.Signal sends only a kill.
func (held) terminate() error {
	return fmt.Errorf("testkit: Terminate: Windows has no SIGTERM, so nothing was sent; Interrupt and Kill reach the program: %w", errors.ErrUnsupported)
}

func (h held) release() error { return syscall.CloseHandle(h.h) }

// killLeavesACode is true on Windows: TerminateProcess gives the program code 1, which Running's Wait
// cannot tell from the program's own, so it reports a program its Kill ended as -1. A Kill after the
// program ended by itself fails, so the program keeps its own code.
const killLeavesACode = true
