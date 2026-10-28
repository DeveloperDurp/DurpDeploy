#!/usr/bin/env bash
# Real failure and cancellation controls. Never edit this script while it runs.
set -euo pipefail
umask 077
root=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"
runtime=${DEV_STACK_RUNTIME:-podman}
command -v "$runtime" >/dev/null || runtime=docker
socket=${DEV_STACK_SOCKET:-/var/run/docker.sock}
if [[ $runtime == podman && -z ${DEV_STACK_SOCKET:-} ]]; then
    socket=${XDG_RUNTIME_DIR:-/run/user/$(id -u)}/podman/podman.sock
fi
args=(--host="unix://$socket")
if [[ $runtime == podman ]]; then args=(--remote --url="unix://$socket"); fi
engine() { timeout --signal=TERM --kill-after=5s 60s "$runtime" "${args[@]}" "$@"; }
private=$(mktemp -d "${TMPDIR:-/tmp}/durpdeploy-transport-controls.XXXXXXXX")
tools=localhost/transport-controls:$(openssl rand -hex 12)
inert=$tools-inert
child=
report=$root/artifacts/transport
mkdir -p "$report"
mv "$report" "$private/original"
mkdir "$private/collected"

finish() {
    local result=$?
    trap - EXIT INT TERM HUP
    if [[ -n $child ]] && kill -0 "$child" 2>/dev/null; then
        kill -TERM -- "-$child" 2>/dev/null || true
        wait "$child" || true
    fi
    for image in "$inert" "$tools"; do
        if engine image inspect "$image" >/dev/null 2>&1; then
            engine image rm "$image" >/dev/null || result=1
        elif ! engine image ls --all --format '{{.Repository}}:{{.Tag}}' >"$private/images" ||
            grep -Fxq "$image" "$private/images"; then
            result=1
        fi
    done
    if [[ -d $report ]]; then rm -rf -- "$report"; fi
    mv "$private/original" "$report"
    rm -rf -- "$report/controls"
    mkdir -p "$report/controls"
    cp "$private/collected/"*.json "$report/controls/" 2>/dev/null || true
    if ((result == 0)); then rm -rf -- "$private"
    else printf 'Transport control diagnostics retained privately: %s\n' "$private" >&2; fi
    exit "$result"
}
trap finish EXIT
trap 'exit 130' INT
trap 'exit 143' TERM HUP
timeout --signal=TERM --kill-after=10s 5m "$runtime" "${args[@]}" build \
    -t "$tools" -f Dockerfile.transport-security . >"$private/tools.log" 2>&1
printf 'FROM %s\nENTRYPOINT ["/bin/false"]\n' "$tools" >"$private/Dockerfile"
"$runtime" "${args[@]}" build --network=none -t "$inert" -f "$private/Dockerfile" "$private" \
    >"$private/inert.log" 2>&1

mkdir -p "$report"
printf '{"passed":true}\n' >"$report/framing.json"
if HTTPS_SECURITY_TOOLS_IMAGE="$inert" bash scripts/https_security_test.sh full \
    >"$private/failure.log" 2>&1; then
    echo 'Transport suite accepted a failed scanner' >&2; exit 1
fi
cp "$report/summary.json" "$private/collected/failed-scanner.json"
if [[ -f $report/browser.json ]]; then
    cp "$report/browser.json" "$private/collected/failed-scanner-browser.json"
fi
jq -e '.status=="failed" and .stage=="tls-rsa" and .cleanup_failures==0' "$report/summary.json" >/dev/null
[[ ! -e $report/framing.json ]] || { echo 'Transport suite retained stale evidence' >&2; exit 1; }
rm -rf -- "$report"
mkdir "$report"

for control in cancellation browser-timeout; do
    control_dir=$private/$control
    mkdir "$control_dir"
    force_timeout=0
    [[ $control != browser-timeout ]] || force_timeout=1
    setsid env TMPDIR="$control_dir" HTTPS_SECURITY_TOOLS_IMAGE="$tools" \
        HTTPS_SECURITY_BROWSER_HOLD=1 HTTPS_SECURITY_BROWSER_FORCE_TIMEOUT="$force_timeout" \
        bash scripts/https_security_test.sh quick >"$private/$control.log" 2>&1 &
    child=$!
    connection=
    deadline=$((SECONDS+120))
    while ((SECONDS < deadline)); do
        for candidate in "$control_dir"/durpdeploy-transport.*/stack/connection.json; do
            if [[ -f $candidate ]] && jq -e '.status=="ready"' "$candidate" >/dev/null; then
                connection=$candidate
                break
            fi
        done
        [[ -z $connection ]] || break
        kill -0 "$child" 2>/dev/null || break
        sleep 0.25
    done
    [[ -n $connection ]] || { echo 'Control fixture never became ready' >&2; exit 1; }
    stack=$(jq -r .stack_id "$connection")
    browser_ready=0
    while ((SECONDS < deadline)); do
        if engine exec "$stack-browser" test -f /tmp/browser-ready >/dev/null 2>&1; then
            browser_ready=1; break
        fi
        kill -0 "$child" 2>/dev/null || break
        sleep 0.25
    done
    ((browser_ready)) || { echo 'Control never reached the owned browser' >&2; exit 1; }
    transport_run=$(engine container inspect "$stack-browser" |
        jq -er '.[0].Config.Labels["io.durpdeploy.transport-run"]')
    if [[ $control == cancellation ]]; then kill -TERM -- "-$child"; fi
    result=0
    wait "$child" || result=$?
    child=
    if [[ $control == cancellation ]]; then
        [[ $result == 143 ]] || { echo 'Cancellation lost its termination status' >&2; exit 1; }
    else
        ((result != 0)) || { echo 'Browser timeout passed unexpectedly' >&2; exit 1; }
    fi
    cp "$report/summary.json" "$private/collected/$control.json"
    cp "$report/browser.json" "$private/collected/$control-browser.json"
    jq -e '.status=="failed" and .stage=="browser" and .cleanup_failures==0' "$report/summary.json" >/dev/null
    jq -e '.passed==false and (.exit_status|type)=="number" and
        (.phase|test("^[a-z-]+$")) and (.reason|test("^[a-z-]+$"))' "$report/browser.json" >/dev/null
    if [[ $control == browser-timeout ]]; then
        jq -e '.reason=="go-timeout" and .phase!="runtime"' "$report/browser.json" >/dev/null
        if grep -R -Fq transport-private-browser-canary "$report"; then
            echo 'Browser diagnostics leaked private output' >&2; exit 1
        fi
    fi
    for kind in container volume network; do
        names=$(engine "$kind" ls --quiet --filter "label=io.durpdeploy.dev-stack=$stack")
        [[ -z $names ]] || { echo 'Control leaked owned resources' >&2; exit 1; }
    done
    names=$(engine image ls --quiet --filter "label=io.durpdeploy.transport-run=$transport_run")
    [[ -z $names ]] || { echo 'Control leaked its payload image' >&2; exit 1; }
    rm -rf -- "$report"
    mkdir "$report"
done
jq -n '{failed_scanner_rejected:true,cancellation_preserved:true,browser_timeout_rejected:true,
    browser_ready_before_cancellation:true,browser_diagnostics_redacted:true,
    owned_resources_removed:true,stale_evidence_removed:true}' \
    >"$private/collected/result.json"
echo 'Real scanner failure and cancellation cleanup controls: PASS'
