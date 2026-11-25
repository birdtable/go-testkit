package testkit

import "testing"

// TestKeptNameRefusesSuperscriptComAndLptDevices: Windows folds the Latin-1 superscripts ¹ ² ³ to 1 2 3
// when it reads a device name, so COM¹ and LPT³ open the COM1 and LPT3 devices. keptName refuses them,
// with or without an extension and in any case, and keeps a name that only looks like a device. Dropping
// the superscript cases turns the kept rows red.
//
// What it costs when this fails: a test's program is placed under a name Windows opens as a serial or
// printer device, so the build writes to the device or hangs on it instead of landing a file.
func TestKeptNameRefusesSuperscriptComAndLptDevices(t *testing.T) {
	t.Parallel()
	kept := []string{"COM¹", "com²", "COM³", "LPT¹", "lpt²", "LPT³", "lpt³.txt", "COM¹.exe"}
	for _, name := range kept {
		if !keptName(name) {
			t.Errorf("keptName(%q) is false; Windows reads it as a COM or LPT device, so it must be kept", name)
		}
	}
	free := []string{"COM", "COMB", "COM¹²", "LPT", "COM0", "COM¹a", "ACOM¹", "LPT⁴"}
	for _, name := range free {
		if keptName(name) {
			t.Errorf("keptName(%q) is true; it is not a device name", name)
		}
	}
}
