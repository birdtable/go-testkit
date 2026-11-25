package testkit

import "testing"

// reportOpen reads nothing on Windows, where the system itself refuses the removal of a file or folder
// a handle holds: the folder's removal is the check there, and NoOpenFiles adds none.
func reportOpen(testing.TB, string) {}
