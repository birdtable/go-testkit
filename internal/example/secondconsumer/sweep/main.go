// Command sweep is the second consumer's own stand-in: fake's generic steps, and one of
// the consumer's, region, which a stand-in built from testkit's default program does not have.
package main

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/birdtable/go-testkit/fake"
)

func main() {
	fake.Main(map[string]func(arg string, args []string) (code int, end bool){
		// region says the region its step names and the stand-in's arguments, and ends the stand-in
		// with code 41 when the region is not among them, or 125, as fake's own steps do, when it
		// cannot say it.
		"region": func(arg string, args []string) (int, bool) {
			if _, err := fmt.Fprintf(os.Stdout, "region %s: %s\n", arg, strings.Join(args, " ")); err != nil { //nolint:forbidigo // cli.errors.3: a stand-in's stdout is the run's output, which the test reads
				return 125, true
			}
			if !slices.Contains(args, arg) {
				return 41, true
			}
			return 0, false
		},
	})
}
