#!/usr/bin/env bash
# Private development stacks. State is data, never executable shell input.
set -euo pipefail
umask 077

root=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
fail() { printf 'dev-stack: %s\n' "$*" >&2; exit 1; }
[[ $# == 2 || ( $# == 3 && $1 == logs ) ]] ||
    fail 'Usage: dev_stack.sh up|status|logs|down STATE_DIRECTORY [app|caddy]'
action=$1
case $action in up|status|logs|down) ;; *) fail 'Unknown operation' ;; esac
for tool in jq openssl curl timeout flock realpath; do
    command -v "$tool" >/dev/null || fail "Missing prerequisite: $tool"
done
state=$(realpath -m -- "$2")
[[ $state != *$'\n'* && $state != *','* && $state != *':'* ]] || fail 'Unsupported state path'

if [[ $action == up ]]; then
    runtime=${DEV_STACK_RUNTIME:-podman}
else
    [[ -f $state/connection.json && ! -L $state/connection.json ]] || fail 'Missing connection record'
    runtime=$(jq -er .runtime "$state/connection.json")
fi
case $runtime in podman|docker) ;; *) fail 'Use podman or docker' ;; esac
command -v "$runtime" >/dev/null || fail "Missing runtime: $runtime"
# Use only a local socket. Do not inherit a remote engine/context accidentally.
if [[ $action == up ]]; then
    socket=${DEV_STACK_SOCKET:-}
    if [[ -z $socket ]]; then
        if [[ $runtime == podman ]]; then
            socket=${XDG_RUNTIME_DIR:-/run/user/$(id -u)}/podman/podman.sock
        else
            socket=/var/run/docker.sock
        fi
    fi
else
    socket=$(jq -er .socket "$state/connection.json")
fi
[[ $socket == /* && -S $socket ]] || fail 'Start the selected local runtime socket first'
unset DOCKER_HOST DOCKER_CONTEXT CONTAINER_HOST CONTAINER_CONNECTION
engine_cmd=("$runtime")
if [[ $runtime == podman ]]; then
    engine_cmd+=(--remote "--url=unix://$socket")
else
    engine_cmd+=("--host=unix://$socket")
fi
engine() {
    local budget=60
    if [[ -n ${cleanup_deadline:-} ]]; then
        budget=$((cleanup_deadline - SECONDS))
        ((budget > 0)) || return 124
        ((budget <= 60)) || budget=60
    fi
    timeout --signal=TERM --kill-after=5s "${budget}s" "${engine_cmd[@]}" "$@"
}
engine info >/dev/null
if [[ $runtime == podman ]]; then
    [[ $(engine info --format '{{.Host.Security.Rootless}}') == true ]] || fail 'Podman must be rootless'
fi

record() {
    jq "$@" "$state/connection.json" >"$state/connection.next"
    mv -- "$state/connection.next" "$state/connection.json"
}

# Check both labels and the immutable ID. A reused name is never sufficient.
inspect_owned() {
    local kind=$1 name=$2 info id expected
    info=$(engine "$kind" inspect "$name") || return 1
    jq -e --arg stack "$stack" --arg run "$run" --arg state "$state" \
        --arg owner "${DEV_STACK_OWNER_WORKTREE:-}" '
        .[0] | (.Config.Labels // .Labels // .labels) |
        .["io.durpdeploy.dev-stack"] == $stack and
        .["io.durpdeploy.dev-run"] == $run and
        .["io.durpdeploy.dev-state"] == $state and
        ($owner == "" or .["io.durpdeploy.dev-worktree"] == $owner)
    ' <<<"$info" >/dev/null || { printf 'Refusing foreign resource: %s\n' "$name" >&2; return 1; }
    id=$(jq -er '.[0] | .Id // .ID // .id // .Name // .name' <<<"$info")
    expected=$(jq -r --arg name "$name" '.ids[$name] // empty' "$state/connection.json")
    [[ -z $expected || $id == "$expected" ]] || { printf 'Resource ID changed: %s\n' "$name" >&2; return 1; }
    printf '%s\n' "$info"
}

remember() {
    local kind=$1 name=$2 info id
    info=$(inspect_owned "$kind" "$name")
    id=$(jq -er '.[0] | .Id // .ID // .id // .Name // .name' <<<"$info")
    record --arg name "$name" --arg id "$id" '.ids[$name] = $id'
}

# Return empty output only when a complete successful listing confirms absence.
inspect_present() {
    local kind=$1 name=$2 info names
    if info=$(engine "$kind" inspect "$name" 2>"$state/inspect-error.log"); then
        printf '%s\n' "$info"
        return 0
    fi
    case $kind in
        container) names=$(engine container ls --all --format '{{.Names}}') || return 1 ;;
        volume) names=$(engine volume ls --format '{{.Name}}') || return 1 ;;
        network) names=$(engine network ls --format '{{.Name}}') || return 1 ;;
        image) names=$(engine image ls --all --format '{{.Repository}}:{{.Tag}}') || return 1 ;;
        *) return 1 ;;
    esac
    if [[ $'\n'$names$'\n' == *$'\n'"$name"$'\n'* ]]; then
        printf 'Inspection failed for existing resource: %s\n' "$name" >&2
        return 1
    fi
}

cleanup_resources() {
    local kind name info ids id scope
    cleanup_deadline=$((SECONDS + 180))
    # Verify every surviving named resource before the first mutation.
    while read -r kind name; do
        info=$(inspect_present "$kind" "$name") || return 1
        if [[ -n $info ]]; then
            inspect_owned "$kind" "$name" >/dev/null || return 1
        fi
    done < <(jq -r '.resources[] | [.kind,.name] | @tsv' "$state/connection.json")
    for name in "$app" "$proxy"; do
        info=$(inspect_present container "$name") || return 1
        if [[ -n $info ]]; then
            info=$(inspect_owned container "$name") || return 1
            id=$(jq -er '.[0] | .Id // .ID' <<<"$info")
            engine stop --time=30 "$id" >/dev/null || return 1
            engine rm "$id" >/dev/null || return 1
        fi
    done
    # The server normally drains these. Recover only its exact runner namespace.
    for scope in "$runtime:$stack" "$runtime:$stack:gate-images"; do
        ids=$(engine ps --all --quiet --filter "label=io.durpdeploy.namespace=$scope") || return 1
        for id in $ids; do
            info=$(engine container inspect "$id") || return 1
            [[ $(jq -r '.[0].Config.Labels["io.durpdeploy.namespace"]' <<<"$info") == "$scope" ]] || return 1
            engine rm --force "$id" >/dev/null || return 1
        done
    done
    ids=$(engine volume ls --quiet --filter "label=io.durpdeploy.namespace=$runtime:$stack") || return 1
    for id in $ids; do
        info=$(engine volume inspect "$id") || return 1
        [[ $(jq -r '.[0].Labels["io.durpdeploy.namespace"]' <<<"$info") == "$runtime:$stack" ]] || return 1
        engine volume rm "$id" >/dev/null || return 1
    done
    for name in "$stack-data" "$stack-identity" "$stack-caddy-data" "$stack-caddy-config"; do
        info=$(inspect_present volume "$name") || return 1
        if [[ -n $info ]]; then
            inspect_owned volume "$name" >/dev/null || return 1
            engine volume rm "$name" >/dev/null || return 1
        fi
    done
    info=$(inspect_present network "$stack") || return 1
    if [[ -n $info ]]; then
        info=$(inspect_owned network "$stack") || return 1
        id=$(jq -er '.[0] | .Id // .ID // .id' <<<"$info")
        engine network rm "$id" >/dev/null || return 1
    fi
    info=$(inspect_present image "$image") || return 1
    if [[ -n $info ]]; then
        inspect_owned image "$image" >/dev/null || return 1
        engine image rm --no-prune "$image" >/dev/null || return 1
    fi
    record '.status = "down"'
}

if [[ $action != up ]]; then
    exec 9>"$state/operation.lock"
    flock -n 9 || fail 'Another operation owns this stack'
    jq -e --arg state "$state" '
        .state_dir == $state and .schema == 1 and
        (.stack_id | test("^durpdeploy-dev-[0-9a-f]{24}$")) and
        (.run_id | test("^[0-9a-f]{32}$"))
    ' "$state/connection.json" >/dev/null || fail 'Invalid or copied connection record'
    stack=$(jq -er .stack_id "$state/connection.json")
    run=$(jq -er .run_id "$state/connection.json")
    app=$stack-app
    proxy=$stack-caddy
    image=localhost/$stack:dev
    # Resource names also come from our generated ID, never from editable metadata.
    jq -e --arg stack "$stack" --arg image "$image" '
        .resources == [
            {kind:"container",name:($stack+"-app")},
            {kind:"container",name:($stack+"-caddy")},
            {kind:"volume",name:($stack+"-data")},
            {kind:"volume",name:($stack+"-identity")},
            {kind:"volume",name:($stack+"-caddy-data")},
            {kind:"volume",name:($stack+"-caddy-config")},
            {kind:"network",name:$stack}, {kind:"image",name:$image}]
    ' "$state/connection.json" >/dev/null || fail 'Invalid resource manifest'
    case $action in
        down) cleanup_resources || fail 'Cleanup unconfirmed; state retained'; cat "$state/connection.json" ;;
        logs)
            service=${3:-app}
            case $service in app) name=$app ;; caddy) name=$proxy ;; *) fail 'Use app or caddy' ;; esac
            inspect_owned container "$name" >/dev/null
            engine logs --tail=200 "$name"
            ;;
        status)
            status=down
            if [[ $(jq -r .status "$state/connection.json") != down ]]; then
                status=unavailable
                if info=$(inspect_owned container "$app"); then
                    status=$(jq -r '.[0].State.Status' <<<"$info")
                fi
                inspect_owned container "$proxy" >/dev/null
            fi
            jq --arg status "$status" '. + {container_state:$status}' "$state/connection.json"
            ;;
        *) fail 'Unknown operation' ;;
    esac
    exit
fi

# An existing directory is never replaced, even after a failed launch.
mkdir -p -- "$(dirname -- "$state")"
mkdir -- "$state" || fail 'State directory exists; use a new directory'
exec 9>"$state/operation.lock"
flock -n 9 || fail 'Another operation owns this stack'
stack=durpdeploy-dev-$(openssl rand -hex 12)
run=$(openssl rand -hex 16)
app=$stack-app
proxy=$stack-caddy
image=localhost/$stack:dev
labels=(--label "io.durpdeploy.dev-stack=$stack" --label "io.durpdeploy.dev-run=$run" --label "io.durpdeploy.dev-state=$state")
if [[ -n ${DEV_STACK_OWNER_WORKTREE:-} ]]; then
    labels+=(--label "io.durpdeploy.dev-worktree=$DEV_STACK_OWNER_WORKTREE")
fi
jq -n --arg stack "$stack" --arg run "$run" --arg state "$state" \
    --arg runtime "$runtime" --arg socket "$socket" --arg image "$image" '
    {schema:1,stack_id:$stack,run_id:$run,state_dir:$state,runtime:$runtime,socket:$socket,
     status:"starting",ids:{},url:null,agent_url:null,ca_file:($state+"/tls/ca.pem"),
     resources:[{kind:"container",name:($stack+"-app")},
        {kind:"container",name:($stack+"-caddy")},
        {kind:"volume",name:($stack+"-data")}, {kind:"volume",name:($stack+"-identity")},
        {kind:"volume",name:($stack+"-caddy-data")}, {kind:"volume",name:($stack+"-caddy-config")},
        {kind:"network",name:$stack}, {kind:"image",name:$image}]}
    ' >"$state/connection.json"
startup_cleanup() {
    local result=$?
    trap - EXIT INT TERM HUP
    if ((result != 0)); then
        engine logs "$proxy" >"$state/caddy.log" 2>&1 || true
        engine logs "$app" >"$state/app.log" 2>&1 || true
        if ! cleanup_resources >>"$state/cleanup.log" 2>&1; then
            printf 'Cleanup unconfirmed. Retry down with %s\n' "$state" >&2
        fi
    fi
    exit "$result"
}
trap startup_cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM HUP

source_dir=$(realpath -- "${DEV_STACK_SOURCE:-$root}")
[[ $state != "$source_dir" && $state != "$source_dir/"* ]] || fail 'Keep stack state outside the source worktree'
record --arg source "$source_dir" '.source_dir=$source'
[[ -z ${DEV_STACK_OWNER_WORKTREE:-} || $DEV_STACK_OWNER_WORKTREE == "$source_dir" ]] ||
    fail 'Owner worktree must match the source worktree'
if [[ -n ${DEV_STACK_IMAGE:-} ]]; then
    [[ $DEV_STACK_IMAGE =~ ^[a-zA-Z0-9][a-zA-Z0-9._/:@-]*$ ]] || fail 'Invalid app image reference'
    # Add ownership to a private image layer when CI supplies the scanned app.
    printf 'FROM %s\n' "$DEV_STACK_IMAGE" >"$state/Dockerfile"
    timeout --signal=TERM --kill-after=5s 20m "${engine_cmd[@]}" build \
        --force-rm "${labels[@]}" -t "$image" -f "$state/Dockerfile" "$state" >"$state/build.log" 2>&1
else
    timeout --signal=TERM --kill-after=5s 20m "${engine_cmd[@]}" build \
        --force-rm "${labels[@]}" -t "$image" "$source_dir" >"$state/build.log" 2>&1
fi
remember image "$image"
engine network create "${labels[@]}" "$stack" >/dev/null
remember network "$stack"
for name in "$stack-data" "$stack-identity" "$stack-caddy-data" "$stack-caddy-config"; do
    engine volume create "${labels[@]}" "$name" >/dev/null
    remember volume "$name"
done

mkdir "$state/tls"
host=$stack.localhost
openssl req -x509 -newkey rsa:2048 -sha256 -nodes -days 7 \
    -subj '/CN=DurpDeploy private development CA' \
    -addext 'basicConstraints=critical,CA:TRUE' -addext 'keyUsage=critical,keyCertSign,cRLSign' \
    -keyout "$state/tls/ca-key.pem" -out "$state/tls/ca.pem" >"$state/tls.log" 2>&1
openssl req -new -newkey rsa:2048 -sha256 -nodes -subj "/CN=$host" \
    -keyout "$state/tls/key.pem" -out "$state/tls/server.csr" >>"$state/tls.log" 2>&1
printf 'subjectAltName=DNS:%s\nbasicConstraints=critical,CA:FALSE\nextendedKeyUsage=serverAuth\n' "$host" >"$state/tls/server.ext"
openssl x509 -req -sha256 -days 7 -in "$state/tls/server.csr" \
    -CA "$state/tls/ca.pem" -CAkey "$state/tls/ca-key.pem" -CAcreateserial \
    -extfile "$state/tls/server.ext" -out "$state/tls/cert.pem" >>"$state/tls.log" 2>&1
printf ':8443 {\n tls /config/cert.pem /config/key.pem\n reverse_proxy %s:8080\n}\n' "$app" >"$state/Caddyfile"
engine create --name "$proxy" "${labels[@]}" --network "$stack" \
    --publish 127.0.0.1::8443 --read-only --cap-drop=ALL --cap-add=NET_BIND_SERVICE \
    --security-opt=no-new-privileges --memory=256m --cpus=0.5 --pids-limit=128 \
    --tmpfs /tmp:rw,size=32m --volume "$stack-caddy-data:/data" \
    --volume "$stack-caddy-config:/config" \
    harbor.durp.info/dockerhub/library/caddy:2-alpine \
    caddy run --config /config/Caddyfile --adapter caddyfile >/dev/null
remember container "$proxy"
for file in Caddyfile tls/cert.pem tls/key.pem; do
    engine cp --archive=false "$state/$file" "$proxy:/config/${file##*/}"
done
engine start "$proxy" >/dev/null
port=$(engine port "$proxy" 8443/tcp)
[[ $port =~ ^127\.0\.0\.1:([0-9]+)$ ]] || fail "Unexpected HTTPS binding: $port"
port=${BASH_REMATCH[1]}
url=https://$host:$port
proxy_ip=$(engine container inspect "$proxy" | jq -er --arg network "$stack" '.[0].NetworkSettings.Networks[$network].IPAddress')
[[ $proxy_ip =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]] || fail 'Missing private proxy address'
openssl rand -base64 32 >"$state/secret-key"
socket_options=()
if [[ $runtime == podman ]]; then socket_options=(--security-opt=label=disable); fi
engine create --name "$app" "${labels[@]}" --network "$stack" \
    --publish 127.0.0.1::23187 --read-only --user=0 \
    --cap-drop=ALL --cap-add=SETUID --cap-add=SETGID \
    --security-opt=no-new-privileges "${socket_options[@]}" \
    --memory=512m --cpus=1 --pids-limit=256 --tmpfs /tmp:rw,size=64m,mode=1777 \
    --volume "$stack-data:/data" --volume "$stack-identity:/var/lib/durpdeploy/agent-identity" \
    --volume "$socket:/var/run/durpdeploy-runtime.sock" \
    --env DURPDEPLOY_EXECUTION_BOUNDARY=service --env DURPDEPLOY_ADDR=0.0.0.0:8080 \
    --env DURPDEPLOY_DB=/data/durpdeploy.db --env TMPDIR=/data/tmp \
    --env DURPDEPLOY_ENV_FILE=/nonexistent \
    --env "DURPDEPLOY_URL=$url" --env "DURPDEPLOY_TRUSTED_PROXIES=$proxy_ip/32" \
    --env "DURPDEPLOY_CONTAINER_NAMESPACE=$stack" --env "DURPDEPLOY_CONTAINER_RUNTIME=$runtime" \
    --env DURPDEPLOY_CONTAINER_URL=unix:///var/run/durpdeploy-runtime.sock \
    --env DURPDEPLOY_AGENT_LISTEN_ADDR=0.0.0.0:23187 \
    --env DURPDEPLOY_AGENT_IDENTITY_DIR=/var/lib/durpdeploy/agent-identity \
    --entrypoint /bin/sh "$image" -c '
        runtime_gid=$(stat -c %g /var/run/durpdeploy-runtime.sock)
        exec su-exec "10001:$runtime_gid" /bin/sh -c '\''
        for i in $(seq 1 60); do
            if test -s /var/lib/durpdeploy/agent-identity/public-url; then
                DURPDEPLOY_AGENT_PUBLIC_URL=$(cat /var/lib/durpdeploy/agent-identity/public-url)
                DURPDEPLOY_SECRET_KEY=$(cat /data/secret-key)
                export DURPDEPLOY_AGENT_PUBLIC_URL DURPDEPLOY_SECRET_KEY
                exec /usr/local/bin/container-entrypoint
            fi
            sleep 1
        done
        exit 1'\''' >/dev/null
remember container "$app"
engine start "$app" >/dev/null
# Write as the volume owner. No stopped-container copy or privileged chown is needed.
engine exec --interactive --user=10001 "$app" sh -c \
    'umask 077; cat > /data/secret-key' <"$state/secret-key"
agent_port=$(engine port "$app" 23187/tcp)
[[ $agent_port =~ ^127\.0\.0\.1:([0-9]+)$ ]] || fail 'Unexpected agent binding'
agent_url=https://$host:${BASH_REMATCH[1]}
printf '%s\n' "$agent_url" >"$state/agent-url"
engine exec --interactive --user=10001 "$app" sh -c \
    'umask 077; cat > /var/lib/durpdeploy/agent-identity/public-url' <"$state/agent-url"
record --arg url "$url" --arg agent "$agent_url" '.url=$url | .agent_url=$agent'
deadline=$((SECONDS + 90))
while ((SECONDS < deadline)); do
    if curl --noproxy '*' --resolve "$host:$port:127.0.0.1" --cacert "$state/tls/ca.pem" \
        --silent --fail --max-time 2 "$url/healthz" >/dev/null; then
        record '.status="ready"'
        cat "$state/connection.json"
        exit 0
    fi
    [[ $(engine container inspect "$app" | jq -r '.[0].State.Running') == true ]] || fail 'App exited; inspect its logs'
    sleep 0.5
done
fail 'HTTPS readiness deadline exceeded'
