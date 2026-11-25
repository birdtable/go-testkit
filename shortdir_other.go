//go:build !windows

package testkit

// shortRoot is ShortDir's root off Windows: /tmp, which every Unix has and whose sticky bit keeps
// another account from removing or renaming a folder in it. TMPDIR is not read: on macOS it is long.
func shortRoot() (string, error) { return "/tmp", nil }

// shortDirRemedy is the fix ShortDir's refusal names off Windows, where the root is /tmp and a
// refusal means a caller passed shortDir its own long root; there is no 8.3 name, so shortErr is nil.
func shortDirRemedy(error) string { return "pass a shorter root under /tmp" }
