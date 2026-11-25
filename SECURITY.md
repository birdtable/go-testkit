# Security

## Reporting a vulnerability

Please report a vulnerability privately, through GitHub's private vulnerability reporting:
[open a report](https://github.com/birdtable/go-testkit/security/advisories/new). Never open a public
issue for one.

Say what testkit should hold and does not, which package and call, and what a test or its child could
then reach: for example, a token or a person's setting that reaches a test, or a contained test that
reaches a process outside its containment. Give the version (`go list -m github.com/birdtable/go-testkit`),
the Go version and the system.

## Supported versions

The latest minor version of each module (`go-testkit`, `go-testkit/contain` and `go-testkit/taskfile`)
gets fixes. testkit is at v0, so a fix ships in a new release, not a backport.
