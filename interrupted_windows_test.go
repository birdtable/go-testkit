package testkit

// statusControlCExit is the code Windows gives a process that a console control event ended:
// STATUS_CONTROL_C_EXIT, 0xC000013A.
const statusControlCExit = 0xC000013A

// endedTheSystemsWay reports whether a child of RerunInterrupted with code and err was ended by the
// interrupt it did not catch: on Windows a CTRL_BREAK_EVENT no handler takes ends the process with
// STATUS_CONTROL_C_EXIT, and no error, as Signal reads no signal there.
func endedTheSystemsWay(code int, err error) bool {
	return err == nil && code == statusControlCExit
}
