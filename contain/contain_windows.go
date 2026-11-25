package contain

import (
	"errors"
	"fmt"
	"os"
	"os/exec" //nolint:depguard // the rerun inside the Job is this test binary, started in a group and a console of its own
	"os/signal"
	"syscall"
	"time"
	"unsafe"
)

// The Job Object declarations of the Windows headers that package syscall lacks, each with its name there. On
// Windows contain calls package syscall alone, with no golang.org/x/sys, so it names kernel32's
// calls itself, as the root's interrupt_windows.go does.
const (
	jobObjectExtendedLimitInformation = 9          // JobObjectExtendedLimitInformation of winnt.h
	jobObjectLimitKillOnJobClose      = 0x00002000 // JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	processSetQuota                   = 0x0100     // PROCESS_SET_QUOTA
	processTerminate                  = 0x0001     // PROCESS_TERMINATE
	jobObjectQuery                    = 0x0004     // JOB_OBJECT_QUERY
	createNoWindow                    = 0x08000000 // CREATE_NO_WINDOW
)

// kernel32 is loaded from the system folder alone: it is one of Windows' KnownDLLs.
var (
	kernel32          = syscall.NewLazyDLL("kernel32.dll")           //nolint:gochecknoglobals // the one system library of the calls below
	createJobObject   = kernel32.NewProc("CreateJobObjectW")         //nolint:gochecknoglobals // a lazily loaded entry point
	setInfoJobObject  = kernel32.NewProc("SetInformationJobObject")  //nolint:gochecknoglobals // a lazily loaded entry point
	openJobObject     = kernel32.NewProc("OpenJobObjectW")           //nolint:gochecknoglobals // a lazily loaded entry point
	assignToJobObject = kernel32.NewProc("AssignProcessToJobObject") //nolint:gochecknoglobals // a lazily loaded entry point
	isProcessInJob    = kernel32.NewProc("IsProcessInJob")           //nolint:gochecknoglobals // a lazily loaded entry point
)

// extendedLimits is JOBOBJECT_EXTENDED_LIMIT_INFORMATION of winnt.h, with its basic limits and its I/O counters.
type extendedLimits struct {
	perProcessUserTimeLimit, perJobUserTimeLimit int64
	limitFlags                                   uint32
	minimumWorkingSetSize, maximumWorkingSetSize uintptr
	activeProcessLimit                           uint32
	affinity                                     uintptr
	priorityClass, schedulingClass               uint32
	ioReadOps, ioWriteOps, ioOtherOps            uint64
	ioReadBytes, ioWriteBytes, ioOtherBytes      uint64
	processMemoryLimit, jobMemoryLimit           uintptr
	peakProcessMemoryUsed, peakJobMemoryUsed     uintptr
}

// waitInJob is how long the rerun child waits to be put in its Job before it ends the run.
const waitInJob = 30 * time.Second

// rerunContained runs this binary again in a Job Object that kills its processes when the Job closes,
// in a process group of its own and a console with no window, and waits. The child waits until it is in
// the Job, so nothing it starts is outside it. It returns the exit code of the tests. A system that
// refuses the Job gives refused its reason and no code: the caller runs the tests here, uncontained.
func rerunContained(start []string) (code int, refused string, err error) {
	self, err := os.Executable()
	if err != nil {
		return 0, Refused + "this system names no running test binary: " + err.Error(), nil
	}
	name := fmt.Sprintf("Local\\testkit-%d-%d", os.Getpid(), time.Now().UnixNano())
	job, err := killOnCloseJob(name)
	if err != nil {
		return 0, Refused + err.Error(), nil
	}
	defer func() { _ = syscall.CloseHandle(job) }() //nolint:errcheck // the Job is closed with the run
	cmd := exec.Command(self, os.Args[1:]...)       //nolint:gosec,noctx // Main has no context: the run ends with its child, by the signals it forwards and the death of its parent, and this test binary itself, with the arguments go test gave it
	cmd.Env = append(append([]string(nil), start...), containVar+"="+partRun+":"+name)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr //nolint:forbidigo // the run's own streams, handed to its rerun child as they are
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | createNoWindow}
	if err := cmd.Start(); err != nil {
		return 0, Refused + err.Error(), nil
	}
	h, err := syscall.OpenProcess(processSetQuota|processTerminate, false, uint32(cmd.Process.Pid)) //nolint:gosec // a process id on Windows is a DWORD, so it fits
	if err == nil {
		err = assign(job, h)
		_ = syscall.CloseHandle(h) //nolint:errcheck // the handle to the child, no longer needed
	}
	if err != nil {
		_ = cmd.Process.Kill() //nolint:errcheck // the child that was not put in the Job is ended
		_ = cmd.Wait()         //nolint:errcheck // and its end is all the run learns of it
		return 0, Refused + "the child could not be put in the Job Object: " + err.Error(), nil
	}
	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, os.Interrupt)
	defer signal.Stop(sigs)
	go func() {
		for range sigs {
			_ = cmd.Process.Kill() //nolint:errcheck // an interrupt ends the child that is the tests
		}
	}()
	if err := cmd.Wait(); err != nil {
		if _, ok := errors.AsType[*exec.ExitError](err); !ok {
			return 1, "", fmt.Errorf("the tests ran in a Job Object of their own, and waiting for it failed: %w", err)
		}
	}
	return cmd.ProcessState.ExitCode(), "", nil
}

// killOnCloseJob is a new Job Object that ends every process in it when its last handle closes.
func killOnCloseJob(name string) (syscall.Handle, error) {
	n, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return 0, fmt.Errorf("the Job Object's name: %w", err)
	}
	r, _, err := createJobObject.Call(0, uintptr(unsafe.Pointer(n))) //nolint:gosec // G103: the call takes the name by address
	if r == 0 {
		return 0, fmt.Errorf("no Job Object could be made: %w", err)
	}
	job := syscall.Handle(r)
	if errors.Is(err, syscall.ERROR_ALREADY_EXISTS) {
		_ = syscall.CloseHandle(job) //nolint:errcheck // the handle to a Job already made, which is not this run's to kill on close
		return 0, fmt.Errorf("the Job Object's name %s is a Job's already, not a new one's: %w", name, err)
	}
	limits := extendedLimits{limitFlags: jobObjectLimitKillOnJobClose}
	if ok, _, err := setInfoJobObject.Call(uintptr(job), jobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), unsafe.Sizeof(limits)); ok == 0 { //nolint:gosec // G103: the call takes the limits by address
		_ = syscall.CloseHandle(job) //nolint:errcheck // the Job that could not be set up
		return 0, fmt.Errorf("the Job Object could not be made to kill on close: %w", err)
	}
	return job, nil
}

// assign puts the process h in the Job.
func assign(job, h syscall.Handle) error {
	if ok, _, err := assignToJobObject.Call(uintptr(job), uintptr(h)); ok == 0 {
		return err
	}
	return nil
}

// supervise is never called on Windows: no run is started as the init of a namespace.
func supervise(bool) (int, error) { return 1, nil }

// keepMounts keeps nothing on Windows: a Job Object has no mounts to give a test, so ContainMounts is Contain.
func keepMounts() (string, error) { return NoContainment, nil }

// mountsHere is false: Windows has no namespace whose mounts a test could be given.
const mountsHere = false

// inJob waits up to waitInJob for this process to be in the Job named name, which contain made and the
// process that started this one puts it in. It is inJobBy with contain's own wait; a test asks with a
// short one.
func inJob(name string) bool { return inJobBy(name, waitInJob) }

// inJobBy waits up to wait for this process to be in the Job named name, and is false when it is not
// within it. It opens the Job by its name for a moment, so this process holds no handle that would keep
// the Job open, and asks IsProcessInJob of that Job alone: a Job this process was started in by a
// runner or a terminal, or any other Job it is in, is no answer. A check of "any Job" (a nil Job handle)
// would take one of those for contain's own.
func inJobBy(name string, wait time.Duration) bool {
	if name == "" {
		return false
	}
	n, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return false
	}
	for deadline := time.Now().Add(wait); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		r, _, _ := openJobObject.Call(jobObjectQuery, 0, uintptr(unsafe.Pointer(n))) //nolint:errcheck,gosec // G103: the call takes the name by address; the handle it returns is its answer
		if r == 0 {
			continue
		}
		var in int32                                                                    // a BOOL
		self, _ := syscall.GetCurrentProcess()                                          //nolint:errcheck // the pseudo-handle of this process, which GetCurrentProcess never fails to give
		ok, _, _ := isProcessInJob.Call(uintptr(self), r, uintptr(unsafe.Pointer(&in))) //nolint:errcheck,gosec // G103: the call takes the result by address; the BOOL it returns is its answer
		_ = syscall.CloseHandle(syscall.Handle(r))                                      //nolint:errcheck // the moment's handle
		if ok != 0 && in != 0 {
			return true
		}
	}
	return false
}

// entered is whether this run, started by the process that made the Job named arg, is in that Job.
func entered(arg string) bool { return inJob(arg) }

// strayEnds is true on Windows: a child started to wait for its place in a Job that is in none ends the run.
const strayEnds = true
