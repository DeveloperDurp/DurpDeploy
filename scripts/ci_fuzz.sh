#!/usr/bin/env bash
# Explore only offline targets. Ordinary Go tests replay their seeds on MRs.
set -euo pipefail
umask 077
cd "$(dirname "${BASH_SOURCE[0]}")/.."
[[ $# == 0 ]] || { echo 'usage: ci_fuzz.sh' >&2; exit 2; }
evidence=artifacts/fuzz
mkdir -p "$evidence"
private=$(mktemp -d)
trap 'rm -rf -- "$private"' EXIT
targets=(
  'internal/handler FuzzReadJSON'
  'internal/artifact FuzzValidateZIPPaths'
  'internal/logscrub FuzzScrubberStreaming'
  'internal/logscrub FuzzDefaultPatternStreaming'
  'internal/artifact FuzzReviewJSONSyntax'
)
result=0
for entry in "${targets[@]}"; do
  read -r package target <<< "$entry"
  command=(bash scripts/go_test.sh -run '^$' -fuzz "^$target$"
    -fuzztime=30s -fuzzminimizetime=5s -parallel=2 -timeout=2m "./$package")
  printf 'Reproduce: ' | tee "$evidence/$target.log"
  printf '%q ' "${command[@]}" | tee -a "$evidence/$target.log"
  printf '\n' | tee -a "$evidence/$target.log"
  status=0
  # Do not inherit application secrets, runtime endpoints, or custom patterns.
  # The wrapper deadline also covers cold compilation and cleanup.
  timeout --signal=TERM --kill-after=10s 6m env -i \
    HOME="$HOME" PATH="$PATH" \
    GOTOOLCHAIN="${GOTOOLCHAIN:-go1.26.10}" GOFLAGS=-mod=readonly \
    GOPATH="$(go env GOPATH)" GOMODCACHE="$(go env GOMODCACHE)" \
    GOCACHE="$(go env GOCACHE)" GOMAXPROCS=2 GOMEMLIMIT=1GiB \
    "${command[@]}" >"$private/$target.log" 2>&1 || status=$?
  # Native failure output can echo inputs. Retain only safe progress and IDs.
  sed -n '/^fuzz: elapsed:/p; /^--- FAIL: Fuzz/p; /^FAIL$/p' \
    "$private/$target.log" | head -c 8192 | tee -a "$evidence/$target.log"
  printf 'Exit: %s\n' "$status" | tee -a "$evidence/$target.log"
  if ((status)); then
    result=1
    if [[ -d $package/testdata/fuzz/$target ]]; then
      mkdir -p "$evidence/corpus/$target"
      # Only synthetic fuzz bytes are present. Retain exact inputs for replay.
      cp -R "$package/testdata/fuzz/$target/." "$evidence/corpus/$target/"
    fi
  fi
done
exit "$result"
