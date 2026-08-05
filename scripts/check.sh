#!/usr/bin/env sh
#
# The repository quality gate. Reports, never rewrites (except --format).
#
#   sh scripts/check.sh             gofmt + vet + test + lint
#   sh scripts/check.sh --no-lint   skip golangci-lint
#   sh scripts/check.sh --format    rewrite Go sources in place
#
# tronc is a library: there is no client half, so this is the whole gate.
# Deliberately depends on nothing but a `go`. It is NOT invoked through mise:
# `mise run` resolves every tool in the merged config before running any task
# body, so an unrelated broken tool in the user's global config would take this
# gate down with it.

set -eu

mode="all"
case "${1:-}" in
--no-lint) mode="nolint" ;;
--format) mode="format" ;;
"") ;;
*)
  echo "usage: $0 [--no-lint|--format]" >&2
  exit 2
  ;;
esac

cd "$(git rev-parse --show-toplevel)"

# Resolve the toolchain from GOROOT when it is set. mise exports GOROOT for the
# version this repo pins, but leaves an unrelated `go` earlier on PATH (Homebrew's,
# here), and a go binary driving a different GOROOT fails with
# `compile: version "X" does not match go tool version "Y"`.
if [ -z "${GO:-}" ]; then
  if [ -n "${GOROOT:-}" ] && [ -x "$GOROOT/bin/go" ]; then GO="$GOROOT/bin/go"; else GO=go; fi
fi
if [ -z "${GOFMT:-}" ]; then
  if [ -n "${GOROOT:-}" ] && [ -x "$GOROOT/bin/gofmt" ]; then GOFMT="$GOROOT/bin/gofmt"; else GOFMT=gofmt; fi
fi

if ! command -v "$GO" >/dev/null 2>&1 && [ ! -x "$GO" ]; then
  echo "check: no usable go ('$GO')" >&2
  exit 1
fi

if [ "$mode" = "format" ]; then
  "$GO" fmt ./...
  exit 0
fi

status=0

unformatted="$("$GOFMT" -l . || true)"
if [ -n "$unformatted" ]; then
  echo "gofmt: the following files are not formatted (run 'sh scripts/check.sh --format'):"
  echo "$unformatted"
  status=1
fi

"$GO" vet ./... || status=1
"$GO" test ./... || status=1

if [ "$mode" = "all" ]; then
  if command -v golangci-lint >/dev/null 2>&1; then
    golangci-lint run ./... || status=1
  else
    echo "check: no 'golangci-lint' on PATH, skipping the lint pass" >&2
  fi
fi

if [ "$status" -ne 0 ]; then
  echo "check failed"
fi
exit "$status"
