#!/usr/bin/env bash
set -euo pipefail
umask 077
root=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
fixture=$(mktemp -d)
trap 'rm -rf "$fixture"' EXIT
export SECURITY_TOOL_DIR=${SECURITY_TOOL_DIR:-$root/bin/security}
export SECURITY_REPORT_DIR="$fixture/reports"
export GOWORK=off
export GOFLAGS=-mod=readonly
runner="$root/scripts/security_scan.sh"
mkdir -p "$fixture/app" "$fixture/db/index" "$fixture/db/ID"
printf 'module security-fixture\n\ngo 1.26.0\n' >"$fixture/app/go.mod"
printf '\nrequire github.com/google/uuid v1.6.0\n' >>"$fixture/app/go.mod"
grep -E '^github.com/google/uuid v1.6.0' "$root/go.sum" >"$fixture/app/go.sum"

expect_exit() {
  local expected=$1 actual=0
  shift
  "$@" >"$fixture/output" 2>&1 || actual=$?
  [[ "$actual" == "$expected" || ( "$expected" == nonzero && "$actual" != 0 ) ]] || {
    printf 'FAIL: expected exit %s, got %s\n' "$expected" "$actual" >&2
    cat "$fixture/output" >&2
    exit 1
  }
}

# Given a local database and a call into an existing pinned dependency.
cat >"$fixture/db/index/db.json" <<'JSON'
{"modified":"2026-10-01T00:00:00Z"}
JSON
cat >"$fixture/db/index/modules.json" <<'JSON'
[{"path":"github.com/google/uuid","vulns":[{"id":"GO-2099-0001","modified":"2026-10-01T00:00:00Z","fixed":"99.0.0"}]}]
JSON
cat >"$fixture/db/ID/GO-2099-0001.json" <<'JSON'
{"schema_version":"1.3.1","id":"GO-2099-0001","modified":"2026-10-01T00:00:00Z","published":"2026-10-01T00:00:00Z","details":"Inert gate detection fixture, not a real vulnerability.","database_specific":{"url":"https://example.invalid/security-gate-fixture"},"affected":[{"package":{"name":"github.com/google/uuid","ecosystem":"Go"},"ranges":[{"type":"SEMVER","events":[{"introduced":"0"},{"fixed":"99.0.0"}]}],"ecosystem_specific":{"imports":[{"path":"github.com/google/uuid","symbols":["New"]}]}}]}
JSON
cat >"$fixture/app/main.go" <<'GO'
package main
import ("fmt"; "github.com/google/uuid")
func main() { fmt.Println(uuid.New()) }
GO
# When / Then: reachable finding fails through the actual gate, with a trace.
expect_exit 3 env SECURITY_VULN_DB="file://$fixture/db" bash "$runner" govulncheck "$fixture/app"
grep -Fq 'GO-2099-0001' "$fixture/reports/govulncheck.txt"
grep -Fq 'uuid.New' "$fixture/reports/govulncheck.txt"

# Given the same advisory but no call to the vulnerable symbol.
printf 'package main\nfunc main() {}\n' >"$fixture/app/main.go"
# When / Then: unreachable advisory does not fail the symbol gate.
expect_exit 0 env SECURITY_VULN_DB="file://$fixture/db" bash "$runner" govulncheck "$fixture/app"

# Given an inaccessible database. When / Then: fail rather than claim clean.
expect_exit 2 env SECURITY_VULN_DB="file://$fixture/missing" bash "$runner" govulncheck "$fixture/app"
jq -e '.status == "incomplete"' "$fixture/reports/govulncheck.json" >/dev/null

# Given an unsafe HTTP server fixture; it is analyzed, never executed.
cat >"$fixture/app/main.go" <<'GO'
package main
import "net/http"
func main() { http.ListenAndServe(":0", nil) }
GO
# When / Then: gosec findings fail the gate and source snippets are omitted.
expect_exit 1 bash "$runner" gosec "$fixture/app"
jq -e '.blocking > 0 and all(.issues[]; has("code") | not)' "$fixture/reports/gosec.json" >/dev/null

# Given an invalid Go package. When / Then: incomplete analysis fails closed.
printf 'package main\nfunc broken(\n' >"$fixture/app/main.go"
expect_exit 2 bash "$runner" gosec "$fixture/app"
jq -e '.status != "complete"' "$fixture/reports/gosec.json" >/dev/null

# Given malformed/empty reports. When / Then: the publishing parser rejects them.
for report in '' '   ' '{}' '{"Stats":{"files":0},"Issues":[],"Golang errors":{}}' \
  '{"Stats":{"files":1},"Issues":[],"Golang errors":{}} {}' \
  '{"Stats":{"files":0.5},"Issues":[],"Golang errors":{}}' \
  '{"Stats":{"files":1},"Issues":[{"rule_id":"G1","file":" ","line":"1","severity":"HIGH","confidence":"HIGH"}],"Golang errors":{}}'; do
  printf '%s\n' "$report" >"$fixture/report.json"
  expect_exit nonzero jq -se --arg root "$fixture/app" -f "$root/.security/gosec-report.jq" "$fixture/report.json"
done
printf '{' >"$fixture/report.json"
expect_exit nonzero jq -se --arg root "$fixture/app" -f "$root/.security/gosec-report.jq" "$fixture/report.json"
for report in '' '   ' '{}' '[] []' '[{}]' \
  '[{"RuleID":" ","File":"a.go","StartLine":1,"Commit":""}]' \
  '[{"RuleID":"key","File":" ","StartLine":1,"Commit":""}]' \
  '[{"RuleID":"key","File":"a.go","StartLine":1.5,"Commit":""}]'; do
  printf '%s' "$report" >"$fixture/report.json"
  expect_exit nonzero jq -se -f "$root/.security/gitleaks-report.jq" "$fixture/report.json"
done

# Given a synthetic credential generated only in temporary files.
canary="ghp_$(openssl rand -hex 20)"
printf 'api_key = "%s"\n' "$canary" >"$fixture/app/canary.txt"
# When / Then: actual Gitleaks blocks it and the published evidence has no value.
expect_exit 1 bash "$runner" gitleaks "$fixture/app"
jq -e '.blocking > 0' "$fixture/reports/gitleaks.json" >/dev/null
if grep -FRq -- "$canary" "$fixture/reports" "$fixture/output"; then
  printf 'FAIL: evidence exposed synthetic credential\n' >&2
  exit 1
fi

# Given a different credential in an allowlisted test path.
mkdir -p "$fixture/app/internal/runner"
mv "$fixture/app/canary.txt" "$fixture/app/internal/runner/scrubber_test.go"
# When / Then: path alone does not grant an exception.
expect_exit 1 bash "$runner" gitleaks "$fixture/app"

# Given a credential removed from the current tree but retained in PR history.
git clone -q --no-checkout "$root" "$fixture/history"
git -C "$fixture/history" read-tree --empty
# Explicit paths keep worktree-aware hooks working in reference transactions.
fixture_commit() {
  local message=$1
  GIT_DIR="$fixture/history/.git" GIT_WORK_TREE="$fixture/history" \
    git -c user.name=Fixture -c user.email=fixture@example.invalid commit -qm "$message"
}
printf 'safe\n' >"$fixture/history/config.txt"
git -C "$fixture/history" add config.txt
fixture_commit 'safe baseline'
base=$(git -C "$fixture/history" rev-parse HEAD)
printf 'api_key = "%s"\n' "$canary" >"$fixture/history/config.txt"
git -C "$fixture/history" add config.txt
fixture_commit 'synthetic credential'
printf 'safe\n' >"$fixture/history/config.txt"
git -C "$fixture/history" add config.txt
fixture_commit 'remove synthetic credential'
head=$(git -C "$fixture/history" rev-parse HEAD)
# When / Then: both the PR range and full-history modes detect the removal.
expect_exit 1 env SECURITY_GIT_RANGE="$base..$head" bash "$runner" gitleaks "$fixture/history"
expect_exit 1 bash "$runner" gitleaks "$fixture/history"
if grep -FRq -- "$canary" "$fixture/reports" "$fixture/output"; then
  printf 'FAIL: history evidence exposed synthetic credential\n' >&2
  exit 1
fi
expect_exit 2 env SECURITY_GIT_RANGE=invalid bash "$runner" gitleaks "$fixture/history"

# Given a workflow that weakens permissions or disables findings.
for mutation in 's/contents: read/contents: write/' 's/run: make security-scan-test/run: true/'; do
  sed "$mutation" "$root/.github/workflows/security.yml" >"$fixture/workflow.yml"
  expect_exit 1 bash "$root/scripts/check-security-contract.sh" "$fixture/workflow.yml"
done

# Given a missing pinned executable. When / Then: each scanner rejects it.
for scanner in govulncheck gosec gitleaks; do
  expect_exit 2 env SECURITY_TOOL_DIR="$fixture/missing" bash "$runner" "$scanner" "$fixture/app"
  jq -e '.status == "incomplete"' "$fixture/reports/$scanner.json" >/dev/null
done
printf 'Security gate detection/error/redaction tests: PASS\n'
