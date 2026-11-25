package testkit

import "strings"

// keptName reports whether Windows keeps name for a device, so opening name would reach the device
// and not a file of the folder. Windows reads the name up to its first '.', without
// its trailing spaces, so "nul.txt" and "COM1.log" name the device as "NUL" and "COM1" do;
// filepath.IsLocal catches the bare names but not these. The set is CON, PRN, AUX, NUL, CONIN$,
// CONOUT$, and COM1..9 and LPT1..9, read without case.
func keptName(name string) bool {
	base, _, _ := strings.Cut(name, ".")
	base = strings.ToUpper(strings.TrimRight(base, " "))
	switch base {
	case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$":
		return true
	}
	// Windows folds the Latin-1 superscripts ¹ ² ³ (U+00B9, U+00B2, U+00B3) to 1 2 3 when it reads a
	// device name, so COM¹ and LPT³ reach the COM1 and LPT3 devices as surely as COM1 and LPT3 do.
	tail, ok := strings.CutPrefix(base, "COM")
	if !ok {
		tail, ok = strings.CutPrefix(base, "LPT")
	}
	if ok {
		switch tail {
		case "1", "2", "3", "4", "5", "6", "7", "8", "9", "¹", "²", "³":
			return true
		}
	}
	return false
}
