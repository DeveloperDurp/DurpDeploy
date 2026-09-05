#!/usr/bin/env bash
set -o pipefail

if [ "$#" -lt 2 ]; then
    echo "usage: $0 <output> <command...>" >&2
    exit 2
fi

output=$1
shift
mkdir -p "$(dirname "$output")"
: >"$output"

command_name=$1
if [ "$command_name" = "env" ]; then
    for argument in "${@:2}"; do
        case "$argument" in
            *=*) ;;
            *) command_name=$argument; break ;;
        esac
    done
fi

is_go_test=0
if [ "${command_name##*/}" = "go" ]; then
    for argument in "$@"; do
        if [ "$argument" = "test" ]; then
            is_go_test=1
            break
        fi
    done
fi

if [ "${command_name##*/}" = "go" ] &&
    [ "${CAPTURE_TEST_GENERATED:-0}" != "1" ]; then
    templ generate > >(tee "$output") 2>&1 || exit $?
    make swagger-ui-copy > >(tee -a "$output") 2>&1 || exit $?
fi

"$@" 2>&1 | tee -a "$output"
status=${PIPESTATUS[0]}
if [ "$status" -ne 0 ]; then
    exit "$status"
fi

if [ "$is_go_test" -eq 1 ]; then
    if grep -Fq '[no tests to run]' "$output" ||
        ! grep -Fq -- '--- PASS:' "$output"; then
        echo "capture-test: go test did not execute a passing test" |
            tee -a "$output" >&2
        exit 1
    fi
fi
