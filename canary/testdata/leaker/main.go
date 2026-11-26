// Command leaker is the program canary's acceptance test runs: it is handed a token by its
// environment and writes a log line on stderr, a settings file in its home and a SQLite file there,
// copied from a fixture. Given leak, the token is in the log line and the settings file, and the
// fixture copied is the one whose rows hold planted values; given clean, it writes the same places
// with none of them.
//
//	leaker leak|clean <home> <fixtures>
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func main() {
	if len(os.Args) != 4 || (os.Args[1] != "leak" && os.Args[1] != "clean") {
		fmt.Fprintln(os.Stderr, "usage: leaker leak|clean <home> <fixtures>")
		os.Exit(2)
	}
	if err := run(os.Args[1] == "leak", os.Args[2], os.Args[3], os.Getenv("LEAKER_TOKEN")); err != nil {
		fmt.Fprintln(os.Stderr, "leaker:", err)
		os.Exit(1)
	}
}

func run(leak bool, home, fixtures, token string) error {
	shown, fixture := "", filepath.Join(fixtures, "clean")
	if leak {
		shown, fixture = token, filepath.Join(fixtures, "leak")
	}
	fmt.Fprintf(os.Stderr, "leaker: signed in, token %q\n", shown)
	if err := os.WriteFile(filepath.Join(home, "settings.json"), []byte(fmt.Sprintf("{\"token\": %q}\n", shown)), 0o600); err != nil {
		return err
	}
	data := filepath.Join(home, "data")
	if err := os.MkdirAll(data, 0o700); err != nil {
		return err
	}
	for _, name := range []string{"app.db", "app.db-wal"} {
		if err := copyFile(filepath.Join(fixture, name), filepath.Join(data, name)); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
