#!/bin/bash
# Run forgectl's checks and test suite.
#
# With no arguments it runs the full local gate, in order:
#   1. gofmt check  — fail if any Go file is not gofmt-clean (nothing is rewritten).
#   2. go vet       — report suspicious constructs.
#   3. go test      — the whole module with the race detector and coverage.
#
# Any arguments are passed straight through to `go test` instead, so you can
# narrow a run during development:
#   bash test.sh ./pkg/cli
#   bash test.sh -run TestReleaseList ./pkg/cli
#   bash test.sh -v ./...
#
# Usage: bash test.sh [go test arguments...]

# pipefail makes a pipeline fail if any stage fails; -e exits on the first
# unhandled error.
set -eo pipefail

# Change to the script's directory (the repo root) so the script can be run
# from any location.
cd "$(dirname "${BASH_SOURCE[0]}")"

# With arguments, hand full control to `go test`: the caller chooses the flags
# and the target, and the gofmt/vet pre-checks are skipped.
if [ "$#" -gt 0 ]; then
    exec go test "$@"
fi

echo "gofmt: checking formatting ..."
unformatted=$(gofmt -l .)
if [ -n "${unformatted}" ]; then
    echo "Error: the following files are not gofmt-clean:" >&2
    echo "${unformatted}" >&2
    echo "Run: gofmt -w ${unformatted}" >&2
    exit 1
fi

echo "go vet: examining packages ..."
go vet ./...

echo "go test: running the suite (race + coverage) ..."
go test -race -cover ./...

echo
echo "All checks passed."
