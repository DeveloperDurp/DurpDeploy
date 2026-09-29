#!/bin/sh
set -eu

if [ "$(id -u)" -ne 0 ]; then
	exec /usr/local/bin/durpdeploy "$@"
fi

runtime_gid=10001
for socket in /var/run/durpdeploy-runtime.sock /var/run/docker.sock \
	/run/podman/podman.sock; do
	if [ -S "$socket" ]; then
		runtime_gid=$(stat -c '%g' "$socket")
		if ! su-exec "10001:$runtime_gid" test -r "$socket" ||
			! su-exec "10001:$runtime_gid" test -w "$socket"; then
			echo "runtime socket must grant read/write access to group $runtime_gid: $socket" >&2
			exit 1
		fi
		break
	fi
done

exec su-exec "10001:$runtime_gid" /usr/local/bin/durpdeploy "$@"
