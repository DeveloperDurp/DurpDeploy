#!/usr/bin/env bash
# Isolated transport checks. Accept a mode, never an operator URL or scan target.
set -euo pipefail
umask 077
root=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"
mode=${1:-full}
case $mode in quick|full) ;; *) echo 'Use quick or full' >&2; exit 2 ;; esac
for tool in jq openssl go timeout git sha256sum tar; do command -v "$tool" >/dev/null; done
source_digest() {
    git ls-files -z --cached --others --exclude-standard | sort -z |
        xargs -0 sha256sum --zero | sha256sum | cut -d ' ' -f 1
}
source_sha=$(source_digest)
runtime=${DEV_STACK_RUNTIME:-podman}
if ! command -v "$runtime" >/dev/null; then runtime=docker; fi
case $runtime in podman|docker) ;; *) exit 2 ;; esac
socket=${DEV_STACK_SOCKET:-}
if [[ -z $socket ]]; then
    if [[ $runtime == podman ]]; then socket=${XDG_RUNTIME_DIR:-/run/user/$(id -u)}/podman/podman.sock
    else socket=/var/run/docker.sock; fi
fi
[[ $socket == /* && -S $socket ]] || { echo 'A local runtime socket is required' >&2; exit 2; }
export DEV_STACK_RUNTIME=$runtime DEV_STACK_SOCKET=$socket
unset DOCKER_HOST DOCKER_CONTEXT CONTAINER_HOST CONTAINER_CONNECTION
engine_args=(--host="unix://$socket")
if [[ $runtime == podman ]]; then engine_args=(--remote --url="unix://$socket"); fi
engine() { timeout --signal=TERM --kill-after=5s 60s "$runtime" "${engine_args[@]}" "$@"; }
private=$(mktemp -d "${TMPDIR:-/tmp}/durpdeploy-transport.XXXXXXXXXXXX")
state=$private/stack
run=$(openssl rand -hex 12)
report=$root/artifacts/transport
mkdir -p "$report"
for old in summary publication probes listeners application browser forwarding tls-rsa tls-ecdsa framing; do
    rm -f -- "$report/$old.json"
done
rm -f -- "$report/failed-tests.txt" "$report/screenshots/transport-authenticated-https.png" "$report/screenshots/transport-logout.png"
tools_image=${HTTPS_SECURITY_TOOLS_IMAGE:-localhost/durpdeploy-transport-tools:$run}
payload_image=localhost/durpdeploy-transport-payload:$run
build_tools=1
[[ -z ${HTTPS_SECURITY_TOOLS_IMAGE:-} ]] || build_tools=0
containers=()
stage=setup
stack=
cleanup_failed=0

finish() {
    local result=$? name label diagnostic phase=runtime reason=runtime-or-timeout
    trap - EXIT INT TERM HUP
    for name in "${containers[@]}"; do
        if engine container inspect "$name" >"$private/inspect.json" 2>/dev/null; then
            label=$(jq -r '.[0].Config.Labels["io.durpdeploy.transport-run"]' "$private/inspect.json")
            if [[ $label != "$run" ]] || ! engine rm -f "$name" >/dev/null; then cleanup_failed=1; fi
        elif ! engine container ls --all --format '{{.Names}}' >"$private/names" || grep -Fxq "$name" "$private/names"; then
            cleanup_failed=1
        fi
    done
    if [[ -f $state/connection.json ]]; then
        bash scripts/dev_stack.sh down "$state" >"$private/cleanup.log" 2>&1 || cleanup_failed=1
    fi
    local images=("$payload_image")
    if ((build_tools)); then images+=("$tools_image"); fi
    for name in "${images[@]}"; do
        if engine image inspect "$name" >/dev/null 2>&1; then
            engine image rm "$name" >/dev/null || cleanup_failed=1
        elif ! engine image ls --all --format '{{.Repository}}:{{.Tag}}' >"$private/images" ||
            grep -Fxq "$name" "$private/images"; then
            cleanup_failed=1
        fi
    done
    local status=passed
    ((result == 0)) || status=failed
    if ((cleanup_failed)); then status=incomplete; result=1; fi
    if [[ $(source_digest) != "$source_sha" ]]; then
        status=incomplete; result=1; stage=source-changed
    fi
    if [[ -f $private/api.log ]]; then
        grep -E '^--- FAIL: TestTransport[A-Za-z0-9_/.-]+ ' "$private/api.log" |
            cut -d ' ' -f 3 >"$report/failed-tests.txt" || true
    fi
    if [[ $stage == browser && $status != passed ]]; then
        # Accept only fixed tokens. Browser logs can contain credentials and URLs.
        diagnostic=$(grep -E '^TRANSPORT_BROWSER_DIAGNOSTIC (chromium-startup|go-test|cdp-connect|login-page|login-submit|session-cookie|authenticated-screenshot|logout-submit|logout-page|logout-screenshot|screenshot-export) (check-failed|go-timeout|interrupted)$' \
            "$private/browser.log" 2>/dev/null | tail -n 1) || true
        if [[ -n $diagnostic ]]; then
            read -r _ phase reason <<<"$diagnostic"
        fi
        jq -n --arg phase "$phase" --arg reason "$reason" --argjson result "$result" \
            '{passed:false,phase:$phase,reason:$reason,exit_status:$result}' >"$report/browser.json"
    fi
    jq -n --arg status "$status" --arg stage "$stage" --arg mode "$mode" \
        --arg commit "$(git rev-parse HEAD)" --argjson cleanup "$cleanup_failed" \
        --arg source "$source_sha" --argjson override "$([[ -n ${HTTPS_SECURITY_APP_IMAGE:-} ]] && echo true || echo false)" \
        '{status:$status,stage:$stage,mode:$mode,commit:$commit,source_sha256:$source,
          app_image_override:$override,cleanup_failures:$cleanup}' >"$report/summary.json"
    if ((cleanup_failed)); then
        printf 'Transport cleanup incomplete. Private recovery record: %s\n' "$state/connection.json" >&2
    else rm -rf -- "$private"; fi
    printf 'Transport %s: %s (stage %s)\n' "$mode" "$status" "$stage"
    exit "$result"
}
trap finish EXIT
trap 'exit 130' INT
trap 'exit 143' TERM HUP

stage=tool-build
if ((build_tools)); then
    timeout --signal=TERM --kill-after=10s 5m "$runtime" "${engine_args[@]}" build \
        -f Dockerfile.transport-security -t "$tools_image" . >"$private/tools.log" 2>&1
fi
CGO_ENABLED=0 go build -o "$private/transportcheck" ./scripts/transportcheck
CGO_ENABLED=0 go test -c -tags=e2e,transportsecurity,packagebrowser \
    -o "$private/transport-browser.test" ./internal/handler/api
chmod 755 "$private/transportcheck" "$private/transport-browser.test"
stage=fixture-startup
DEV_STACK_PROFILE=security DEV_STACK_IMAGE=${HTTPS_SECURITY_APP_IMAGE:-} \
    bash scripts/dev_stack.sh up "$state" >"$private/startup.json" 2>"$private/startup.log"
stack=$(jq -er .stack_id "$state/connection.json")
app=$stack-app
proxy=$stack-caddy
bash scripts/dev_stack.sh status "$state" >"$private/owned.json"
engine network inspect "$stack" | jq -e '.[0] | (.internal // .Internal) == true' >/dev/null
app_ip=$(engine container inspect "$app" | jq -er --arg net "$stack" '.[0].NetworkSettings.Networks[$net].IPAddress')
proxy_ip=$(engine container inspect "$proxy" | jq -er --arg net "$stack" '.[0].NetworkSettings.Networks[$net].IPAddress')
jq -n --arg app "$app_ip" --arg caddy "$proxy_ip" \
    '[{name:"app",ip:$app},{name:"caddy",ip:$caddy}]' >"$private/targets.json"

stage=bootstrap
password=$(openssl rand -hex 24)
engine exec --user=10001 "$app" sh -c '
    DURPDEPLOY_SECRET_KEY=$(cat /data/secret-key); export DURPDEPLOY_SECRET_KEY
    exec /usr/local/bin/container-entrypoint admin create --email "$1" --password "$2"
' bootstrap admin@transport.test "$password" >"$private/bootstrap.log" 2>&1
engine exec --user=10001 "$app" sh -c '
    DURPDEPLOY_SECRET_KEY=$(cat /data/secret-key); export DURPDEPLOY_SECRET_KEY
    exec /usr/local/bin/container-entrypoint tokens create --user admin@transport.test --name transport --scope global --role admin
' >"$private/token" 2>"$private/token.log"
engine exec --user=10001 "$app" cat /var/lib/durpdeploy/agent-identity/identity.crt >"$private/agent-ca.pem"
jq --arg password "$password" --rawfile token "$private/token" --arg ca "$private/agent-ca.pem" \
    '. + {email:"admin@transport.test",password:$password,token:($token|rtrimstr("\n")),agent_ca:$ca}' \
    "$state/connection.json" >"$private/fixture.json"
unset password
cp "$state/tls/ca.pem" "$private/fixture-ca.pem"
chmod 644 "$private/fixture-ca.pem"

# Docker rejects cp into read-only root filesystems, including stopped containers.
# This owned image contains only public trust data and compiled test programs.
stage=payload-build
mkdir "$private/payload"
cp "$private/transportcheck" "$private/transport-browser.test" "$private/fixture-ca.pem" "$private/payload/"
chmod 755 "$private/payload/transportcheck" "$private/payload/transport-browser.test"
chmod 644 "$private/payload/fixture-ca.pem"
printf 'FROM %s\nCOPY transportcheck transport-browser.test fixture-ca.pem /opt/\n' \
    "$tools_image" >"$private/payload/Dockerfile"
timeout --signal=TERM --kill-after=10s 5m "$runtime" "${engine_args[@]}" build \
    --network=none --label "io.durpdeploy.transport-run=$run" \
    -t "$payload_image" "$private/payload" >"$private/payload.log" 2>&1

stage=publication-and-mounts
for service in app caddy; do
    engine container inspect "$stack-$service" >"$private/$service.inspect.json"
    jq -e --arg service "$service" '
        .[0] | (.NetworkSettings.Ports // {}) | to_entries |
        map(select(.value != null and (.value|length)>0)) |
        (length == (if $service=="app" then 1 else 2 end)) and
        all(.[]; (.key == "23187/tcp" and $service=="app" or
                  ((.key=="80/tcp" or .key=="443/tcp") and $service=="caddy")) and
                  all(.value[]; .HostIp=="127.0.0.1"))
    ' "$private/$service.inspect.json" >/dev/null
done
jq -e '.[0] | all(.Mounts[]; .Destination!="/var/run/durpdeploy-runtime.sock") and
    (.Config.Env | index("DURPDEPLOY_EMBEDDED_AGENT_ENABLED=false")!=null)' "$private/app.inspect.json" >/dev/null
printf '{"published_ports":true,"isolated_network":true,"no_app_runtime_socket":true}\n' >"$report/publication.json"

create_check() {
    local name=$1
    local memory=256m temporary=128m pids=128
    shift
    if [[ $name == "$stack-browser" ]]; then memory=1g; temporary=512m; pids=256; fi
    containers+=("$name")
    engine create --interactive --name "$name" --label "io.durpdeploy.transport-run=$run" \
        --label "io.durpdeploy.dev-stack=$stack" \
        --network "$stack" --cap-drop=ALL --security-opt=no-new-privileges --read-only \
        --memory="$memory" --cpus=0.5 --pids-limit="$pids" \
        --tmpfs /tmp:rw,size="$temporary",mode=1777 "$@" >/dev/null
}

stage=tcp-probes
create_check "$stack-probes" --entrypoint=/opt/transportcheck "$payload_image" probes
engine start --attach --interactive "$stack-probes" <"$private/targets.json" >"$report/probes.json"
stage=listener-inventory
printf '[]\n' >"$private/listeners.json"
for service in app caddy; do
    for protocol in tcp tcp6 udp udp6; do
        engine exec --user=10001 "$stack-$service" cat "/proc/net/$protocol" >"$private/sockets"
        jq --rawfile data "$private/sockets" --arg service "$service" --arg protocol "$protocol" \
            '. + [{target:$service,protocol:$protocol,data:$data}]' "$private/listeners.json" >"$private/next.json"
        mv "$private/next.json" "$private/listeners.json"
    done
done
"$private/transportcheck" listeners <"$private/listeners.json" >"$report/listeners.json"

stage=api-web
HTTPS_SECURITY_FIXTURE="$private/fixture.json" DURPDEPLOY_PACKAGE_UI_EVIDENCE="$report/screenshots" bash scripts/go_test.sh \
    -tags=e2e,transportsecurity -count=1 -timeout=3m -run '^TestTransport' ./internal/handler/api \
    >"$private/api.log" 2>&1
printf '{"api_web":true,"tls":true,"cookies":true,"forwarding":true,"agent_rejection":true}\n' >"$report/application.json"

stage=browser
spki=$(openssl x509 -in "$state/tls/cert.pem" -pubkey -noout |
    openssl pkey -pubin -outform DER | openssl dgst -sha256 -binary | openssl base64 -A)
jq --arg url "https://$stack.localhost" '{url:$url,email,password}' \
    "$private/fixture.json" >"$private/browser-fixture.json"
create_check "$stack-browser" --env "TRANSPORT_BROWSER_HOLD=${HTTPS_SECURITY_BROWSER_HOLD:-0}" \
    --env "TRANSPORT_BROWSER_FORCE_TIMEOUT=${HTTPS_SECURITY_BROWSER_FORCE_TIMEOUT:-0}" \
    --entrypoint=/bin/bash "$payload_image" /opt/transport-browser.sh "$proxy_ip" "$stack.localhost" "$spki"
payload_id=$(engine image inspect "$payload_image" | jq -er '.[0] | .Id // .ID')
engine container inspect "$stack-browser" | jq -e --arg net "$stack" --arg id "$payload_id" '
    .[0] | (.Image|ltrimstr("sha256:"))==($id|ltrimstr("sha256:")) and
    (.NetworkSettings.Networks|keys)==[$net] and .HostConfig.NetworkMode!="host" and
    .HostConfig.ReadonlyRootfs==true and .Config.User=="pwuser" and
    all(.Mounts[]; .Type=="tmpfs")
' >/dev/null
timeout --signal=TERM --kill-after=5s 2m "$runtime" "${engine_args[@]}" start --attach --interactive \
    "$stack-browser" <"$private/browser-fixture.json" >"$private/browser.tar" 2>"$private/browser.log"
[[ $(tar -tf "$private/browser.tar" | sort) == $'transport-authenticated-https.png\ntransport-logout.png' ]]
[[ $(tar -tvf "$private/browser.tar" | cut -c1 | sort -u) == - ]]
mkdir -p "$report/screenshots" "$private/browser-evidence"
tar --no-same-owner --no-same-permissions -xf "$private/browser.tar" -C "$private/browser-evidence"
for screenshot in transport-authenticated-https transport-logout; do
    cp "$private/browser-evidence/$screenshot.png" "$report/screenshots/$screenshot.png"
done
printf '{"passed":true,"internal_network":true,"pinned_image":true,"owned_cleanup":true}\n' >"$report/browser.json"

stage=forwarding-peers
source scripts/transport_forwarding.sh
check_forwarding_peers

if [[ $mode == full ]]; then
    for key_type in rsa ecdsa; do
        stage=tls-$key_type
        if [[ $key_type == ecdsa ]]; then
            openssl req -new -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes \
                -subj "/CN=$stack.localhost" -keyout "$private/ec.key" -out "$private/ec.csr" >"$private/ec.log" 2>&1
            openssl x509 -req -sha256 -days 7 -in "$private/ec.csr" -CA "$state/tls/ca.pem" \
                -CAkey "$state/tls/ca-key.pem" -CAcreateserial -extfile "$state/tls/server.ext" \
                -out "$private/ec.crt" >>"$private/ec.log" 2>&1
            engine cp --archive=false "$private/ec.crt" "$proxy:/config/cert.pem"
            engine cp --archive=false "$private/ec.key" "$proxy:/config/key.pem"
            engine exec "$proxy" caddy reload --force --config /config/security.json >"$private/reload.log" 2>&1
            HTTPS_SECURITY_FIXTURE="$private/fixture.json" bash scripts/go_test.sh \
                -tags=e2e,transportsecurity -count=1 -timeout=2m -run '^TestTransportTLSAPIWebE2E$' \
                ./internal/handler/api >"$private/ec-api.log" 2>&1
        fi
        create_check "$stack-tls-$key_type" "$payload_image" --ip "$proxy_ip" "https://$stack.localhost:443"
        timeout --signal=TERM --kill-after=5s 5m "$runtime" "${engine_args[@]}" start --attach \
            "$stack-tls-$key_type" >"$private/tls-$key_type.json" 2>"$private/tls-$key_type.log"
        bash scripts/transport_tls_policy.sh "$private/tls-$key_type.json" >"$report/tls-$key_type.json"
    done
fi
stage=framing-headers
if HTTPS_SECURITY_FIXTURE="$private/fixture.json" bash scripts/go_test.sh \
    -tags=e2e,transportsecurity,framingsecurity -count=1 -timeout=1m \
    -run '^TestTransportFramingHeadersE2E$' ./internal/handler/api >"$private/framing.log" 2>&1; then
    printf '{"passed":true}\n' >"$report/framing.json"
else
    printf '{"passed":false,"check":"framing-headers"}\n' >"$report/framing.json"
    exit 1
fi
stage=complete
