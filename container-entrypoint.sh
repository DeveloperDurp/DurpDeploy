#!/bin/sh
set -eu

if [ "$(id -u)" -ne 0 ]; then
	exec /usr/local/bin/durpdeploy "$@"
fi

runtime_gid=10001
for socket in /var/run/durpdeploy-runtime.sock /var/run/docker.sock \
	/run/podman/podman.sock; do
	if [ -S "$socket" ]; then
		chmod g+rw "$socket"
		runtime_gid=$(stat -c '%g' "$socket")
		break
	fi
done

exec su-exec "10001:$runtime_gid" /usr/local/bin/durpdeploy "$@"
