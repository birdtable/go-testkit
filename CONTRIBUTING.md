# Contributing

Issues and pull requests are welcome. For a bug, the Bug form asks for the testkit version, the Go
version, the system and the shortest test that shows it. For a feature, say what your tests need, not
only the API you want; a need can often be met another way. Security problems go to
[SECURITY.md](SECURITY.md), never to an issue.

## Running the tests

The tools are pinned in `tools/` and built by `go tool` on first use, so nothing is installed on your
machine. [Task](https://taskfile.dev) runs them:

```bash
git clone https://github.com/birdtable/go-testkit && cd go-testkit
go tool -modfile=tools/task.mod task hooks      # once per clone: git runs hooks/pre-push
go tool -modfile=tools/task.mod task pre-push   # what a push runs: lint, the tests with -race -short, the API gate
go tool -modfile=tools/task.mod task release    # what a release runs: the whole suite, shuffled
```

Each tier runs its members side by side and holds them to the time budget in `budgets.yaml`: the
push tier by the CPU time its members take, the commit and release tiers by elapsed time.
`Taskfile.yml` says what each member runs. Its commands call Go, and `task taskfile-check` holds them
to that. The few that stay in shell are listed in `taskfile-exceptions.txt` with their reasons.

The root module imports the standard library only, and `task graph` holds it there. `contain` and
`taskfile` are modules of their own, with tags `contain/v*` and `taskfile/v*`.

## The API and its promises

- `task api`, `task contain-api` and `task taskfile-api` run the API gate on each module, from one
  host for all three systems. The gate checks that the systems differ only as `api/systems.txt` says,
  that each breaking change since the last tag is listed in `api/breaks/<version>.txt`, and that
  `api/marks.txt` names only documented, non-experimental names.
- `api/contract.txt` is the promise index: one line per promise, naming the test that holds it.
  `internal/contract` checks it in every tier.

## Commits and pull requests

- One change per pull request, with a body that says what changes and why. The template asks about
  each exported constant or default the change adds.
- Behaviour changes come with a test that fails without them, and a change note under
  `.changes/unreleased` (`go tool -modfile=tools/changie.mod changie new`).
- Keep commits small, with a subject line that says what the commit does. A fix to a pushed commit
  is a new commit.
- `task pre-push` passes before you push.

By contributing, you agree that your contribution is licensed under [Apache-2.0](LICENSE).
