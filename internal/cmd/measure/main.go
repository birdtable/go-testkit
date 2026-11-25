// Command measure prints the size of testkit's code and fails above its ceiling, as go-proc's
// internal/cmd/measure does for go-proc. The Taskfile runs it from the
// repository's top, and it starts no program:
//
//	measure size  prints the non-test Go lines of each module outside internal/ and their total,
//	              and fails with the total above Ceiling
package main

import "os"

func main() { os.Exit(run(os.Args[1:], ".", os.Stdout)) }
