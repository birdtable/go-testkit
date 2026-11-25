//go:build !windows && !darwin && !plan9

package testkit

// readsXDG is whether os.UserConfigDir reads XDG_CONFIG_HOME on this system, and so gives a
// process with HOME alone HOME's .config: true on Linux and the BSDs.
const readsXDG = true
