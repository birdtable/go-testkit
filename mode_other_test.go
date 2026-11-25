//go:build !windows

package testkit

import (
	"context"
	"errors"
)

// namesFoldHere is false off Windows, where goenv is a variable go does not read. The tests hold
// Isolate to it apart from foldNames, so a foldNames that is wrong for the system is red.
const namesFoldHere = false

// killIsSignal is true off Windows, where a process's Kill is SIGKILL and its exit says so.
const killIsSignal = true

// exeHere is the end of a program's name off Windows: none. The tests hold Install to it apart from
// exeSuffix, so an exeSuffix that is wrong for the system is red.
const exeHere = ""

// keptNames are the file names off Windows that the system keeps for a device: none.
var keptNames = []string{} //nolint:gochecknoglobals // the system's answer, read by the Install table

// junctions is false off Windows, the one system with a directory junction, so no junction row is
// in a table here, since a tier fails on a skip.
const junctions = false

// junction plants nothing off Windows.
func junction(context.Context, string, string) error { return errors.ErrUnsupported }

// holdIsAPipe is false off Windows, where HoldRead's hold is a FIFO, an empty file after release.
const holdIsAPipe = false
