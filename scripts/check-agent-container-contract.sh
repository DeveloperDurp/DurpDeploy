#!/usr/bin/env bash
set -euo pipefail

root=${AGENT_CONTAINER_CONTRACT_ROOT:-.}
image=${AGENT_CONTAINER_IMAGE:-durpdeploy-agent:contract}

require_file() {
	if [ ! -f "$root/$1" ]; then
		echo "agent container contract: missing $1" >&2
		exit 1
	fi
}

require_text() {
	local file=$1 text=$2 description=$3
	if ! grep -Fq -- "$text" "$root/$file"; then
		echo "agent container contract: $description" >&2
		exit 1
	fi
}

require_file Dockerfile.agent
require_file Makefile
require_text Dockerfile.agent 'USER 10001' 'agent image must run as UID 10001'
require_text Dockerfile.agent 'VOLUME ["/var/lib/durpdeploy-agent", "/tmp"]' \
	'agent image must declare writable state and temporary volumes'
require_text Makefile 'build-agent' 'Make must build the agent binary'
require_text Makefile 'agent-container' 'Make must build the agent image'

docker build -f "$root/Dockerfile.agent" -t "$image" "$root"

if [ "$(docker image inspect --format '{{.Config.User}}' "$image")" != 10001 ]; then
	echo 'agent container contract: image user is not UID 10001' >&2
	exit 1
fi
if [ "$(docker image inspect --format '{{json .Config.ExposedPorts}}' "$image")" != null ]; then
	echo 'agent container contract: image must not expose a port' >&2
	exit 1
fi
if docker image inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "$image" | \
	grep -Eq '^DURPDEPLOY_(SECRET_KEY|DB)='; then
	echo 'agent container contract: image includes server storage or secret configuration' >&2
	exit 1
fi

docker run --rm --read-only --entrypoint sh "$image" -ceu '
	test -w /var/lib/durpdeploy-agent
	test -w /tmp
	test ! -w /
	command -v bash
	command -v setpriv
	! command -v curl
	! command -v docker
	! test -e /usr/local/bin/durpdeploy
	! test -e /data
	! test -S /var/run/docker.sock
'

set +e
output=$(docker run --rm --read-only \
	-e DURPDEPLOY_AGENT_ENROLLMENT_TOKEN=placeholder \
	-e DURPDEPLOY_AGENT_ID=agent-contract \
	-e DURPDEPLOY_AGENT_NAME=contract \
	-e DURPDEPLOY_AGENT_VERSION=contract \
	"$image" 2>&1)
status=$?
set -e
if [ "$status" -eq 0 ] || ! grep -Fq 'agent client requires server' <<<"$output"; then
	echo 'agent container contract: missing server URL/fingerprint does not fail clearly' >&2
	exit 1
fi

printf '%s\n' 'agent container contract: PASS'
