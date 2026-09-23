#!/usr/bin/env bash
set -euo pipefail

root=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
bash "$root/scripts/check-ci-contract.sh" "$@"

ci="$root/.gitlab-ci.yml"
for required in \
	'golang:1.26.8-alpine@sha256:' \
	'golang:1.26.8-bookworm@sha256:' \
	'docker:24-cli@sha256:2b62f89aaab9e4df0ff843f0d9a7bbd17340a9ba7c660ba6f40b1f00b38c2a4d' \
	'docker:24-dind@sha256:9b17a9f25adf17b88d0a013b4f00160754adf4b07ccbe9986664a49886c2c98e' \
	'DOCKER_HOST: tcp://docker:2376' \
	'DOCKER_TLS_VERIFY: "1"' \
	'agent:rollout:' \
	'make agent-rollout-gate' \
	'agent:smoke:' \
	'agent-smoke-container'; do
	grep -Fq "$required" "$ci" || {
		printf 'agent CI contract: missing %s\n' "$required" >&2
		exit 1
	}
done

printf '%s\n' 'agent CI rollout contract: PASS'
