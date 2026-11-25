//go:build !windows

package testkit

import (
	"os"
	"os/exec" //nolint:depguard // the child Rerun starts, given the start this system's interrupt needs
	"syscall"
)

// ownGroup leaves the child as it is: off Windows the interrupt goes to its process alone.
func ownGroup(*exec.Cmd) {}

// held is a child RerunInterrupted may interrupt. Off Windows os.Process is held already: its Signal
// sends nothing once the child was waited for, so a later process that took its id is never sent it.
type held struct{ p *os.Process }

func hold(p *os.Process) (held, error) { return held{p}, nil }

func (h held) interrupt() error { return h.p.Signal(os.Interrupt) }

// terminate sends the child SIGTERM.
func (h held) terminate() error { return h.p.Signal(syscall.SIGTERM) }

func (held) release() error { return nil }

// killLeavesACode is false off Windows: a kill is SIGKILL, and the ended process's state says so, so
// Running's Wait reads a killed program's -1 from that state, and a program that ended by itself
// before the signal keeps its own code.
const killLeavesACode = false
