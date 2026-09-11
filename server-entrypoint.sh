#!/bin/sh
set -eu

exec /usr/bin/setpriv \
	--reuid=durpdeploy --regid=durpdeploy --clear-groups \
	--inh-caps=+setuid,+setgid,+setpcap \
	--ambient-caps=+setuid,+setgid,+setpcap \
	-- "$@"
