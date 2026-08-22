# tronc — Development

Local setup, the quality gate, CI, and how versions are cut and consumed.

## Prerequisites

- **Go 1.24.** `go.mod` declares `go 1.24.0` and `mise.toml` pins the toolchain to `1.24`.
  Consumers run newer — Jardin is on 1.26.1, Plume and Casier on 1.25.0 — but 1.24.0 is the
  floor across the suite and the version CI builds with.
- **mise**, for the task runner. Optional; every task is a one-line shell command you can run
  directly.
- **golangci-lint v2**, optional locally. CI pins `v2.12.2`. The local gate skips the lint pass
  when the binary is missing or unusable and says so.

There is no database, no Docker, and no client half. `tronc` is a library with one dependency,
so `git clone` and `go test ./...` is the whole setup.

## Tasks

```sh
mise run check      # gofmt + vet + test + golangci-lint
mise run test       # go test ./...
mise run format     # rewrite Go sources in place
```

## Git hooks

`mise install` wires the git hooks through lefthook, so a fresh clone needs nothing beyond
the command it already runs to get the toolchain.

Two hooks land. `commit-msg` enforces Conventional Commits and comes from the shared config
in `FacileStudio/hooks`, pinned by tag in `lefthook.yml`, so the rule is identical across the
suite and changes in one place. `pre-push` runs `scripts/check.sh`, the same gate as before
and with the script unchanged.

## The quality gate

`scripts/check.sh` is the gate. It reports and never rewrites, except under `--format`.

```sh
sh scripts/check.sh             # gofmt -l + go vet + go test + golangci-lint
sh scripts/check.sh --no-lint   # skip the lint pass
sh scripts/check.sh --format    # go fmt ./... and exit
```

Every stage runs even after an earlier one fails, so one invocation reports everything rather
than one thing at a time. The script exits non-zero with `check failed` at the end.

Two deliberate quirks worth knowing before you "simplify" it:

- **It is not invoked through mise.** `mise run` resolves every tool in the merged config
  before running any task body, so one broken tool anywhere in your *global* mise config would
  take the gate down with it. The lefthook `pre-push` job therefore calls `scripts/check.sh`
  directly.
- **It resolves the toolchain from `GOROOT`.** mise exports `GOROOT` for the pinned version but
  leaves an unrelated `go` earlier on `PATH` — Homebrew's, typically — and a `go` binary
  driving a different `GOROOT` fails with `compile: version "X" does not match go tool version
  "Y"`. Override with `GO` and `GOFMT` if you need something else.
- **`golangci-lint` is probed by running it, not by `command -v`.** A mise shim for an
  uninstalled version resolves on `PATH` but fails on every invocation, so the script runs
  `golangci-lint version` and skips the pass when that fails rather than failing the gate.

Bypass the hook once with `git push --no-verify`.

## Linters

`.golangci.yml` runs with `default: none` and an explicit enable list: `errcheck`, `govet`,
`ineffassign`, `staticcheck` (all checks), `unused`, `bodyclose`, `errorlint`, `misspell`,
`nilerr`, `unconvert`, plus the `gofmt` formatter. `errcheck` also checks type assertions.
Test files are excluded from `errcheck` and `bodyclose`. Issue caps are lifted
(`max-issues-per-linter: 0`, `max-same-issues: 0`), so the run shows everything.

## CI

`.github/workflows/ci.yml` runs on pushes to `main` and on every pull request. This is the one
licensed exception to the suite's no-Actions rule: `tronc` is public and shared, so its gate
runs where contributors can see it.

The job is `gofmt -l` (piped so any output fails the step), `go vet ./...`,
`go test -race -coverprofile=coverage.out ./...`, then `golangci-lint-action@v7` at `v2.12.2`.
Note that CI runs the tests with `-race` while the local gate does not — a data race can pass
locally and fail in CI.

## Tests

Every package carries a `_test.go` beside it. They are the executable version of this
documentation: `spa/spa_test.go` asserts the 405 on a write with `Allow: GET, HEAD` and names
the log shipper in the failure message, `env/env_test.go` asserts that an unset origin list
allows nobody and that each of the six fallback names is read in order, and
`middleware/middleware_test.go` covers redaction and the wildcard-plus-credentials panic.
Read them before changing a behavior; several encode a specific past outage.

## Versioning

Semver tags, never branch tracking. While on `v0`, a breaking change bumps the **minor**.

```
require github.com/FacileStudio/tronc v0.8.0
```

Every change is recorded in [CHANGELOG.md](../CHANGELOG.md) in Keep a Changelog format, with
the reason it exists — usually the specific app and the specific failure that prompted it.
Add an `Unreleased` entry as part of the change, not after.

## Adding a package

The bar is that a behavior is already duplicated across consumers and has drifted, or that a
consumer cannot adopt the chassis without it. `spa` was extracted from Courrier's
`internal/spa`, `httpx.Chain` exists because Jardin does not use chi, and `LoadCoreWithout`
exists because Jardin has no database. Nothing here was designed speculatively.

The hard constraint: **no new dependencies.** `github.com/go-chi/chi/v5` is the only one, and
that is a feature of the module, not an accident. Anything that would need a second one belongs
in the consumer.
