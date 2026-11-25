//go:build !windows

package testkit

// foldNames is false off Windows, where a variable's name is read exactly, so goenv is not GOENV.
const foldNames = false
