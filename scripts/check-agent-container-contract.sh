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

forbid_text() {
	local file=$1 text=$2 description=$3
	if grep -Fq -- "$text" "$root/$file"; then
		echo "agent container contract: $description" >&2
		exit 1
	fi
}

require_file Dockerfile.agent
require_file Makefile
require_file internal/agentbootstrap/listener.go
require_file internal/agentbootstrap/commit.go
require_text Dockerfile.agent 'USER root' \
	'agent image must bootstrap the agent capabilities as root'
require_text Dockerfile.agent 'docker-agent-entrypoint.sh' \
	'agent image must drop to its service identity in the entrypoint'
require_text Dockerfile.agent 'durpdeploy-runner' \
	'agent image must create the distinct runner identity'
require_text Dockerfile.agent 'util-linux' \
	'agent image must provide util-linux setpriv'
require_text Dockerfile.agent 'VOLUME ["/var/lib/durpdeploy-agent", "/tmp"]' \
	'agent image must declare writable state and temporary volumes'
require_text Makefile 'build-agent' 'Make must build the agent binary'
require_text Makefile 'agent-container' 'Make must build the agent image'
require_text internal/agentbootstrap/listener.go \
	'mux.HandleFunc(agentproto.ServerInitPath, listener.serverInit)' \
	'agent bootstrap must expose only the server-init pairing route'
forbid_text internal/agentbootstrap/listener.go 'BootstrapPath' \
	'agent bootstrap must not restore the code-bearing GET route'
forbid_text internal/agentbootstrap/listener.go '"/agent/v1/bootstrap"' \
	'agent bootstrap must not restore the code-bearing GET route'
require_text internal/agentbootstrap/listener.go 'ClientAuth:   tls.RequestClientCert,' \
	'agent bootstrap TLS must request the server client certificate'
require_text internal/agentbootstrap/commit.go \
	'len(request.TLS.PeerCertificates) != 1' \
	'server-init must reject requests without exactly one client certificate'
require_text internal/agentbootstrap/commit.go \
	'serverPin != pairRequest.ServerPin' \
	'server-init must bind the request server pin to the mTLS peer certificate'

docker build -f "$root/Dockerfile.agent" -t "$image" "$root"

if [ "$(docker image inspect --format '{{.Config.User}}' "$image")" != root ]; then
	echo 'agent container contract: image user is not root for capability bootstrap' >&2
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

docker run --rm --read-only --security-opt no-new-privileges:true \
	--cap-drop ALL \
	--cap-add SETUID --cap-add SETGID --cap-add SETPCAP \
	--cap-add SYS_ADMIN --cap-add SYS_CHROOT \
	"$image" sh -ceu '
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
	test "$(id -u durpdeploy-runner)" = 10002
	sandbox=$(mktemp -d)
	cleanup() {
		for source in /bin /usr /lib /lib64 /proc; do
			/bin/busybox umount "$sandbox$source" 2>/dev/null || true
		done
		rm -rf "$sandbox"
	}
	trap cleanup EXIT
	for source in /bin /usr /lib; do
		target="$sandbox$source"
		mkdir -p "$target"
		/bin/busybox mount -o bind,ro "$source" "$target"
	done
	if test -e /lib64; then
		mkdir -p "$sandbox/lib64"
		/bin/busybox mount -o bind,ro /lib64 "$sandbox/lib64"
	fi
	mkdir -p "$sandbox/proc"
	/bin/busybox mount -o bind,ro /proc "$sandbox/proc"
	cat > "$sandbox/script.sh" <<"EOF"
test "$(id -u)" = 10002
test ! -e /var/lib/durpdeploy-agent
test ! -w /usr
for capability_set in CapInh CapPrm CapEff CapBnd CapAmb; do
	test "$(grep "^$capability_set:" /proc/self/status | tr -s "[:space:]" " " | cut -d " " -f 2)" = 0000000000000000
done
test "$(grep "^NoNewPrivs:" /proc/self/status | tr -s "[:space:]" " " | cut -d " " -f 2)" = 1
EOF
	chmod 0755 "$sandbox/script.sh"
	chmod 0711 "$sandbox"
	chroot "$sandbox" /usr/bin/setpriv \
		--reuid=10002 --regid=10002 --clear-groups \
		--bounding-set=-all --inh-caps=-all --ambient-caps=-all --no-new-privs -- \
		/bin/bash /script.sh
'

help=$(docker run --rm --read-only "$image" --help)
for required in DURPDEPLOY_AGENT_LISTEN_ADDR DURPDEPLOY_AGENT_STATE_DIR \
	DURPDEPLOY_AGENT_VERSION; do
	grep -Fq "$required" <<<"$help" || {
		echo "agent container contract: help omits $required" >&2
		exit 1
	}
done
if grep -Fq 'DURPDEPLOY_AGENT_SERVER_URL' <<<"$help"; then
	echo 'agent container contract: help exposes manual server configuration' >&2
	exit 1
fi

printf '%s\n' 'agent container contract: PASS'
