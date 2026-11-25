//go:build !windows

package fake

// programName is the name a stand-in was placed under, from its file's name: the same.
func programName(file string) string { return file }
