package fake

import "strings"

// programName is the name a stand-in was placed under, from its file's name: without the .exe that
// Install added, in whatever case the system gives it back.
func programName(file string) string {
	const exe = ".exe"
	if len(file) > len(exe) && strings.EqualFold(file[len(file)-len(exe):], exe) {
		return file[:len(file)-len(exe)]
	}
	return file
}
