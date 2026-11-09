#!/usr/bin/env bash
# Share the transport policy between development and demo HTTPS proxies.
set -euo pipefail
listen=${1:?listener is required}
certificate=${2:?certificate path is required}
key=${3:?key path is required}
backend=${4:?backend is required}
public_port=${5:-}
hsts=${6:-0}
if [[ -n "$public_port" ]] && \
    { [[ ! "$public_port" =~ ^[1-9][0-9]{0,4}$ ]] || ((public_port > 65535)); }; then
    echo 'Public HTTPS port must be between 1 and 65535.' >&2
    exit 2
fi
[[ "$hsts" == 0 || "$hsts" == 1 ]] || {
    echo 'HSTS must be 0 or 1.' >&2
    exit 2
}

cat <<EOF
$listen {
	tls $certificate $key
EOF
if [[ -n "$public_port" ]]; then
    # Override Caddy's internal listener port when the response is written.
    printf '\theader >Alt-Svc "h3=\\":%s\\"; ma=86400"\n' "$public_port"
fi
if [[ "$hsts" == 1 ]]; then
    printf '\t@https protocol https\n\theader @https Strict-Transport-Security "max-age=86400"\n'
fi
cat <<EOF
	reverse_proxy $backend {
		transport http {
			expect_continue_timeout 1s
		}
	}
}
EOF
