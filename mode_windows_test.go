package testkit

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// namesFoldHere is true on Windows, where a go reads goenv=off as GOENV=off. The tests hold Isolate to
// it apart from foldNames, so a foldNames that is wrong for the system is red.
const namesFoldHere = true

// killIsSignal is false on Windows, where a process's Kill ends it with an exit code and no signal.
const killIsSignal = false

// exeHere is the end of a program's name on Windows, which runs a program only as a .exe. The tests
// hold Install to it apart from exeSuffix, so an exeSuffix that is wrong for the system is red.
const exeHere = ".exe"

// keptNames are file names Windows keeps for a device, with and without an extension, which a test's
// program must not be placed under.
var keptNames = []string{"NUL", "con", "COM1", "nul.txt"} //nolint:gochecknoglobals // the system's answer, read by the Install table

// junctions is true on Windows, where a directory junction is a link a test can plant.
const junctions = true

// junction plants a directory junction at link naming the folder target, by cmd's mklink /J, which
// needs no privilege, so a junction row runs where Developer Mode is off too.
func junction(ctx context.Context, target, link string) error {
	cmd := filepath.Join(os.Getenv("SYSTEMROOT"), "System32", "cmd.exe")
	out, err := exec.CommandContext(ctx, cmd, "/c", "mklink", "/J", link, target).CombinedOutput() //nolint:gosec // the system's cmd, with the test's own folders
	if err != nil {
		return fmt.Errorf("mklink /J %s %s: %w: %s", link, target, err, out)
	}
	return nil
}

// holdIsAPipe is true on Windows, where HoldRead's hold is a named pipe, with no name after release.
const holdIsAPipe = true
