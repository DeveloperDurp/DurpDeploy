#!/usr/bin/env bash
set -euo pipefail

usage() {
	printf '%s\n' \
		'usage: run_named_go_tests.sh [--race] --packages PACKAGES --tests EXPRESSION (--require NAMES | --require-file PATH)' >&2
}

packages_value=
tests_value=
require_value=
require_file=
race_flag=()

while (($# > 0)); do
	case $1 in
	--race)
		race_flag=(-race)
		shift
		;;
	--packages | --tests | --require | --require-file)
		if (($# < 2)); then
			usage
			exit 2
		fi
		case $1 in
		--packages) packages_value=$2 ;;
		--tests) tests_value=$2 ;;
		--require) require_value=$2 ;;
		--require-file) require_file=$2 ;;
		esac
		shift 2
		;;
	*)
		usage
		exit 2
		;;
	esac
done

if [[ -z $packages_value || -z $tests_value ]]; then
	usage
	exit 2
fi
if [[ -n $require_value && -n $require_file ]] ||
	[[ -z $require_value && -z $require_file ]]; then
	usage
	exit 2
fi

read -r -a packages <<<"$packages_value"
if ((${#packages[@]} == 0)); then
	usage
	exit 2
fi

required=()
if [[ -n $require_value ]]; then
	IFS='|' read -r -a required <<<"$require_value"
else
	if [[ ! -f $require_file ]]; then
		printf 'require file does not exist: %s\n' "$require_file" >&2
		exit 2
	fi
	mapfile -t required <"$require_file"
fi
if ((${#required[@]} == 0)); then
	printf 'at least one required test event is required\n' >&2
	exit 2
fi
for name in "${required[@]}"; do
	if [[ -z $name ]]; then
		printf 'required test event names must not be empty\n' >&2
		exit 2
	fi
done

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
events=$(mktemp "${TMPDIR:-/tmp}/durpdeploy-go-test-events.XXXXXX")
trap 'rm -f "$events"' EXIT

set +e
go test -json -v -count=1 "${race_flag[@]}" \
	-run "^(${tests_value})$" "${packages[@]}" | tee "$events"
go_status=${PIPESTATUS[0]}
set -e

set +e
go run "$repo_root/scripts/run_named_go_tests_verify.go" \
	"${required[@]}" <"$events"
verify_status=$?
set -e

if ((go_status != 0)); then
	exit "$go_status"
fi
exit "$verify_status"
