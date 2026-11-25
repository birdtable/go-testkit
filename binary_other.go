//go:build !windows

package testkit

// exeSuffix is empty off Windows, where a built program's name needs no suffix.
const exeSuffix = ""
