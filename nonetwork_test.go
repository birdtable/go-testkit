package testkit

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"
)

// guardWord, as the first argument and with a mode, an address and a refusal after it, makes this test
// binary a run of Main with or without NoNetwork, whose one "test" dials the address and reports each
// result on a line of its own: guardChild.
const guardWord = "testkit-guard"

// plain is the refusal of a mode that forces none, and a refused mode's usual one (forceRefusal).
const plain = "plain"

// dialWord, as the first argument and with an address after it, makes this test binary dial it once and
// print the result, as a program a test starts would: dialChild.
const dialWord = "testkit-dial"

// The modes of guardChild.
const (
	modeOn       = "on"        // NoNetwork: every result, a child's and a nested run's too
	modeOff      = "off"       // no NoNetwork: the same dials, as a run has them today
	modeInner    = "inner"     // NoNetwork, started by an on run with what Rerun would give: no new guard
	modeRefused  = "refused"   // NoNetwork where the guard cannot be set (forceRefusal)
	modeAccepted = "unguarded" // NoNetwork(hostOS()) where the guard cannot be set
)

// guardChild is this binary as guardWord makes it: Main with the mode's options, whose run reports, and
// 3 with Main's error printed when Main refused. Each process of a guard's rerun is this too, with the
// same arguments, so a forced refusal holds in every one of them.
func guardChild(mode, addr, refusal string) int {
	var opts []Option
	switch mode {
	case modeOn, modeInner:
		opts = append(opts, NoNetwork())
	case modeRefused:
		forceRefusal(refusal)
		opts = append(opts, NoNetwork())
	case modeAccepted:
		forceRefusal(refusal)
		opts = append(opts, NoNetwork(hostOS()))
	}
	code, err := Main(runner(func() int { return guardReport(mode, addr) }), opts...)
	if err != nil {
		fmt.Println("main error:", err) //nolint:forbidigo // the child's report, read by the test that started it
		return 3
	}
	return code
}

// guardReport is guardChild's run: one line per result, "ok" where a call succeeded and its error where
// it failed.
func guardReport(mode, addr string) int {
	say := func(what string, err error) {
		v := "ok"
		if err != nil {
			v = err.Error()
		}
		fmt.Printf("%s: %s\n", what, v) //nolint:forbidigo // the child's report, read by the test that started it
	}
	fmt.Println("ran") //nolint:forbidigo // as above
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }))
	defer srv.Close()
	r, err := srv.Client().Get(srv.URL) //nolint:noctx // a bounded call to the run's own server
	if err == nil {
		_ = r.Body.Close()
	}
	say("loopback", err)
	say("dial", dialOnce(addr))
	r, err = (&http.Client{Timeout: 3 * time.Second}).Get("http://" + addr + "/") //nolint:noctx,gosec // a fresh client, as the code under test would make; G704: the test's own address, which the guard must refuse
	if err == nil {
		_ = r.Body.Close()
	}
	say("client", err)
	netns, _ := os.Readlink("/proc/self/ns/net") // empty where there is no such link, off Linux
	fmt.Printf("netns: %s\n", netns)             //nolint:forbidigo // as above
	if mode == modeInner || mode == modeAccepted {
		return 0
	}
	self, err := os.Executable()
	if err != nil {
		say("child", err)
		return 0
	}
	out, err := exec.Command(self, dialWord, addr).CombinedOutput() //nolint:gosec,noctx // this test binary, dialing once and ending
	if err != nil {
		say("child", fmt.Errorf("%w: %s", err, out))
	} else {
		fmt.Printf("child: %s", out) //nolint:forbidigo // as above
	}
	if mode != modeOn {
		return 0
	}
	_, err = net.DefaultResolver.LookupHost(context.Background(), "example.com")
	say("lookup", err)
	// A child of Rerun is given Clean's environment and RerunEnv's: it must hold, and not nest again.
	cmd := exec.Command(self, guardWord, modeInner, addr, plain) //nolint:gosec,noctx // as above
	cmd.Env = append(Clean(os.Environ()), guardRerunEnv()...)
	out, err = cmd.CombinedOutput()
	for line := range strings.Lines(string(out)) {
		fmt.Print("inner " + line) //nolint:forbidigo // as above
	}
	if err != nil {
		say("inner", err)
	}
	return 0
}

// dialChild dials addr once and prints the result.
func dialChild(addr string) int {
	if err := dialOnce(addr); err != nil {
		fmt.Println(err) //nolint:forbidigo // the child's report
		return 0
	}
	fmt.Println("ok") //nolint:forbidigo // as above
	return 0
}

// dialOnce dials addr over TCP, bounded by 3 s, and closes what it made.
func dialOnce(addr string) error {
	d := net.Dialer{Timeout: 3 * time.Second}
	c, err := d.DialContext(context.Background(), "tcp", addr)
	if err != nil {
		return err
	}
	return c.Close()
}

// listenLoopback is a TCP listener on a port of 127.0.0.1 the kernel chose.
func listenLoopback(t *testing.T) net.Listener {
	t.Helper()
	var lc net.ListenConfig
	l, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// runGuard starts this test binary as guardChild with mode and addr, wrapped by the words of wrap
// first when it has any, and is its exit code and its lines, and its stderr's apart.
func runGuard(t *testing.T, wrap []string, mode, addr, refusal string) (int, map[string]string, string) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), Scale(t, time.Minute, 4, 0))
	defer cancel()
	argv := append(slices.Clone(wrap), self, guardWord, mode, addr, refusal)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...) //nolint:gosec // this test binary, or the system's sandbox-exec running it
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	code := 0
	if err := cmd.Run(); err != nil {
		exit, ok := err.(*exec.ExitError) //nolint:errorlint // Run's own error type, never wrapped
		if !ok {
			t.Fatalf("the child of mode %s could not run: %v", mode, err)
		}
		code = exit.ExitCode()
	}
	lines := map[string]string{}
	sc := bufio.NewScanner(&stdout)
	for sc.Scan() {
		k, v, _ := strings.Cut(sc.Text(), ": ")
		lines[k] = v
	}
	return code, lines, stderr.String()
}

// freePort is a TCP port nothing listened on a moment ago: the kernel's choice for a listener at once closed.
func freePort(t *testing.T) string {
	t.Helper()
	l := listenLoopback(t)
	_, port, _ := net.SplitHostPort(l.Addr().String())
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}

// Planted failure: a refusal that runs the tests anyway, or says nothing of the system, makes the run
// print "ran" or Main's error miss its words. Risk: a host where the guard holds fails it, as the
// refusal is forced (forceRefusal) and on Windows is the system's.
func TestNoNetworkFailsMainNamingTheSystemWhereTheGuardCannotBeSet(t *testing.T) {
	t.Parallel()
	refused(t, plain)
}

// refused runs a guard refused as refusal says, and checks that no test ran and Main's error names the
// system and each of refusalWords.
func refused(t *testing.T, refusal string) {
	t.Helper()
	code, got, stderr := runGuard(t, refusalWrap(refusal), modeRefused, probeTarget, refusal)
	if _, ran := got["ran"]; ran || code != 3 {
		t.Fatalf("a refused guard (%s) ended %d and ran %v, want 3 and no test run: %v\n%s", refusal, code, ran, got, stderr)
	}
	for _, want := range append([]string{"no network guard on " + hostOS() + ": "}, refusalWords(refusal)...) {
		if !strings.Contains(got["main error"], want) {
			t.Errorf("Main's error is %q, want it to say %q\n%s", got["main error"], want, stderr)
		}
	}
}

// Planted failure: a named system that still fails, or runs silently, makes the run end 3 or the stderr
// line go missing. Risk: as the test above.
func TestNoNetworkNamingTheSystemRunsTheTestsUnguardedAndSaysSo(t *testing.T) {
	t.Parallel()
	code, got, stderr := runGuard(t, refusalWrap(plain), modeAccepted, probeTarget, plain)
	if _, ran := got["ran"]; !ran || code != 0 {
		t.Fatalf("an accepted refusal ended %d and ran %v, want 0 and the tests run: %v\n%s", code, ran, got, stderr)
	}
	want := fmt.Sprintf("testkit: no network guard on %s: ", hostOS())
	if !strings.Contains(stderr, want) || !strings.Contains(stderr, "the tests run with the network open") {
		t.Errorf("stderr is %q, want the line %q... saying the network is open", stderr, want)
	}
}

// Planted failure: a name taken unchecked lets "macos" through. Risk: none.
func TestNoNetworkRefusesANameThatIsNoGOOS(t *testing.T) {
	t.Parallel()
	code, done, err := noNetwork(nil, []string{"linux", "macos"})
	if !done || code != 1 || err == nil || !strings.Contains(err.Error(), `"macos", which is no GOOS`) {
		t.Fatalf("NoNetwork(\"linux\", \"macos\") gave %d, %v, %v; want the run refused, naming macos", code, done, err)
	}
}

// Planted failure: a probe that takes any error, or a made connection, as the guard's passes a guard
// that holds nothing. Risk: none; it dials loopback alone.
func TestTheProbeFailsAGuardThatHoldsNothing(t *testing.T) {
	t.Parallel()
	l := listenLoopback(t)
	t.Cleanup(func() { _ = l.Close() })
	if why := probe(l.Addr().String(), probeRefusal); !strings.Contains(why, "was made, so the guard holds nothing") {
		t.Errorf("a probe whose connection was made says %q", why)
	}
	closed := "127.0.0.1:" + freePort(t)
	if why := probe(closed, probeRefusal); !strings.Contains(why, "but not with the guard's") {
		t.Errorf("a probe whose connection was refused by no guard says %q", why)
	}
}
