#!/usr/bin/env bash
# Builds, vets, format-checks, and race-tests every module in go.work, tests the Pi bridge's
# TypeScript (pi/extension) with node, and runs lint-context and lint-references. Every step runs,
# and the check exits nonzero if any failed. Reads no network but the Go module proxy on a cold
# cache, and writes nothing.
set -uo pipefail
cd "$(dirname "$0")/.." || exit

check_gofmt() {
  local unformatted
  unformatted=$(gofmt -l .) || return
  if [[ -n $unformatted ]]; then
    echo "gofmt: unformatted files:"
    echo "$unformatted"
    return 1
  fi
}

check_go_modules() {
  local mods mod status=0
  mods=$(go work edit -json | jq -r '.Use[].DiskPath') || return
  for mod in $mods; do
    echo "check: $mod"
    (cd "$mod" && go build ./... && go vet ./... && check_gofmt && go test -race ./...) || status=1
  done
  return "$status"
}

test_pi_bridge() {
  echo "check: pi/extension"
  node --test pi/extension/*.test.ts
}

run_lint() {
  if ! command -v "$1" >/dev/null; then
    echo "FAIL: $1 is not on PATH; run mise install" >&2
    return 1
  fi
  "$1"
}

fail=0
check_go_modules || fail=1
test_pi_bridge || fail=1
run_lint lint-context || fail=1
run_lint lint-references || fail=1
exit "$fail"
