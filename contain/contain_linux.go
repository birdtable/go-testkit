package contain

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec" //nolint:depguard // the rerun inside the namespace is this test binary, started with the clone flags no other package makes
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"kernel.org/pub/linux/libs/security/libcap/cap"
)

// said is the one line init writes to its parent once the tests are started in the containment.
const said = "contained"

// cloneflags are the namespaces the init is made in; a test of the refusal sets flags the system refuses.
var cloneflags uintptr = syscall.CLONE_NEWUSER | syscall.CLONE_NEWPID | syscall.CLONE_NEWNS //nolint:gochecknoglobals // one value, written by the test of a refused namespace and read by no other

// rerunContained runs this binary again as the init of a user and PID namespace of its own, which
// mounts a fresh /proc, then runs the tests as its child, and waits. It returns the exit code of the
// tests. A system that refuses a namespace, or a /proc, gives refused its reason and no code: the
// caller runs the tests here, uncontained.
func rerunContained(start []string) (code int, refused string, err error) {
	self, err := os.Executable()
	if err != nil {
		return 0, Refused + "this system names no running test binary: " + err.Error(), nil
	}
	uid, gid := os.Getuid(), os.Getgid()
	// The ready pipe: init writes one line to it, "contained" when /proc is its own, else why not.
	r, w, err := os.Pipe()
	if err != nil {
		return 0, Refused + err.Error(), nil
	}
	defer func() { _ = r.Close() }()          //nolint:errcheck // a pipe that is read to its line
	cmd := exec.Command(self, os.Args[1:]...) //nolint:gosec,noctx // Main has no context: the run ends with its child, by the signals it forwards and the death of its parent, and this test binary itself, with the arguments go test gave it
	cmd.Env = append(append([]string(nil), start...), containVar+"="+partInit)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr //nolint:forbidigo // the run's own streams, handed to its rerun child as they are
	cmd.ExtraFiles = []*os.File{w}
	// The one account of this process is the account in the namespace, as unshare --map-current-user has
	// it, so a test sees the files it made as its own. CAP_SYS_ADMIN is ambient to make /proc (and, under
	// ContainMounts, for the tests' exec), CAP_SETPCAP to set the securebits the tests start under: an
	// execve as a uid other than 0 leaves init the ambient set alone, and init drops both before the tests.
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags:                 cloneflags,
		UidMappings:                []syscall.SysProcIDMap{{ContainerID: uid, HostID: uid, Size: 1}},
		GidMappings:                []syscall.SysProcIDMap{{ContainerID: gid, HostID: gid, Size: 1}},
		GidMappingsEnableSetgroups: false,
		AmbientCaps:                []uintptr{uintptr(cap.SYS_ADMIN), uintptr(cap.SETPCAP)},
		Pdeathsig:                  syscall.SIGKILL,
	}
	// Pdeathsig is the death of the thread that started the child, not of the process.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	err = cmd.Start()
	_ = w.Close() //nolint:errcheck // the parent's end of the child's pipe, closed once the child has its own
	if err != nil {
		return 0, Refused + err.Error(), nil
	}
	line, _ := bufio.NewReader(r).ReadString('\n') //nolint:errcheck // a pipe that ended without its line is the init's end, and line is empty then
	if line = strings.TrimSpace(line); line != said {
		_ = cmd.Wait() //nolint:errcheck // a child that refused the containment ended, and the line says why
		if line == "" {
			line = "the namespace's init ended before it said it was ready"
		}
		return 0, Refused + line, nil
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
			return 1, "", fmt.Errorf("the tests ran in a namespace of their own, and waiting for it failed: %w", err)
		}
	}
	return exitCode(cmd.ProcessState), "", nil
}

// exitCode is the code the shell would report for a process that ended as ps did.
func exitCode(ps *os.ProcessState) int {
	if ws, ok := ps.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	return ps.ExitCode()
}

// supervise is the binary as PID 1 of the namespace: it mounts a fresh /proc, drops what it holds on the
// thread it starts the tests from, sets the securebits they start under, tells its parent it did,
// starts the tests as its child, reaps every process that is left with no parent, and ends with the
// code of its child, which ends everything left in the namespace. mounts is ContainMounts.
func supervise(mounts bool) (code int, err error) {
	if os.Getpid() != 1 {
		return 1, errors.New("the binary was started as the init of a namespace and is not PID 1, so none ran") // before fd 3 is touched: it is no pipe of ours
	}
	ready := os.NewFile(3, "ready")
	say := func(s string) {
		if ready != nil {
			_, _ = fmt.Fprintln(ready, s) //nolint:errcheck // the one line to the parent, which ends the run by its own wait when it is not read
			_ = ready.Close()             //nolint:errcheck // the pipe's end, closed with its one line written
		}
	}
	if err := syscall.Mount("", "/", "", syscall.MS_REC|syscall.MS_PRIVATE, ""); err != nil {
		say("the mounts of the namespace could not be made private: " + err.Error())
		return 1, nil
	}
	if err := syscall.Mount("proc", "/proc", "proc", syscall.MS_NOSUID|syscall.MS_NODEV|syscall.MS_NOEXEC, ""); err != nil {
		say("a fresh /proc could not be mounted: " + err.Error())
		return 1, nil
	}
	exe, err := os.Executable()
	if err != nil {
		say("this system names no running test binary: " + err.Error())
		return 1, nil
	}
	ns, err := os.Readlink("/proc/self/ns/pid")
	if err != nil {
		say("the namespace's identity could not be read: " + err.Error())
		return 1, nil
	}
	// Dumpable is per process, and init's other threads hold what the tests do not: not dumpable, no test and
	// nothing it starts opens init's memory or maps, or ptraces it. Its /proc/1/stat stays readable to entered.
	if _, err := cap.Prctl(prSetDumpable, 0); err != nil {
		say("init could not make itself not dumpable: " + err.Error())
		return 1, nil
	}
	part := partRun
	if mounts {
		part = partMounts
	}
	pid, why := startTests(exe, append(withoutVar(os.Environ(), containVar), containVar+"="+part+":"+ns), mounts)
	if why != "" {
		say(why)
		return 1, nil
	}
	say(said)
	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	go func() {
		for s := range sigs {
			if sg, ok := s.(syscall.Signal); ok {
				_ = syscall.Kill(pid, sg) //nolint:errcheck // a signal to a child that has ended
			}
		}
	}()
	for {
		var ws syscall.WaitStatus
		got, err := syscall.Wait4(-1, &ws, 0, nil)
		switch {
		case errors.Is(err, syscall.EINTR):
			continue
		case err != nil:
			return 1, fmt.Errorf("the namespace's init could not wait for its child: %w", err)
		case got != pid:
			continue // an orphan of a test, reaped
		case ws.Signaled():
			return 128 + int(ws.Signal()), nil
		}
		return ws.ExitStatus(), nil
	}
}

// dropRefused is why the launcher's callback could not drop what init holds, said to init's parent as it is.
type dropRefused string

func (d dropRefused) Error() string { return string(d) }

// startTests starts exe, this test binary, as the tests, in the environment env, and is its process, or
// why it could not. Ambient CAP_SYS_ADMIN served the mount; the tests are given it only under
// ContainMounts. A thread's capabilities and securebits are its own, and the tests take those of the
// thread that forks them, so the tests are started by a cap.Launcher: its callback runs dropForTests on the
// one thread that then forks them, where each cap call changes that thread alone, and the thread ends
// with the launch, so no thread of init is left with other credentials than init's. The launcher forks
// by syscall.ForkExec, not os/exec: init waits for every process in the namespace with Wait4(-1), which a
// Cmd's own Wait must not race.
func startTests(exe string, env []string, mounts bool) (pid int, why string) {
	l := cap.NewLauncher(exe, os.Args, env) // the tests keep init's streams: the launcher hands on descriptors 0, 1 and 2
	l.Callback(func(*syscall.ProcAttr, any) error {
		if why := dropForTests(mounts); why != "" {
			return dropRefused(why)
		}
		return nil
	})
	pid, err := l.Launch(nil)
	if refused, ok := errors.AsType[dropRefused](err); ok {
		return 0, string(refused)
	}
	if err != nil {
		return 0, "the tests could not be started: " + err.Error()
	}
	return pid, ""
}

// withoutVar is environ with each entry of the variable name left out.
func withoutVar(environ []string, name string) []string {
	out := make([]string, 0, len(environ))
	for _, e := range environ {
		if !strings.HasPrefix(e, name+"=") {
			out = append(out, e)
		}
	}
	return out
}

// entered is whether this run is in the namespace contain's init made, found by the run itself, never by the
// variable alone: arg is the identity of that namespace as init gave it (its link in /proc/self/ns/pid), and
// the run is in it when it is not PID 1, its own PID namespace is that one, and PID 1 of it is a process of this
// binary's name. A stray value of the variable names no namespace this run is in, so it makes no contained run.
// PID 1's own files are not read: it was started with a capability, so its /proc entries are the system's.
func entered(arg string) bool { return enteredBy(arg, "/proc/self/ns/pid", "/proc/1/stat") }

// enteredBy is entered, with the link of this run's PID namespace and PID 1's stat file given.
func enteredBy(arg, nsLink, pid1Stat string) bool {
	if arg == "" || os.Getpid() == 1 {
		return false
	}
	if ns, err := os.Readlink(nsLink); err != nil || ns != arg {
		return false
	}
	self, err := os.Executable()
	if err != nil {
		return false
	}
	b, err := os.ReadFile(pid1Stat) //nolint:gosec // G304: the file is PID 1's stat, or the one a test of this check gives
	if err != nil {
		return false
	}
	// "1 (comm) S ...": comm is the program's name cut to 15 bytes, and may hold spaces and brackets itself.
	open, shut := strings.IndexByte(string(b), '('), strings.LastIndexByte(string(b), ')')
	if open < 0 || shut < open {
		return false
	}
	want := filepath.Base(self)
	if len(want) > 15 {
		want = want[:15]
	}
	return string(b[open+1:shut]) == want
}

// strayEnds is false: a run whose variable is no namespace's word is made contained by a rerun of its own.
const strayEnds = false
