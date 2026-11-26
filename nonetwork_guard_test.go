//go:build darwin || linux

package testkit

import (
	"net"
	"strings"
	"testing"
)

// Planted failure: a NoNetwork that sets no guard, or a profile or namespace that lets the address
// through, makes the on run's dial, client, child or inner line read "ok" or "connection refused".
// Risk: a guard that refuses loopback too fails every package that serves httptest. It is built only
// where there is a guard, as a skip is red in CI; elsewhere the refusal's tests cover NoNetwork.
func TestUnderNoNetworkADialOutFailsNamingTheAddressAndLoopbackWorks(t *testing.T) {
	t.Parallel()
	addr, local := guardedAddr(t), localAddr(t)
	refused := probeRefusal.Error()
	code, on, stderr := runGuard(t, nil, modeOn, addr, plain)
	if _, ran := on["ran"]; code != 0 || !ran {
		t.Fatalf("the guarded run ended %d, ran %v, Main's error %q: under an outer sandbox the guard cannot be set, and this test fails rather than skip\n%v\n%s", code, ran, on["main error"], on, stderr)
	}
	if on["loopback"] != "ok" || on["inner loopback"] != "ok" {
		t.Errorf("under the guard the httptest server gave %q, and in a child of Rerun %q; want ok\n%s", on["loopback"], on["inner loopback"], stderr)
	}
	for _, k := range []string{"dial", "client", "child", "inner dial", "inner client"} {
		if v := on[k]; !strings.Contains(v, refused) || !strings.Contains(v, addr) {
			t.Errorf("under the guard %s gave %q, want %q naming %s\n%s", k, v, refused, addr, stderr)
		}
	}
	guardedHolds(t, on)
	code, off, stderr := runGuard(t, nil, modeOff, local, plain)
	if code != 0 || off["loopback"] != "ok" {
		t.Fatalf("the run with no guard ended %d, loopback %q\n%s", code, off["loopback"], stderr)
	}
	for _, k := range []string{"dial", "client", "child"} {
		if v := off[k]; v == "" || strings.Contains(v, refused) {
			t.Errorf("with no guard %s gave %q, want it as today: ok, or the host's own refusal", k, v)
		}
	}
}

// localAddr is an address of this host that is not loopback, on a port nothing listens on: a dial to it
// is refused by the host itself, so no packet leaves the machine. On Linux the guard refuses it, as the
// new namespace does not have it. A host with no such address fails the test, saying so.
func localAddr(t *testing.T) string {
	t.Helper()
	ifs, err := net.Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range ifs {
		if i.Flags&net.FlagUp == 0 || i.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := i.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && !n.IP.IsLinkLocalUnicast() {
				return net.JoinHostPort(n.IP.String(), freePort(t))
			}
		}
	}
	t.Fatal("this host has no IPv4 address but loopback, and the guard's test dials one to show it refused with no packet leaving")
	return ""
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
