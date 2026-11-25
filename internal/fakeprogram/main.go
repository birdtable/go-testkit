// Command fakeprogram is the default stand-in of testkit's fake package: fake.Program builds it and
// places it under the name a test gave, and it does the steps written beside it. It has no steps of
// its own; a product's stand-in is its own main package, which calls fake.Main with its handlers.
package main

import "github.com/birdtable/go-testkit/fake"

func main() { fake.Main(nil) }
