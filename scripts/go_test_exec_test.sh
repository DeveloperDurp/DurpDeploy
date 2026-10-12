#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
fixture=$(mktemp -d "$root/.go-test-sessions.XXXXXXXX")
trap 'rm -rf -- "$fixture"' EXIT
mkdir -p "$fixture/first" "$fixture/second" "$fixture/results"

# Given: two real package binaries record their cleanup identities.
cat > "$fixture/first/session_test.go" <<'GO'
package fixture

import (
    "os"
    "path/filepath"
    "testing"
)

func TestSession(t *testing.T) {
    directory, err := os.Getwd()
    if err != nil { t.Fatal(err) }
    result := filepath.Join(os.Getenv("SESSION_RESULTS"), filepath.Base(directory))
    session := os.Getenv("TESTCONTAINERS_SESSION_ID")
    owned := os.Getenv("DURPDEPLOY_GO_TEST_DIRECTORY")
    if filepath.Base(owned) != session { t.Fatal("unowned session") }
    if err := os.WriteFile(result, []byte(owned), 0600); err != nil { t.Fatal(err) }
    if os.Getenv("SESSION_FAILURE") == "true" { t.Fatal("fixture failure") }
}
GO
cp "$fixture/first/session_test.go" "$fixture/second/session_test.go"
cd "$root"
for failure in false true; do
    status=0
    # When: Go runs both packages concurrently through the real runner.
    SESSION_RESULTS="$fixture/results" SESSION_FAILURE="$failure" \
        bash scripts/go_test.sh -count=1 -p=2 \
        "./${fixture##*/}/first" "./${fixture##*/}/second" \
        > "$fixture/output" 2>&1 || status=$?
    expected=0
    if [[ $failure == true ]]; then expected=1; fi
    if [[ $status != "$expected" ]]; then
        cat "$fixture/output" >&2
        exit 1
    fi
    first=$(cat "$fixture/results/first")
    second=$(cat "$fixture/results/second")
    # Then: package reapers cannot share an identity, and cleanup removes both.
    if [[ $first == "$second" ]]; then
        echo 'FAIL: package binaries share a cleanup session' >&2
        exit 1
    fi
    [[ ! -e $first && ! -e $second ]]
done
echo 'Go test package session isolation: PASS'
