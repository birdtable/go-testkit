// Package contract holds testkit's three modules to their promise indexes: its test keeps
// api/contract.txt for the root, and contain/api's and taskfile/api's for those modules. It also holds the README's quick start
// equal to internal/example/quickstart's test file. It has no code: no package imports it, so a
// consumer's go test all never runs its test, which reads this repository's tree and its tags with git.
package contract
