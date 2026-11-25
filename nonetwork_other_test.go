//go:build !darwin && !linux && !windows

package testkit

// forceRefusal does nothing here: the system has no guard, so every run of NoNetwork is refused.
func forceRefusal(string) {}

// refusalWrap is nothing here: the refusal is the system's own.
func refusalWrap(string) []string { return nil }

// refusalWords are what Main's error says of a system with no guard.
func refusalWords(string) []string {
	return []string{"testkit sets no network guard on " + hostOS()}
}
