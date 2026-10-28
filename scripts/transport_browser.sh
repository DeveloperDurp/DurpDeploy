#!/usr/bin/env bash
# The parent owns this container. Input contains only synthetic browser credentials.
set -euo pipefail
umask 077
[[ $# == 3 ]] || exit 2
cat > /tmp/browser-fixture.json
export HOME=/tmp XDG_CONFIG_HOME=/tmp/config XDG_CACHE_HOME=/tmp/cache
export HTTPS_SECURITY_FIXTURE=/tmp/browser-fixture.json
export DURPDEPLOY_PACKAGE_UI_EVIDENCE=/tmp/screenshots
browser_pid=
phase=chromium-startup
finish() {
    local result=$? check reason=check-failed
    trap - EXIT INT TERM HUP
    if ((result != 0)); then
        if [[ $phase == go-test && -f /tmp/browser-test.log ]]; then
            while IFS= read -r check; do
                case $check in
                    'TRANSPORT_BROWSER_STEP cdp-connect'|'TRANSPORT_BROWSER_STEP login-page'|\
                    'TRANSPORT_BROWSER_STEP login-submit'|'TRANSPORT_BROWSER_STEP session-cookie'|\
                    'TRANSPORT_BROWSER_STEP authenticated-screenshot'|'TRANSPORT_BROWSER_STEP logout-submit'|\
                    'TRANSPORT_BROWSER_STEP logout-page'|'TRANSPORT_BROWSER_STEP logout-screenshot')
                        phase=${check#TRANSPORT_BROWSER_STEP } ;;
                    *) : ;; # Keep raw and unexpected diagnostic lines private.
                esac
            done </tmp/browser-test.log
            if grep -q '^panic: test timed out after ' /tmp/browser-test.log; then reason=go-timeout; fi
        fi
        if ((result == 130 || result == 143)); then reason=interrupted; fi
        printf 'TRANSPORT_BROWSER_DIAGNOSTIC %s %s\n' "$phase" "$reason" >&2
    fi
    if [[ -n $browser_pid ]]; then
        kill "$browser_pid" 2>/dev/null || true
        wait "$browser_pid" 2>/dev/null || true
    fi
    exit "$result"
}
trap finish EXIT
trap 'exit 130' INT
trap 'exit 143' TERM HUP
/ms-playwright/chromium-*/chrome-linux*/chrome --headless --no-sandbox \
    --disable-dev-shm-usage --disable-background-networking --no-first-run \
    --remote-debugging-address=127.0.0.1 --remote-debugging-port=0 \
    --user-data-dir=/tmp/browser --ignore-certificate-errors-spki-list="$3" \
    --host-resolver-rules="MAP $2 $1, EXCLUDE localhost" about:blank \
    >/tmp/chromium.log 2>&1 &
browser_pid=$!
for attempt in {1..80}; do
    if [[ -s /tmp/browser/DevToolsActivePort ]]; then
        mapfile -t address </tmp/browser/DevToolsActivePort
        if [[ ${#address[@]} == 2 && ${address[0]} =~ ^[0-9]+$ &&
            ${address[1]} == /devtools/browser/* ]]; then
            endpoint="ws://127.0.0.1:${address[0]}${address[1]}"
            break
        fi
    fi
    kill -0 "$browser_pid"
    sleep 0.25
done
[[ -n ${endpoint:-} ]] || exit 1
export DURPDEPLOY_TEST_BROWSER_ENDPOINT=$endpoint
touch /tmp/browser-ready
phase=go-test
if [[ ${TRANSPORT_BROWSER_HOLD:-0} == 1 ]]; then sleep 30; fi
test_timeout=1m
if [[ ${TRANSPORT_BROWSER_FORCE_TIMEOUT:-0} == 1 ]]; then
    test_timeout=1ns
    # The negative control must prove that raw stderr never reaches reports.
    printf 'transport-private-browser-canary\n' >&2
fi
if ! /opt/transport-browser.test -test.count=1 -test.timeout="$test_timeout" \
    -test.run '^TestTransportBrowserE2E$' >/tmp/browser-test.log 2>&1; then
    cat /tmp/browser-test.log >&2
    exit 1
fi
# Emit only the two fixed screenshots before container tmpfs disappears.
phase=screenshot-export
tar --format=ustar -C /tmp/screenshots -cf - \
    transport-authenticated-https.png transport-logout.png
