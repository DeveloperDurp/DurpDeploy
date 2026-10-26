#!/usr/bin/env bash
# Share the transport policy between development and demo HTTPS proxies.
set -euo pipefail
listen=${1:?listener is required}
certificate=${2:?certificate path is required}
key=${3:?key path is required}
backend=${4:?backend is required}

cat <<EOF
$listen {
	tls $certificate $key
	reverse_proxy $backend {
		transport http {
			expect_continue_timeout 1s
		}
	}
}
EOF
