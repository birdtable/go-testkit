// Command custom is a stand-in with two steps of its own, for the rows of testkit's fake package: what
// a product's own main package looks like when it adds steps to the generic ones.
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/birdtable/go-testkit/fake"
)

func main() {
	fake.Main(map[string]func(arg string, args []string) (code int, end bool){
		// shout says its argument in capitals, then the stand-in's arguments, and goes on.
		"shout": func(arg string, args []string) (int, bool) {
			fmt.Fprintln(os.Stdout, strings.ToUpper(arg), strings.Join(args, ","))
			return 0, false
		},
		// refuse ends the stand-in with code 7 when its argument is among the stand-in's.
		"refuse": func(arg string, args []string) (int, bool) {
			for _, a := range args {
				if a == arg {
					return 7, true
				}
			}
			return 0, false
		},
	})
}
