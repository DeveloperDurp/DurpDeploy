#!/usr/bin/env bash
set -euo pipefail

usage() {
    cat >&2 <<'EOF'
usage: scripts/check-agent-label-routing-evidence.sh [--final] <evidence-directory>

Normal manifest (<evidence-directory> is task-9-agent-label-routing):
  generation.txt quality.txt full-tests.txt race-tests.txt http-e2e.txt revision.txt worktree.txt
  agent-e2e.txt browser.txt postgres.txt mssql.txt secret-scan.txt cleanup.json
  browser/browser-console.json browser/cleanup.json browser/*.png
  task-9-agent-label-routing-review.md (an independent reviewer record)

Final manifest (<evidence-directory> is .omo/evidence):
  final-generation-barrier.txt final-revision.txt; final-F1 through final-F4 command receipts and
  APPROVE records; final-F3-manual-qa/browser-command.txt plus browser artifacts.
  The final check also validates <evidence-directory>/task-9-agent-label-routing.
EOF
    exit 2
}

final=false
case "${1:-}" in
    --final) final=true; shift ;;
esac
[[ $# -eq 1 ]] || usage
root=$1
[[ -d $root ]] || { echo "missing: evidence directory $root" >&2; exit 1; }

failures=0
missing=()
declare -A seen_missing=()

fail() {
    echo "evidence failure: $*" >&2
    failures=1
}

require_file() {
    local label=$1 file=$2
    if [[ ! -s $file ]]; then
        if [[ -z ${seen_missing[$file]:-} ]]; then
            missing+=("$label: $file")
            seen_missing[$file]=1
        fi
        failures=1
    fi
}

require_text() {
    local label=$1 file=$2 pattern=$3
    require_file "$label" "$file"
    [[ -s $file ]] || return 0
    if ! grep -Eiq -- "$pattern" "$file"; then
        fail "$label has no required observable: $pattern ($file)"
    fi
}

require_runtime_pass() {
    local label=$1 file=$2 test=$3 pattern
    require_file "$label" "$file"
    [[ -s $file ]] || return 0
    pattern="^[[:space:]]*--- PASS: TestAgentLabelRoutingRuntimeParity/$test([[:space:]]+\\([0-9]+(\\.[0-9]+)?s\\))?[[:space:]]*$"
    if ! grep -Eq -- "$pattern" "$file"; then
        fail "$label has no required runtime PASS: $test ($file)"
    fi
}

clean_receipt() {
    local label=$1 file=$2
    require_file "$label" "$file"
    [[ -s $file ]] || return 0
    if grep -Eiq -- '--- SKIP:|^[[:space:]]*SKIP([[:space:]]|$)|"?(result|status)"?[[:space:]]*[:=][[:space:]]*"?skip(ped)?"?([,[:space:]}]|$)|\[no tests to run\]|--- FAIL:|^FAIL([[:space:]]|$)|panic:|fatal error:|race detected|interrupted|terminated by signal|"?result"?[[:space:]]*[:=][[:space:]]*"?fail"?' "$file"; then
        fail "$label contains a failure or skip marker ($file)"
    fi
}

require_json_true() {
    local label=$1 file=$2 key=$3
    require_file "$label" "$file"
    [[ -s $file ]] || return 0
    if ! grep -Eq -- "\"$key\"[[:space:]]*:[[:space:]]*true" "$file"; then
        fail "$label does not record $key=true ($file)"
    fi
}

require_approve() {
    local label=$1 file=$2
    require_file "$label" "$file"
    [[ -s $file ]] || return 0
    if ! grep -Eq -- '(^|[^[:alpha:]])APPROVE([^[:alpha:]]|$)' "$file"; then
        fail "$label has no explicit APPROVE verdict ($file)"
    fi
}

require_current_revision() {
    local label=$1 file=$2 expected
    require_file "$label" "$file"
    [[ -s $file ]] || return 0
    expected=$(git -C "$(dirname "${BASH_SOURCE[0]}")/.." rev-parse HEAD)
    if ! grep -Fxq -- "$expected" "$file"; then
        fail "$label does not contain the current commit SHA ($file)"
    fi
}

require_pngs() {
    local label=$1 directory=$2 count
    if [[ ! -d $directory ]]; then
        missing+=("$label directory: $directory")
        failures=1
        return
    fi
    count=$(find "$directory" -type f -name '*.png' -size +0c | wc -l)
    if (( count < 3 )); then
        fail "$label needs at least three nonempty screenshots ($directory)"
    fi
}

require_empty_console_errors() {
    local label=$1 file=$2
    require_file "$label" "$file"
    [[ -s $file ]] || return 0
    if ! python3 -c '
import json
import sys

with open(sys.argv[1], encoding="utf-8") as receipt:
    value = json.load(receipt)
if not isinstance(value, dict) or value.get("errors") != []:
    raise SystemExit(1)
' "$file" >/dev/null 2>&1; then
        fail "$label must be valid JSON with an empty errors array ($file)"
    fi
}

check_runtime() {
    local file=$1 engine=$2 test
    clean_receipt "$engine runtime parity" "$file"
    for test in \
        RoundRobinConcurrentThreeAgents FanoutUniqueChildren ParentConstraints \
        ScheduleOccurrenceCAS ApprovalCAS ApprovalRollback ExactSetRetryApprovalCAS \
        ExactSetRetryApprovalRollback ImmediateAtomicRollback ScheduledDispatchRecovery; do
        require_runtime_pass "$engine runtime parity" "$file" "$test"
    done
}

check_task9() {
    local task=$1 browser
    require_file 'generation receipt' "$task/generation.txt"
    require_file 'quality receipt' "$task/quality.txt"
    clean_receipt 'generation receipt' "$task/generation.txt"
    clean_receipt 'quality receipt' "$task/quality.txt"
    require_current_revision 'task revision receipt' "$task/revision.txt"
    require_text 'clean worktree receipt' "$task/worktree.txt" '^clean$'
    clean_receipt 'full test receipt' "$task/full-tests.txt"
    require_text 'full test receipt' "$task/full-tests.txt" '--- PASS:'
    clean_receipt 'race test receipt' "$task/race-tests.txt"
    require_text 'race test receipt' "$task/race-tests.txt" '--- PASS:'
    clean_receipt 'HTTP E2E receipt' "$task/http-e2e.txt"
    require_text 'HTTP E2E receipt' "$task/http-e2e.txt" '(PASS|OK)'
    clean_receipt 'agent E2E receipt' "$task/agent-e2e.txt"
    require_text 'agent E2E receipt' "$task/agent-e2e.txt" '(PASS|"status":"pass")'
    clean_receipt 'browser receipt' "$task/browser.txt"
    require_text 'browser receipt' "$task/browser.txt" '(full-routing|PASS)'
    check_runtime "$task/postgres.txt" postgres
    check_runtime "$task/mssql.txt" mssql

    require_file 'secret scan receipt' "$task/secret-scan.txt"
    clean_receipt 'secret scan receipt' "$task/secret-scan.txt"
    require_text 'secret scan receipt' "$task/secret-scan.txt" '(pass|clean|no secrets)'
    require_json_true 'task cleanup receipt' "$task/cleanup.json" 'complete'

    browser="$task/browser"
    require_pngs 'browser screenshots' "$browser"
    require_file 'browser console receipt' "$browser/browser-console.json"
    require_file 'browser cleanup receipt' "$browser/cleanup.json"
    clean_receipt 'browser console receipt' "$browser/browser-console.json"
    require_empty_console_errors 'browser console receipt' \
        "$browser/browser-console.json"
    require_json_true 'browser cleanup receipt' "$browser/cleanup.json" 'browserClosed'
    require_approve 'task 9 reviewer record' "$task/task-9-agent-label-routing-review.md"
}

if [[ $final == false ]]; then
    check_task9 "$root"
else
    check_task9 "$root/task-9-agent-label-routing"
    require_current_revision 'final revision receipt' "$root/final-revision.txt"
    clean_receipt 'final generation barrier' "$root/final-generation-barrier.txt"
    for lane in F1-plan-compliance F2-quality F3-manual-qa/browser-command F4-scope-fidelity; do
        clean_receipt "final $lane receipt" "$root/final-$lane.txt"
    done
    require_approve 'F1 reviewer record' "$root/final-F1-plan-compliance.md"
    require_approve 'F2 reviewer record' "$root/final-F2-code-quality.md"
    require_approve 'F3 reviewer record' "$root/final-F3-manual-qa.md"
    require_approve 'F4 reviewer record' "$root/final-F4-scope-fidelity.md"
    require_pngs 'final browser screenshots' "$root/final-F3-manual-qa"
    require_file 'final browser console receipt' "$root/final-F3-manual-qa/browser-console.json"
    require_file 'final browser cleanup receipt' "$root/final-F3-manual-qa/cleanup.json"
    require_file 'final secret scan receipt' "$root/final-F3-manual-qa/secret-scan.txt"
    clean_receipt 'final browser console receipt' "$root/final-F3-manual-qa/browser-console.json"
    require_empty_console_errors 'final browser console receipt' \
        "$root/final-F3-manual-qa/browser-console.json"
    clean_receipt 'final secret scan receipt' "$root/final-F3-manual-qa/secret-scan.txt"
    require_json_true 'final browser cleanup receipt' \
        "$root/final-F3-manual-qa/cleanup.json" 'browserClosed'
fi

if (( ${#missing[@]} > 0 )); then
    printf 'missing: %s\n' "${missing[@]}" >&2
fi
if (( failures > 0 )); then
    exit 1
fi
echo "agent-label-routing evidence: PASS"
