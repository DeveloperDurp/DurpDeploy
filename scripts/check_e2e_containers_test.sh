#!/usr/bin/env bash
set -euo pipefail

if [[ "${1:-}" == --fake-docker ]]; then
	shift
	printf '%s\n' "$*" >>"$CALL_LOG"
	case "$1" in
		ps|events)
			if [[ " $* " != *" label=io.durpdeploy.namespace=docker:$TEST_NAMESPACE "* ]]; then
				printf 'REJECTED namespace filter\n' >>"$CALL_LOG"
				exit 64
			fi
			[[ "$MODE" != query-failure ]] || exit 42
			if [[ "$1" == ps && "$MODE" == leftover ]]; then
				printf 'test-container-id\n'
			fi
			if [[ "$1" == events ]]; then
				for event in create start die stop kill destroy; do
					if [[ " $* " != *" --filter event=$event "* ]]; then
						printf 'exec_create: DIAGNOSTIC_SECRET_SENTINEL\n'
					fi
				done
				printf 'create test-container-id\n'
			fi
			;;
		inspect)
			[[ "$*" != *'.Config.Env'* && "$*" != *'.Config.Cmd'* && "$*" != *'{{json'* ]] || exit 64
			printf 'test-container-id state=running\n'
			;;
		*) exit 64 ;;
	esac
	exit 0
fi

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
test_script="$root/scripts/check_e2e_containers_test.sh"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
printf '#!/usr/bin/env bash\nexec bash %q --fake-docker "$@"\n' "$test_script" >"$tmp/docker"
chmod +x "$tmp/docker"
export PATH="$tmp:$PATH" CALL_LOG="$tmp/calls" TEST_NAMESPACE=e2e_this_thread
since=2026-01-01T00:00:00Z

# Given / When / Then: only this run's namespace is queried; empty passes.
MODE=empty bash "$root/scripts/check_e2e_containers.sh" "$TEST_NAMESPACE" "$since"

# Given / When / Then: survivors remain a failure, with safe metadata only.
status=0
MODE=leftover bash "$root/scripts/check_e2e_containers.sh" "$TEST_NAMESPACE" "$since" \
	>"$tmp/result" 2>&1 || status=$?
[[ "$status" == 1 ]]
grep -q 'state=running' "$tmp/result"
grep -q 'create test-container-id' "$tmp/result"
if grep -q DIAGNOSTIC_SECRET_SENTINEL "$tmp/result"; then exit 1; fi
grep -q '^events ' "$CALL_LOG"
if grep -q '^REJECTED ' "$CALL_LOG"; then exit 1; fi
if grep -Eq '^(rm|stop|kill|prune) ' "$CALL_LOG"; then exit 1; fi

# Given / When / Then: an engine query failure is not mistaken for clean state.
status=0
MODE=query-failure bash "$root/scripts/check_e2e_containers.sh" "$TEST_NAMESPACE" "$since" \
	>"$tmp/result" 2>&1 || status=$?
[[ "$status" == 42 ]]
printf 'E2E namespace diagnostic contract: PASS\n'
