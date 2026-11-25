//go:build windows || darwin || plan9

package testkit

// readsXDG is false on Windows, macOS, iOS (which builds as darwin too) and Plan 9, whose
// configuration folder follows HOME or APPDATA alone, never XDG_CONFIG_HOME.
const readsXDG = false
