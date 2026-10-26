#!/usr/bin/env bash
set -euo pipefail
root=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
fixture=$(mktemp -d)
trap 'rm -rf -- "$fixture"' EXIT
mkdir -p "$fixture/scripts" "$fixture/tools"
export DEMO_TEST_STATE="$fixture"

# Run the launcher through server startup without real containers or builds.
# A missing mock runtime socket stops it before pairing and population.
sed "s|/usr/local/libexec/durpdeploy-demo-firewall|$fixture/helper|g" \
    "$root/scripts/demo.sh" >"$fixture/scripts/demo.sh"
cp "$root/scripts/build_server.sh" "$root/scripts/build_metadata.sh" \
    "$root/scripts/https_proxy_config.sh" "$fixture/scripts/"
touch "$fixture/helper"
chmod 0700 "$fixture/helper"
cat >"$fixture/scripts/demo-firewall-watch.sh" <<'WATCH'
#!/usr/bin/env bash
touch "$DEMO_TEST_STATE/firewall-called" "$1/firewall.ready"
WATCH
cat >"$fixture/server" <<'SERVER'
#!/usr/bin/env bash
[[ ${1:-} == admin ]] && exit 0
printf 'http=%s\nagent=%s\nurl=%s\n' "$DURPDEPLOY_ADDR" \
    "$DURPDEPLOY_AGENT_LISTEN_ADDR" "$DURPDEPLOY_URL" >"$DEMO_TEST_STATE/settings"
printf '%s\n' '{"msg":"server starting","addr":"127.0.0.1:12345"}'
exec sleep 300
SERVER
cat >"$fixture/tools/go" <<'GO'
#!/usr/bin/env bash
while (($#)); do
    if [[ $1 == -o ]]; then cp "$DEMO_TEST_STATE/server" "$2"; exit; fi
    shift
done
exit 1
GO
cat >"$fixture/tools/podman" <<'PODMAN'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$DEMO_TEST_STATE/commands"
case "$1" in
    info)
        if [[ ${3:-} == '{{.Host.Security.Rootless}}' ]]; then
            printf '%s\n' "${DEMO_TEST_ROOTLESS:-true}"
        elif [[ ${3:-} == '{{.Host.RemoteSocket.Path}}' ]]; then
            printf '%s\n' "$DEMO_TEST_STATE/missing.sock"
        fi ;;
    port) printf '%s\n' 127.0.0.1:34567 ;;
esac
PODMAN
for tool in make curl sqlite3; do
    printf '#!/usr/bin/env bash\nexit 0\n' >"$fixture/tools/$tool"
done
chmod 0700 "$fixture/server" "$fixture/tools/"*

launch() {
    if PATH="$fixture/tools:$PATH" DURPDEPLOY_CONTAINER_RUNTIME="$2" \
        DEMO_LOCAL_ONLY="$1" bash "$fixture/scripts/demo.sh" \
        >"$fixture/output" 2>&1; then
        echo 'FAIL: mock launcher unexpectedly completed' >&2; exit 1
    fi
}

# Invalid configuration must fail before runtime commands or state creation.
for invalid in invalid ''; do
    launch "$invalid" podman
    grep -q 'DEMO_LOCAL_ONLY must be 0 or 1' "$fixture/output"
    [[ ! -e "$fixture/commands" && ! -e "$fixture/tmp" ]]
done
launch 1 docker
grep -q 'requires rootless Podman' "$fixture/output"
[[ ! -e "$fixture/commands" && ! -e "$fixture/tmp" ]]
DEMO_TEST_ROOTLESS=false launch 1 podman
grep -q 'requires rootless Podman' "$fixture/output"
[[ ! -e "$fixture/tmp" ]]
rm "$fixture/commands"

launch 1 podman
grep -q 'Start the selected local runtime socket' "$fixture/output"
grep -q -- '--publish 127.0.0.1::8443' "$fixture/commands"
grep -Fxq http=127.0.0.1:0 "$fixture/settings"
grep -Eq '^agent=127\.0\.0\.1:[0-9]+$' "$fixture/settings"
grep -Fxq url=https://127.0.0.1:34567 "$fixture/settings"
[[ ! -e "$fixture/firewall-called" ]]
certificate=("$fixture"/tmp/demo.*/tls/cert.pem)
openssl x509 -in "${certificate[0]}" -noout -checkip 127.0.0.1 \
    | grep -q 'does match certificate'

# The normal launcher must still publish to the LAN and call the watcher.
rm -rf "$fixture/tmp"
rm "$fixture/commands"
launch 0 podman
grep -q 'Start the selected local runtime socket' "$fixture/output"
grep -q -- '--publish :8443' "$fixture/commands"
grep -Eq '^agent=0\.0\.0\.0:[0-9]+$' "$fixture/settings"
grep -Fxq url=https://citadel.durp.loc:34567 "$fixture/settings"
[[ -e "$fixture/firewall-called" ]]
printf 'Demo loopback launcher checks: PASS\n'
