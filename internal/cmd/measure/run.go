package main

import (
	"fmt"
	"io"
	"os"
)

// run does what the one word of args names in the repository at dir, writes what it has to say to
// out, and gives the exit code: 0 done, 1 refused, 2 not run.
func run(args []string, dir string, out io.Writer) int {
	said, code := do(args, dir)
	if _, err := io.WriteString(out, said); err != nil {
		return 2
	}
	return code
}

// do does what run does, and gives what it has to say.
func do(args []string, dir string) (string, int) {
	if len(args) != 1 || args[0] != "size" {
		return fmt.Sprintf("measure takes one word, size, and was given %q\n", args), 2
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err.Error() + "\n", 2
	}
	said, code := size(root)
	if err := root.Close(); err != nil && code == 0 {
		return said + err.Error() + "\n", 2
	}
	return said, code
}

// size says each module's lines, the root's first and then by folder, and the total, and refuses
// the total above Ceiling.
func size(root *os.Root) (string, int) {
	counts, err := Size(root.FS())
	if err != nil {
		return err.Error() + "\n", 2
	}
	said, n := "", 0
	for _, c := range counts {
		said += fmt.Sprintf("module %s: %d\n", c.Module, c.Lines)
		n += c.Lines
	}
	said += fmt.Sprintf("non-test Go lines outside internal/: %d (the ceiling is %d)\n", n, Ceiling)
	if n > Ceiling {
		return said + fmt.Sprintf("%d lines is %d above the ceiling\n", n, n-Ceiling), 1
	}
	return said, 0
}
