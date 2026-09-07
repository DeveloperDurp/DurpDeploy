#!/usr/bin/env bash
set -euo pipefail

usage() {
    echo "usage: $0 [--scope-only]" >&2
    exit 2
}

scope_only=false
case "${1:-}" in
    '') ;;
    --scope-only) scope_only=true ;;
    *) usage ;;
esac

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"
failures=0

fail() {
    echo "contract failure: $*" >&2
    failures=1
}

need() {
    local file=$1 pattern=$2 description=$3
    if ! grep -Eq -- "$pattern" "$file"; then
        fail "$description ($file)"
    fi
}

for file in \
    internal/dispatch/routing.go \
    internal/dispatch/routing_selection.go \
    internal/deploymentstate/routing.go \
    internal/handler/api/logs.go \
    internal/handler/api/log_stream.go \
    internal/handler/api/deployments.go \
    internal/events/bus.go \
    queries/agent_label_routing.sql; do
    [[ -s $file ]] || fail "required routing surface is absent: $file"
done

need internal/dispatch/routing.go 'StrategyRoundRobin.*round_robin' \
    'round-robin policy is absent'
need internal/dispatch/routing.go 'StrategyAll.*all' 'all-agent policy is absent'
need internal/dispatch/routing_selection.go 'ListEligibleAgentLabelMembers' \
    'label eligibility query is not used'
need internal/dispatch/routing_test.go 'DoesNotFallback' \
    'no-match local-fallback guard test is absent'
need internal/dispatch/routing_test.go 'NotStaleHeartbeat' \
    'heartbeat is incorrectly part of routing eligibility'
need internal/deploymentstate/routing.go 'ParentLogMessage' \
    'parent log conflict contract is absent'
need internal/handler/api/logs.go 'rejectParentLogs' \
    'parent JSON log endpoint can bypass the conflict contract'
need internal/handler/api/log_stream.go 'rejectParentLogs' \
    'parent streaming log endpoint can bypass the conflict contract'
need internal/handler/api/deployments.go 'rejectParentLogs' \
    'parent text log endpoint can bypass the conflict contract'
need internal/events/bus.go 'notificationExists' \
    'notification duplicate guard is absent'
need internal/events/bus_test.go 'DelayedChildStartNotifiesStartedRootBeforeTerminal' \
    'root-only notification guard test is absent'

if rg -n -i 'selector|\bdsl\b|expression parser' internal/dispatch \
    internal/deploymentstate >/dev/null; then
    fail 'routing layer introduces a selector DSL'
fi

if rg -n -i 'create table (job|jobs)|type Job struct|/jobs\b' \
    migrations/*agent* queries/agent* internal/dispatch internal/deploymentstate \
    >/dev/null; then
    fail 'routing scope introduces a job entity'
fi

if [[ $scope_only == false ]]; then
    need Makefile 'go-swagger/cmd/swagger@v0\.32\.3 generate spec' \
        'Swagger generator command is not pinned'
    need Makefile ' -c durpdeploy/internal/handler/api' \
        'Swagger generator package is not passed with -c'
    need docs/agents.md 'Retry preserves the exact saved agent IDs' \
        'operator retry contract is absent'
    need docs/agents.md 'does not run' \
        'operator no-match contract is absent'
fi

if [[ $failures -ne 0 ]]; then
    exit 1
fi
echo "agent-label-routing contract: PASS"
