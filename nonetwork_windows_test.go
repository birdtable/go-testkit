//go:build windows

package testkit

// forceRefusal does nothing here: Windows has no guard, so every run of NoNetwork is refused.
func forceRefusal(string) {}

// refusalWrap is nothing here: the refusal is the system's own.
func refusalWrap(string) []string { return nil }

// refusalWords are what Main's error says on Windows.
func refusalWords(string) []string {
	return []string{"Windows gives an unprivileged process no way to refuse outbound connections and keep loopback"}
}
