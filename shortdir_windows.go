package testkit

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// shortRoot is ShortDir's root on Windows: os.TempDir, the account's own temporary folder, which
// another account cannot open. It picks the root through pickRoot and returns its
// choice and the short call's error, if any, for the refusal to name.
func shortRoot() (string, error) { return pickRoot(os.TempDir()) }

// pickRoot chooses ShortDir's root from root: root itself where it fits, its 8.3 short form where it
// is too deep and the volume keeps one shorter, and otherwise root with the short call's own error
// (none came back, or an answer that was no shorter and so no error). It is split from os.TempDir so
// a test can hand it a deep root and read the error. testkit imports only the standard library, so
// the short name comes from syscall, not golang.org/x/sys/windows.
func pickRoot(root string) (string, error) {
	if len(filepath.Clean(root)) <= maxRootLen {
		return root, nil
	}
	short, err := shortName(root)
	if err != nil {
		return root, err
	}
	if len(filepath.Clean(short)) < len(filepath.Clean(root)) {
		return short, nil
	}
	return root, nil
}

// shortName gives p's 8.3 short form through syscall.GetShortPathName. It reads the name from disk,
// so p must be there; where the volume keeps no 8.3 name it gives p back, with no error. A second
// read that answers a size the buffer cannot hold means the name grew between the two reads, which is
// a failed read, not an empty answer taken as shorter.
func shortName(p string) (string, error) {
	u16, err := syscall.UTF16PtrFromString(p)
	if err != nil {
		return "", err
	}
	n, err := syscall.GetShortPathName(u16, nil, 0)
	if err != nil {
		return "", err
	}
	buf := make([]uint16, n)
	got, err := syscall.GetShortPathName(u16, &buf[0], n)
	if err != nil {
		return "", err
	}
	if got >= n {
		return "", fmt.Errorf("the 8.3 name of %q grew to %d from %d between the two reads", p, got, n)
	}
	return syscall.UTF16ToString(buf), nil
}

// shortDirRemedy is the fix ShortDir's refusal names on Windows, where os.TempDir is too deep: the
// cause (the short call's error, or that no 8.3 name is shorter) and then to point TMP at a short
// folder under the profile, which inherits the account's own access and is never a drive root a
// folder under C:\ would let other accounts write.
func shortDirRemedy(shortErr error) string {
	cause := "its 8.3 short name, where the volume keeps one, is no shorter"
	if shortErr != nil {
		cause = fmt.Sprintf("its 8.3 short name could not be read (%v)", shortErr)
	}
	return cause + `; set TMP to a short folder under your profile, such as %USERPROFILE%\t, and make it`
}
