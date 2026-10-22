#!/usr/bin/env bash
# Register one stack for this worktree. T3 calls settle in the owning worktree.
set -euo pipefail
umask 077

fail() { printf 'dev-stack-thread: %s\n' "$*" >&2; exit 1; }
[[ $# == 1 || ( $# == 2 && $1 == logs ) ]] ||
    fail 'Usage: dev_stack_thread.sh up|status|logs|down|settle [app|caddy]'
action=$1
case $action in up|status|logs|down|settle) ;; *) fail 'Unknown operation' ;; esac
launcher=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)/dev_stack.sh
worktree=$(git rev-parse --show-toplevel)
worktree=$(CDPATH= cd -- "$worktree" && pwd -P)
export DEV_STACK_OWNER_WORKTREE=$worktree
if [[ -n ${T3CODE_WORKTREE_PATH:-} ]]; then
    expected=$(CDPATH= cd -- "$T3CODE_WORKTREE_PATH" && pwd -P)
    [[ $expected == "$worktree" ]] || fail 'T3 worktree does not match the current checkout'
fi
registry=$worktree/.local/dev-stack.json
[[ ! -L $worktree/.local && ! -L $registry && ! -L $registry.lock ]] ||
    fail 'Refusing a linked registration path'
mkdir -p -- "$worktree/.local"
exec 8>"$registry.lock"
flock -n 8 || fail 'Another stack operation is active; retry after it finishes'

if [[ $action == up ]]; then
    [[ ! -e $registry ]] || fail 'A stack is registered; run down before starting another'
    parent=$(realpath -m -- "${XDG_STATE_HOME:-$HOME/.local/state}/durpdeploy/worktree-stacks")
    [[ $parent != "$worktree" && $parent != "$worktree/"* ]] ||
        fail 'Keep stack state outside the source worktree'
    mkdir -p -- "$parent"
    state=$(mktemp -d "$parent/thread.XXXXXXXXXXXX")/stack
    jq -n --arg source "$worktree" --arg state "$state" \
        '{schema:1,source_dir:$source,state_dir:$state}' >"$registry.next"
    mv -- "$registry.next" "$registry"
    # Register before startup so an interrupted launch can be cleaned up later.
    if DEV_STACK_SOURCE=$worktree bash "$launcher" up "$state"; then
        exit 0
    else
        result=$?
        # A failed prerequisite check happens before the launcher creates state.
        if [[ ! -e $state ]]; then rm -- "$registry"; fi
        exit "$result"
    fi
fi

if [[ ! -e $registry ]]; then
    [[ $action == settle || $action == down ]] && exit 0
    fail 'No stack is registered for this worktree'
fi
jq -e --arg source "$worktree" '
    .schema == 1 and .source_dir == $source and
    (.state_dir | type == "string" and startswith("/"))
' "$registry" >/dev/null || fail 'Invalid or copied stack registration'
state=$(jq -er .state_dir "$registry")
[[ -f $state/connection.json && ! -L $state/connection.json ]] ||
    fail 'Missing connection record; registration retained'
jq -e --arg source "$worktree" '.source_dir == $source' \
    "$state/connection.json" >/dev/null || fail 'Stack belongs to another worktree'
case $action in
    down|settle)
        bash "$launcher" down "$state"
        rm -- "$registry"
        ;;
    logs) bash "$launcher" logs "$state" "${2:-app}" ;;
    status) bash "$launcher" status "$state" ;;
    *) fail 'Unknown operation' ;;
esac
