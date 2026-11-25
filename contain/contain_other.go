//go:build !linux && !windows

package contain

// rerunContained is a system with no containment contain makes: the tests run here, and Contained skips.
func rerunContained([]string) (code int, refused string, err error) { return 0, NoContainment, nil }

// supervise is never called on this system: no run is started as the init of a namespace.
func supervise(bool) (int, error) { return 1, nil }

// entered is false: this system makes no containment, so no run is in one, whatever its variable says.
func entered(string) bool { return false }

// keepMounts is never reached on this system, which makes no containment: it keeps nothing.
func keepMounts() (string, error) { return NoContainment, nil }

// mountsHere is false: this system has no namespace whose mounts a test could be given.
const mountsHere = false

// strayEnds is false: a run whose variable is no containment's word is a run with none.
const strayEnds = false
