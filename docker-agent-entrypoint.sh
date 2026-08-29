#!/bin/sh
set -eu

# Docker starts with the narrow bootstrap capabilities as root. setpriv then
# transfers only the sandbox setup set to durpdeploy-agent before it execs.
exec /usr/bin/setpriv \
	--reuid=durpdeploy-agent --regid=durpdeploy-agent --clear-groups \
	--inh-caps=+setuid,+setgid,+setpcap,+sys_admin,+sys_chroot \
	--ambient-caps=+setuid,+setgid,+setpcap,+sys_admin,+sys_chroot \
	-- "$@"
