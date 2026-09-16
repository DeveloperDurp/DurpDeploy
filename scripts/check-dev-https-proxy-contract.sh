#!/usr/bin/env bash
set -euo pipefail

root=${DEV_HTTPS_PROXY_CONTRACT_ROOT:-.}

test_shutdown_tree() {
	local tmp child_pid status
	tmp=$(mktemp -d "${TMPDIR:-/tmp}/durpdeploy-proxy-shutdown.XXXXXX")
	child_pid=""
	cleanup_shutdown_test() {
		[[ -z "$child_pid" ]] || kill "$child_pid" 2>/dev/null || true
		rm -rf "$tmp"
	}
	trap cleanup_shutdown_test RETURN

	mkdir "$tmp/bin"
	cat >"$tmp/bin/docker" <<'EOF'
#!/usr/bin/env bash
case "$1" in
	info|run) exit 0 ;;
	rm) : >"$DURPDEPLOY_PROXY_TEST_CLEANUP_FILE"; exit 0 ;;
	container) exit 1 ;;
	esac
exit 1
EOF
	cat >"$tmp/bin/curl" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
	cat >"$tmp/nested-wrapper" <<EOF
#!/usr/bin/env bash
bash -c 'sleep 30 & child=\$!; printf "%s" "\$child" >"$tmp/child.pid"; wait "\$child"' &
wait
EOF
	chmod +x "$tmp/bin/docker" "$tmp/bin/curl" "$tmp/nested-wrapper"

	set +e
	DURPDEPLOY_PROXY_TEST_CLEANUP_FILE="$tmp/docker-cleaned" \
		PATH="$tmp/bin:$PATH" timeout --preserve-status -s INT 1 \
		"$root/scripts/dev_https_proxy.sh" "$tmp/nested-wrapper"
	status=$?
	set -e
	[[ "$status" -eq 130 ]] || {
		echo "dev HTTPS proxy shutdown: expected SIGINT status 130, got $status" >&2
		return 1
	}
	child_pid=$(<"$tmp/child.pid")
	if kill -0 "$child_pid" 2>/dev/null; then
		echo "dev HTTPS proxy shutdown: nested child survived SIGINT" >&2
		return 1
	fi
	[[ -f "$tmp/docker-cleaned" ]] || {
		echo "dev HTTPS proxy shutdown: proxy cleanup did not run" >&2
		return 1
	}

	printf '%s\n' '#!/usr/bin/env bash' 'exit 42' >"$tmp/exit-status"
	chmod +x "$tmp/exit-status"
	rm "$tmp/docker-cleaned"
	set +e
	DURPDEPLOY_PROXY_TEST_CLEANUP_FILE="$tmp/docker-cleaned" \
		PATH="$tmp/bin:$PATH" "$root/scripts/dev_https_proxy.sh" "$tmp/exit-status"
	status=$?
	set -e
	[[ "$status" -eq 42 ]] || {
		echo "dev HTTPS proxy shutdown: expected child status 42, got $status" >&2
		return 1
	}
	[[ -f "$tmp/docker-cleaned" ]] || {
		echo "dev HTTPS proxy shutdown: normal-exit proxy cleanup did not run" >&2
		return 1
	}
}

test_podman_fallback() {
	local tmp status
	tmp=$(mktemp -d "${TMPDIR:-/tmp}/durpdeploy-proxy-podman.XXXXXX")
	cleanup_podman_test() {
		rm -rf "$tmp"
	}
	trap cleanup_podman_test RETURN

	mkdir "$tmp/bin"
	cat >"$tmp/bin/docker" <<'EOF'
#!/usr/bin/env bash
[[ "$1" == info ]] && exit 1
exit 1
EOF
	cat >"$tmp/bin/podman" <<'EOF'
#!/usr/bin/env bash
case "$1" in
	info|run) exit 0 ;;
	rm) : >"$DURPDEPLOY_PROXY_TEST_CLEANUP_FILE"; exit 0 ;;
	container) exit 1 ;;
esac
exit 1
EOF
	cat >"$tmp/bin/curl" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
	cat >"$tmp/server" <<'EOF'
#!/usr/bin/env bash
sleep 30
EOF
	chmod +x "$tmp/bin/docker" "$tmp/bin/podman" "$tmp/bin/curl" "$tmp/server"

	set +e
	DURPDEPLOY_PROXY_TEST_CLEANUP_FILE="$tmp/podman-cleaned" \
		PATH="$tmp/bin:$PATH" timeout --preserve-status -s INT 1 \
		"$root/scripts/dev_https_proxy.sh" "$tmp/server"
	status=$?
	set -e
	[[ "$status" -eq 130 ]] || {
		echo "dev HTTPS proxy Podman fallback: expected SIGINT status 130, got $status" >&2
		return 1
	}
	[[ -f "$tmp/podman-cleaned" ]] || {
		echo "dev HTTPS proxy Podman fallback: Podman cleanup did not run" >&2
		return 1
	}
}

test_existing_unhealthy_proxy_is_replaced() {
	local tmp
	tmp=$(mktemp -d "${TMPDIR:-/tmp}/durpdeploy-proxy-existing.XXXXXX")
	cleanup_existing_proxy_test() {
		rm -rf "$tmp"
	}
	trap cleanup_existing_proxy_test RETURN

	mkdir "$tmp/bin"
	cat >"$tmp/bin/docker" <<'EOF'
#!/usr/bin/env bash
case "$1" in
info|container) exit 0 ;;
rm) printf 'rm\n' >>"$DURPDEPLOY_PROXY_TEST_OPERATIONS"; exit 0 ;;
run)
	printf 'run\n' >>"$DURPDEPLOY_PROXY_TEST_OPERATIONS"
	: >"$DURPDEPLOY_PROXY_TEST_STARTED"
	exit 0
	;;
esac
exit 1
EOF
	cat >"$tmp/bin/curl" <<'EOF'
#!/usr/bin/env bash
[[ -f "$DURPDEPLOY_PROXY_TEST_STARTED" ]]
EOF
	cat >"$tmp/server" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
	chmod +x "$tmp/bin/docker" "$tmp/bin/curl" "$tmp/server"

	DURPDEPLOY_PROXY_TEST_OPERATIONS="$tmp/operations" \
		DURPDEPLOY_PROXY_TEST_STARTED="$tmp/started" \
		PATH="$tmp/bin:$PATH" \
		"$root/scripts/dev_https_proxy.sh" "$tmp/server"
	grep -Fqx 'rm' "$tmp/operations"
	grep -Fqx 'run' "$tmp/operations"
}

test_selinux_labeled_proxy_mounts() {
	local target
	for target in Caddyfile dev-cert.pem dev-key.pem; do
		grep -Fq "/etc/caddy/$target:ro,Z" \
			"$root/scripts/dev_https_proxy.sh" || {
			echo "dev HTTPS proxy: $target mount lacks private SELinux label" >&2
			return 1
		}
	done
}

test_tls_host_addresses() {
	local tmp
	tmp=$(mktemp -d "${TMPDIR:-/tmp}/durpdeploy-proxy-hosts.XXXXXX")
	cleanup_tls_host_test() {
		rm -rf "$tmp"
	}
	trap cleanup_tls_host_test RETURN

	mkdir "$tmp/bin"
	cat >"$tmp/bin/docker" <<'EOF'
#!/usr/bin/env bash
case "$1" in
	info) exit 0 ;;
	container) exit 1 ;;
	run)
		shift
		while (($#)); do
			if [[ "$1" == -v ]]; then
				source=${2%%:*}
				case "$2" in
				*:/etc/caddy/Caddyfile:ro,Z)
					cp "$source" "$DURPDEPLOY_PROXY_TEST_CONFIG"
					;;
				*:/etc/caddy/dev-cert.pem:ro,Z)
					cp "$source" "$DURPDEPLOY_PROXY_TEST_CERT"
					;;
				esac
				shift 2
				continue
			fi
			shift
		done
		exit 0
		;;
	rm) exit 0 ;;
esac
exit 1
EOF
	cat >"$tmp/bin/curl" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
	cat >"$tmp/bin/hostname" <<'EOF'
#!/usr/bin/env bash
[[ "${1:-}" == -I ]] || exit 1
printf '%s\n' '127.0.0.1 192.0.2.10 ::1 2001:db8::10'
EOF
	cat >"$tmp/server" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
	chmod +x "$tmp/bin/docker" "$tmp/bin/curl" \
		"$tmp/bin/hostname" "$tmp/server"

	DURPDEPLOY_PROXY_TEST_CONFIG="$tmp/Caddyfile" \
		DURPDEPLOY_PROXY_TEST_CERT="$tmp/dev-cert.pem" \
		PATH="$tmp/bin:$PATH" \
		"$root/scripts/dev_https_proxy.sh" "$tmp/server"
	grep -Fqx ':443 {' "$tmp/Caddyfile" || {
		echo 'dev HTTPS proxy host test: missing catch-all listener' >&2
		return 1
	}
	grep -Fq \
		'tls /etc/caddy/dev-cert.pem /etc/caddy/dev-key.pem' \
		"$tmp/Caddyfile" || {
		echo 'dev HTTPS proxy host test: missing static certificate' >&2
		return 1
	}
	local sans
	sans=$(openssl x509 -in "$tmp/dev-cert.pem" -noout \
		-ext subjectAltName) || {
		echo 'dev HTTPS proxy host test: invalid certificate' >&2
		return 1
	}
	for expected in \
		'DNS:localhost' \
		'IP Address:127.0.0.1' \
		'IP Address:192.0.2.10' \
		'IP Address:0:0:0:0:0:0:0:1' \
		'IP Address:2001:DB8:0:0:0:0:0:10'; do
		grep -Fq "$expected" <<<"$sans" || {
			echo "dev HTTPS proxy host test: missing SAN $expected" >&2
			return 1
		}
	done
}

bash -n "$root/scripts/dev_https_proxy.sh" "$root/scripts/e2e_db_test.sh"
make -C "$root" -n dev dev-postgres dev-mssql >/dev/null

grep -Fq \
	'image=${DURPDEPLOY_HTTPS_PROXY_IMAGE:-docker.io/library/caddy:2-alpine}' \
	"$root/scripts/dev_https_proxy.sh"
grep -Fq -- '--add-host host.docker.internal:host-gateway' "$root/scripts/dev_https_proxy.sh"
grep -Fq 'trap cleanup EXIT' "$root/scripts/dev_https_proxy.sh"
grep -Fq 'dev-agent-identity' "$root/Makefile"
grep -Fq 'DURPDEPLOY_AGENT_IDENTITY_DIR' "$root/Makefile"
grep -Fq \
	'DURPDEPLOY_AGENT_LISTEN_ADDR=$${DURPDEPLOY_AGENT_LISTEN_ADDR:-0.0.0.0:10943}' \
	"$root/Makefile"
grep -Fq \
	'DURPDEPLOY_AGENT_PUBLIC_URL=$${DURPDEPLOY_AGENT_PUBLIC_URL:-https://host.containers.internal:10943}' \
	"$root/Makefile"
grep -Fq \
	'DURPDEPLOY_AGENT_IDENTITY_DIR=$${DURPDEPLOY_AGENT_IDENTITY_DIR:-$(MAKEFILE_DIR)tmp/dev-agent-identity}' \
	"$root/Makefile"
grep -Fq './scripts/e2e_db_test.sh sqlite' "$root/Makefile"
grep -Fq 'e2e-test-isolated:' "$root/Makefile"
if grep -Eq 'go (build|run)|\$TMP/durpdeploy' "$root/scripts/e2e_db_test.sh"; then
    echo 'dev HTTPS proxy contract: running-server E2E must not build or launch DurpDeploy' >&2
    exit 1
fi
grep -Fq '/settings/security/totp/verify' "$root/scripts/e2e_test.sh"
grep -Fq '/settings/security/totp/cancel' "$root/scripts/e2e_test.sh"
grep -Fq '/settings/security/recovery/continue' "$root/scripts/e2e_test.sh"
test_shutdown_tree
test_podman_fallback
test_existing_unhealthy_proxy_is_replaced
test_selinux_labeled_proxy_mounts
test_tls_host_addresses

echo 'dev HTTPS proxy contract: OK'
