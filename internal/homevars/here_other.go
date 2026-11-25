//go:build !windows

package homevars

// Here is the system this build runs on, as Folders and Halves take it: on every system but
// windows "other", which reads the list each system but windows reads. Each system's file says its
// own, as a platform check belongs in a file of its system (arch.ports.1).
const Here = "other"
