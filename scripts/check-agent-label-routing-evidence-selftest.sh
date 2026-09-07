#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
checker="$root/scripts/check-agent-label-routing-evidence.sh"
tmp=$(mktemp -d "${TMPDIR:-/tmp}/agent-label-routing-evidence.XXXXXX")
cleanup() {
    local status=$?
    rm -rf -- "$tmp"
    exit "$status"
}
trap cleanup EXIT INT TERM

make_task_fixture() {
    local task=$1
    mkdir -p "$task/browser"
    printf 'generation complete\n' >"$task/generation.txt"
    printf 'quality complete\n' >"$task/quality.txt"
    git -C "$root" rev-parse HEAD >"$task/revision.txt"
    printf 'clean\n' >"$task/worktree.txt"
    printf '%s\n' '--- PASS: TestFull' >"$task/full-tests.txt"
    printf '%s\n' '--- PASS: TestRace' >"$task/race-tests.txt"
    printf 'Dev->Prod skip blocked: OK (422)\n' >"$task/http-e2e.txt"
    printf '{"status":"pass"}\n' >"$task/agent-e2e.txt"
    printf 'full-routing: PASS\n' >"$task/browser.txt"
    for engine in postgres mssql; do
        : >"$task/$engine.txt"
        for test in \
            RoundRobinConcurrentThreeAgents FanoutUniqueChildren ParentConstraints \
            ScheduleOccurrenceCAS ApprovalCAS ApprovalRollback ExactSetRetryApprovalCAS \
            ExactSetRetryApprovalRollback ImmediateAtomicRollback ScheduledDispatchRecovery; do
            printf '%s\n' "    --- PASS: TestAgentLabelRoutingRuntimeParity/$test (0.29s)" >>"$task/$engine.txt"
        done
    done
    printf 'secret scan: clean\n' >"$task/secret-scan.txt"
    printf '{"complete":true}\n' >"$task/cleanup.json"
    printf '{"errors":[]}\n' >"$task/browser/browser-console.json"
    printf '{"browserClosed":true}\n' >"$task/browser/cleanup.json"
    : >"$task/browser/desktop.png"
    : >"$task/browser/tablet.png"
    : >"$task/browser/mobile.png"
    printf '\211PNG\r\n\032\n' >"$task/browser/desktop.png"
    printf '\211PNG\r\n\032\n' >"$task/browser/tablet.png"
    printf '\211PNG\r\n\032\n' >"$task/browser/mobile.png"
    printf 'APPROVE fixture-only review\n' >"$task/task-9-agent-label-routing-review.md"
}

fixture="$tmp/task-9-agent-label-routing"
make_task_fixture "$fixture"
"$checker" "$fixture"
echo "fixture-only populated manifest: PASS"

for mutation in runtime named_suffix skip sha cleanup malformed_cleanup reviewer console malformed_console dirty interrupted; do
    copy="$tmp/$mutation"
    cp -a "$fixture" "$copy"
    case "$mutation" in
        runtime) rm "$copy/postgres.txt" ;;
        named_suffix) sed -i 's#TestAgentLabelRoutingRuntimeParity/ApprovalCAS (0.29s)$#TestAgentLabelRoutingRuntimeParity/ApprovalCASNotRun (0.29s)#' "$copy/postgres.txt" ;;
        skip) printf '%s\n' '--- SKIP: TestAgentLabelRoutingRuntimeParity/ApprovalCAS' >>"$copy/mssql.txt" ;;
        sha) printf '0000000000000000000000000000000000000000\n' >"$copy/revision.txt" ;;
        cleanup) rm "$copy/browser/cleanup.json" ;;
        malformed_cleanup) printf '%s\n' '{"complete":"true"}' >"$copy/cleanup.json" ;;
        reviewer) rm "$copy/task-9-agent-label-routing-review.md" ;;
        console) printf '%s\n' '{"errors":["uncaught browser exception"]}' >"$copy/browser/browser-console.json" ;;
        malformed_console) printf '%s\n' '{' >"$copy/browser/browser-console.json" ;;
        dirty) printf 'dirty\n' >"$copy/worktree.txt" ;;
        interrupted) printf 'interrupted\n' >>"$copy/full-tests.txt" ;;
    esac
    if "$checker" "$copy" >"$tmp/$mutation.out" 2>&1; then
        echo "selftest failure: mutated $mutation fixture passed" >&2
        exit 1
    fi
    if ! grep -Eq 'missing:|evidence failure:' "$tmp/$mutation.out"; then
        cat "$tmp/$mutation.out" >&2
        echo "selftest failure: $mutation did not name the rejected field" >&2
        exit 1
    fi
done

final="$tmp/final"
make_task_fixture "$final/task-9-agent-label-routing"
git -C "$root" rev-parse HEAD >"$final/final-revision.txt"
printf 'generation PASS\n' >"$final/final-generation-barrier.txt"
printf 'F1 PASS\n' >"$final/final-F1-plan-compliance.txt"
printf 'F2 PASS\n' >"$final/final-F2-quality.txt"
mkdir -p "$final/final-F3-manual-qa"
printf 'F3 PASS\n' >"$final/final-F3-manual-qa/browser-command.txt"
printf 'F4 PASS\n' >"$final/final-F4-scope-fidelity.txt"
printf 'APPROVE fixture-only F1\n' >"$final/final-F1-plan-compliance.md"
printf 'APPROVE fixture-only F2\n' >"$final/final-F2-code-quality.md"
printf 'APPROVE fixture-only F3\n' >"$final/final-F3-manual-qa.md"
printf 'APPROVE fixture-only F4\n' >"$final/final-F4-scope-fidelity.md"
printf '{"errors":[]}\n' >"$final/final-F3-manual-qa/browser-console.json"
printf '{"browserClosed":true}\n' >"$final/final-F3-manual-qa/cleanup.json"
printf 'secret scan: clean\n' >"$final/final-F3-manual-qa/secret-scan.txt"
for screenshot in desktop tablet mobile; do
    printf '\211PNG\r\n\032\n' >"$final/final-F3-manual-qa/$screenshot.png"
done
"$checker" --final "$final"
rm "$final/final-F2-code-quality.md"
if "$checker" --final "$final" >"$tmp/final.out" 2>&1; then
    echo 'selftest failure: incomplete final fixture passed' >&2
    exit 1
fi
grep -Eq 'missing: F2 reviewer record:' "$tmp/final.out"

echo "agent-label-routing evidence selftest: PASS"
