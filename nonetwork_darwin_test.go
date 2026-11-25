//go:build darwin

package testkit

import (
	"strings"
	"testing"
)

// forceRefusal does nothing on macOS: the refused run is started inside sandbox-exec (refusalWrap),
// where the system itself refuses the guard.
func forceRefusal(string) {}

// refusalWrap starts a refused run inside a sandbox that allows everything, so sandbox_init refuses
// the guard's profile as it does under an agent's sandboxed shell.
func refusalWrap(string) []string {
	return []string{"/usr/bin/sandbox-exec", "-p", "(version 1)(allow default)"}
}

// refusalWords are what Main's error says of the refusal: the system's, and the outer sandbox named.
func refusalWords(string) []string {
	return []string{"sandbox_init refused the profile", "this process already runs inside a sandbox"}
}

// guardedAddr is the address the guarded run dials on macOS: one in TEST-NET-2 of RFC 5737, which no
// network routes. The sandbox's localhost is every address of this host, so one of its own would pass
// (NoNetwork's doc); the guard refuses this one before a packet is sent.
func guardedAddr(*testing.T) string { return "198.51.100.1:9" }

// guardedHolds checks what the guard holds on macOS beyond the dials: a lookup fails, as the
// resolver's socket and service are denied, so the name is not carried out through mDNSResponder.
func guardedHolds(t *testing.T, on map[string]string) {
	t.Helper()
	if v := on["lookup"]; v == "ok" || !strings.Contains(v, "example.com") {
		t.Errorf("under the guard a lookup of example.com gave %q, want its failure: the resolver carried the name out", v)
	}
}
