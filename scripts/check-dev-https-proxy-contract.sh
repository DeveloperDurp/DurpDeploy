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

bash -n "$root/scripts/dev_https_proxy.sh" "$root/scripts/e2e_db_test.sh"
make -C "$root" -n dev dev-postgres dev-mssql >/dev/null

grep -Fq 'caddy:2-alpine' "$root/scripts/dev_https_proxy.sh"
grep -Fq -- '--add-host host.docker.internal:host-gateway' "$root/scripts/dev_https_proxy.sh"
grep -Fq 'trap cleanup EXIT' "$root/scripts/dev_https_proxy.sh"
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

echo 'dev HTTPS proxy contract: OK'
