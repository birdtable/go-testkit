//go:build !windows

package testkit

// keptName is false off Windows, where no file name is kept for a device.
func keptName(string) bool { return false }
