//go:build linux

package testkit

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec" //nolint:depguard // the rerun inside the namespaces is this test binary, started with clone flags no runner makes
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"unsafe"
)

// probeRefusal is the error a connect gets in a network namespace where only loopback is up: no route.
const probeRefusal = syscall.ENETUNREACH

// guardVar carries the part of the guard a process is, from the process that made the namespaces to the
// one it starts. It is testkit's own, never a name a caller sets: Isolate keeps it from the tests, and
// RerunEnv hands it to a child of Rerun. Its value is "init:<uid>:<gid>:<net>" for the root of the new
// namespaces, or "run:<net>" for the tests, net being the link of the network namespace the run started
// in (/proc/self/ns/net, "net:[<inode>]"). A stray value decides nothing: a run is guarded only in a
// network namespace other than that one whose every interface is loopback, and only once the probe
// passes there; any other run starts the namespaces itself.
const guardVar = "TESTKIT_NONETWORK"

// The parts guardVar names.
const (
	guardInit = "init" // root of the new user and network namespace: brings loopback up and starts the tests
	guardRun  = "run"  // the tests, in a nested user namespace with the caller's ids
)

// guardReady is the one line the namespace's root writes to its parent once loopback is up and the tests
// are started; any other line is why the guard could not be set.
const guardReady = "guarded"

// netLink is the file whose link names this process's network namespace.
const netLink = "/proc/self/ns/net"

// The seams of testkit's own tests of a refusal, each written only by them, before Main, and each able
// only to make the guard fail: guardCloneflags the namespaces the root is made in, and loopbackName the
// interface it brings up.
var (
	guardCloneflags uintptr = syscall.CLONE_NEWUSER | syscall.CLONE_NEWNET //nolint:gochecknoglobals // a refusal's test writes flags the system refuses
	loopbackName            = "lo"                                         //nolint:gochecknoglobals // a refusal's test writes an interface there is none of
)

// guardedFrom is the network namespace this run started in, set once the probe passed in a run that is
// the tests of the guard, and read by RerunEnv for a child of Rerun.
var guardedFrom atomic.Pointer[string] //nolint:gochecknoglobals // set once by Main, before any test, read by Rerun from parallel tests

// guardHere is NoNetwork on Linux. The first run starts the namespaces' root and returns its tests' exit
// code with done, or why it could not; the root does its part and ends; the tests return, for the probe.
func guardHere(start []string) (code int, done bool, why string, err error) {
	part, arg, _ := strings.Cut(lookup(start, guardVar), ":")
	switch part {
	case guardRun:
		if here, err := os.Readlink(netLink); err == nil && arg != "" && here != arg && onlyLoopback() {
			guardedFrom.Store(&arg)
			return 0, false, "", nil
		}
	case guardInit:
		if code, ok := guardRoot(arg); ok {
			return code, true, "", nil
		}
	}
	return guardFirst(start)
}

// guardFirst starts this binary again as the root of a user and network namespace of its own, and waits
// for the tests it starts there. why is set, and nothing waited for, where the system refused.
func guardFirst(start []string) (code int, done bool, why string, err error) {
	outer, err := os.Readlink(netLink)
	if err != nil {
		return 0, false, "this process's network namespace cannot be read: " + err.Error(), nil
	}
	self, err := os.Executable()
	if err != nil {
		return 0, false, "this system names no running test binary: " + err.Error(), nil
	}
	r, w, err := os.Pipe()
	if err != nil {
		return 0, false, "the pipe to the namespace's root: " + err.Error(), nil
	}
	defer func() { _ = r.Close() }() //nolint:errcheck // a pipe read to its line
	uid, gid := os.Getuid(), os.Getgid()
	cmd := exec.Command(self, os.Args[1:]...) //nolint:gosec,noctx // this test binary, with the arguments go test gave it; it ends with the signals passed on and its parent's death
	cmd.Env = append(withGoResolver(withoutGuardVar(start)), fmt.Sprintf("%s=%s:%d:%d:%s", guardVar, guardInit, uid, gid, outer))
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr //nolint:forbidigo // the run's own streams, handed on as they are
	cmd.ExtraFiles = []*os.File{w}
	// The caller's ids are root of the new user namespace, which owns the network namespace, so the root
	// may bring loopback up there; the tests then run as the caller's ids again, a namespace further in.
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags:                 guardCloneflags,
		UidMappings:                []syscall.SysProcIDMap{{ContainerID: 0, HostID: uid, Size: 1}},
		GidMappings:                []syscall.SysProcIDMap{{ContainerID: 0, HostID: gid, Size: 1}},
		GidMappingsEnableSetgroups: false,
		Pdeathsig:                  syscall.SIGKILL,
	}
	code, why, err = runGuarded(cmd, r, w)
	return code, why == "", why, err
}

// guardRoot is the root of the new namespaces: it brings loopback up, starts the tests in a nested user
// namespace with the caller's ids, tells its parent, and is their exit code. ok is false where this
// process is no root the first run made, which then starts as a first run itself.
func guardRoot(arg string) (code int, ok bool) {
	ids := strings.SplitN(arg, ":", 3)
	if len(ids) != 3 || os.Getuid() != 0 {
		return 0, false
	}
	uid, uerr := strconv.Atoi(ids[0])
	gid, gerr := strconv.Atoi(ids[1])
	// fd 3 is checked before it is wrapped: an *os.File of a descriptor this process does not own
	// would close it when collected.
	var st syscall.Stat_t
	if uerr != nil || gerr != nil || syscall.Fstat(3, &st) != nil || st.Mode&syscall.S_IFMT != syscall.S_IFIFO {
		return 0, false
	}
	// It came as an extra file, so with no close-on-exec: set, so the tests do not hold the pipe open.
	syscall.CloseOnExec(3)
	ready := os.NewFile(3, "ready")
	defer func() { _ = ready.Close() }() //nolint:errcheck // the pipe to the parent, written to its line
	// A root still in the network namespace the run started in holds nothing: it says so and ends,
	// rather than start the tests or a namespace again.
	if here, err := os.Readlink(netLink); err != nil || here == ids[2] {
		_, _ = fmt.Fprintf(ready, "the new network namespace is the one the run started in (%s)\n", ids[2]) //nolint:errcheck // the parent reads it or has ended
		return 1, true
	}
	if err := loopbackUp(loopbackName); err != nil {
		_, _ = fmt.Fprintf(ready, "loopback could not be brought up in the new network namespace: %v\n", err) //nolint:errcheck // the parent reads it or has ended
		return 1, true
	}
	self, err := os.Executable()
	if err != nil {
		_, _ = fmt.Fprintf(ready, "this system names no running test binary: %v\n", err) //nolint:errcheck // as above
		return 1, true
	}
	cmd := exec.Command(self, os.Args[1:]...) //nolint:gosec,noctx // as in guardFirst
	cmd.Env = append(withoutGuardVar(os.Environ()), guardVar+"="+guardRun+":"+ids[2])
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr //nolint:forbidigo // as in guardFirst
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags:                 syscall.CLONE_NEWUSER,
		UidMappings:                []syscall.SysProcIDMap{{ContainerID: uid, HostID: 0, Size: 1}},
		GidMappings:                []syscall.SysProcIDMap{{ContainerID: gid, HostID: 0, Size: 1}},
		GidMappingsEnableSetgroups: false,
		Pdeathsig:                  syscall.SIGKILL,
	}
	code, why, err := runGuarded(cmd, nil, ready)
	if why != "" {
		_, _ = fmt.Fprintln(ready, why) //nolint:errcheck // as above
		return 1, true
	}
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "testkit:", err) //nolint:forbidigo // the tests ran; the parent has the code, and the run's log the reason
	}
	return code, true
}

// runGuarded starts cmd, and with r reads the one line its child writes to w; with no r, it writes
// guardReady to w itself once cmd started. It then passes on the run's signals to cmd and is its exit
// code. why is set, and nothing waited for, when cmd did not start or its child said why not.
func runGuarded(cmd *exec.Cmd, r, w *os.File) (code int, why string, err error) {
	// Pdeathsig follows the thread that started the child, not the process.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	err = cmd.Start()
	if r != nil {
		_ = w.Close() //nolint:errcheck // the parent's end of the child's pipe, closed once the child holds its own
	}
	if err != nil {
		return 0, "the system refused the namespace: " + err.Error(), nil
	}
	if r != nil {
		line, _ := bufio.NewReader(r).ReadString('\n') //nolint:errcheck // a pipe that ended with no line is the root's end, and line is empty then
		if line = strings.TrimSpace(line); line != guardReady {
			_ = cmd.Wait() //nolint:errcheck // a root that could not set the guard has ended, and line says why
			if line == "" {
				line = "the namespace's root ended before it said the guard was set"
			}
			return 0, line, nil
		}
	} else {
		_, _ = fmt.Fprintln(w, guardReady) //nolint:errcheck // the parent reads it or has ended
		_ = w.Close()                      //nolint:errcheck // the line is written
	}
	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	defer signal.Stop(sigs)
	go func() {
		for s := range sigs {
			_ = cmd.Process.Signal(s) //nolint:errcheck // a signal to a child that has ended
		}
	}()
	if err := cmd.Wait(); err != nil {
		if _, ok := errors.AsType[*exec.ExitError](err); !ok {
			return 1, "", fmt.Errorf("the tests ran with the network guarded, and waiting for them failed: %w", err)
		}
	}
	if ws, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal()), "", nil
	}
	return cmd.ProcessState.ExitCode(), "", nil
}

// ifreq is the kernel's struct ifreq as SIOCGIFFLAGS and SIOCSIFFLAGS read it: the name, then the flags
// at the head of the union, padded to the 40 bytes of a 64-bit system, which a 32-bit kernel reads less of.
type ifreq struct {
	name  [syscall.IFNAMSIZ]byte
	flags uint16
	_     [22]byte
}

// loopbackUp brings the interface named up, as ip link set lo up does.
func loopbackUp(name string) error {
	fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_DGRAM|syscall.SOCK_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("a socket to set it by: %w", err)
	}
	defer func() { _ = syscall.Close(fd) }() //nolint:errcheck // a socket used for two ioctls
	var req ifreq
	copy(req.name[:syscall.IFNAMSIZ-1], name)
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), syscall.SIOCGIFFLAGS, uintptr(unsafe.Pointer(&req))); e != 0 { //nolint:gosec // G103: the ioctl reads and writes the request by its address
		return fmt.Errorf("reading the flags of %s: %w", name, e)
	}
	req.flags |= syscall.IFF_UP
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), syscall.SIOCSIFFLAGS, uintptr(unsafe.Pointer(&req))); e != 0 { //nolint:gosec // G103: as above
		return fmt.Errorf("setting %s up: %w", name, e)
	}
	return nil
}

// onlyLoopback is whether every network interface this process sees is a loopback one, as in the
// namespace the guard made, and none is a way out.
func onlyLoopback() bool {
	ifs, err := net.Interfaces()
	if err != nil || len(ifs) == 0 {
		return false
	}
	for _, i := range ifs {
		if i.Flags&net.FlagLoopback == 0 {
			return false
		}
	}
	return true
}

// withoutGuardVar is environ with guardVar left out.
func withoutGuardVar(environ []string) []string {
	out := make([]string, 0, len(environ))
	for _, kv := range environ {
		if !strings.HasPrefix(kv, guardVar+"=") {
			out = append(out, kv)
		}
	}
	return out
}

// withGoResolver is environ with netdns=go after the GODEBUG settings it has, which it keeps, as the last
// of a setting wins: the rerun's Go programs look names up with Go's own resolver, which the guard
// refuses, and not the C library's, which may reach a daemon over a Unix socket.
func withGoResolver(environ []string) []string {
	out, debug := make([]string, 0, len(environ)+1), ""
	for _, kv := range environ {
		if v, ok := strings.CutPrefix(kv, "GODEBUG="); ok {
			if debug = ""; v != "" {
				debug = v + ","
			}
			continue
		}
		out = append(out, kv)
	}
	return append(out, "GODEBUG="+debug+"netdns=go")
}

// guardRerunEnv gives a child of Rerun the guard this run is in, so it checks itself against it and is
// not guarded a second time; and nothing in a run that is not the guard's tests.
func guardRerunEnv() []string {
	if from := guardedFrom.Load(); from != nil {
		return []string{guardVar + "=" + guardRun + ":" + *from}
	}
	return nil
}
