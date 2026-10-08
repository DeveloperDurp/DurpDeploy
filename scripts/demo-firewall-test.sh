#!/usr/bin/env bash
set -euo pipefail
root=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf -- "$tmp"' EXIT
script_header='#!/bin/bash\ntmp=%q\n'

# Exercise the helper with an isolated firewall transport and lock. No root
# permissions or real firewall changes are needed for these regressions.
sed -e '/^\[\[ \$EUID == 0 \]\]/d' \
    -e "s|/usr/bin/firewall-cmd|$tmp/firewall|g" \
    -e "s|/run/durpdeploy-demo-firewall.lock|$tmp/lock|g" \
    "$root/scripts/demo-firewall-helper.sh" >"$tmp/helper"
printf "$script_header" "$tmp" >"$tmp/firewall"
cat >>"$tmp/firewall" <<'FIREWALL'
printf '%s\n' "$*" >>"$tmp/calls"
[[ ! -f "$tmp/fail-remove" || $2 != --remove-rich-rule=* ]] || exit 1
[[ ! -f "$tmp/fail-add" || $2 != --add-rich-rule=* ]] || exit 1
case "$2" in
    --remove-rich-rule=*)
        [[ -f "$tmp/rule" ]] || exit 12
        rm "$tmp/rule"
        ;;
    --add-rich-rule=*)
        [[ $3 == --timeout=120s ]] || exit 2
        touch "$tmp/rule"
        ;;
    *) exit 2 ;;
esac
FIREWALL
chmod 0755 "$tmp/firewall"
for arguments in 'lease 80' 'lease 61000' 'lease 32767' 'lease 041757' 'lease 41757 --permanent' 'other 41757' 'lease --permanent'; do
    read -r -a parts <<<"$arguments"
    if bash -p "$tmp/helper" "${parts[@]}" >/dev/null 2>&1; then
        echo "FAIL: helper accepted $arguments" >&2; exit 1
    fi
done
[[ ! -e "$tmp/calls" ]]
bash -p "$tmp/helper" lease 41757
[[ -f "$tmp/rule" ]]
bash -p "$tmp/helper" lease 41757
[[ -f "$tmp/rule" && $(wc -l <"$tmp/calls") == 4 ]]
grep -Fq -- '--zone=public --add-rich-rule=rule port port="41757" protocol="tcp" log prefix="durp-demo " limit value="1/m" accept --timeout=120s' "$tmp/calls"
bash -p "$tmp/helper" release 41757
bash -p "$tmp/helper" release 41757
[[ ! -f "$tmp/rule" ]]
touch "$tmp/fail-remove"
if bash -p "$tmp/helper" lease 41757 >/dev/null 2>&1; then
    echo 'FAIL: helper renewed after an unexpected removal failure' >&2; exit 1
fi
[[ ! -f "$tmp/rule" ]]
rm "$tmp/fail-remove"
touch "$tmp/fail-add"
if bash -p "$tmp/helper" lease 41757 >/dev/null 2>&1; then
    echo 'FAIL: helper ignored an add-rule failure' >&2; exit 1
fi
[[ ! -f "$tmp/rule" ]]
rm "$tmp/fail-add"

mkdir "$tmp/demo"
printf '%s\n' "$tmp/engine" >"$tmp/demo/runtime"
printf '%s\n' durpdeploy-demo-test >"$tmp/demo/container"
printf "$script_header" "$tmp" >"$tmp/engine"
cat >>"$tmp/engine" <<'ENGINE'
case "$*" in
    *'{{.Id}}'*) printf '%064d\n' 1 ;;
    *'{{.State.Running}}'*) [[ -f "$tmp/running" ]] && echo true || echo false ;;
    port*) echo '0.0.0.0:41757' ;;
    *) exit 2 ;;
esac
ENGINE
printf "$script_header" "$tmp" >"$tmp/sudo"
cat >>"$tmp/sudo" <<'SUDO'
[[ $1 == -n && $2 == /usr/local/libexec/durpdeploy-demo-firewall ]] || exit 2
[[ ! -f "$tmp/fail-lease" || $3 != lease ]] || exit 1
bash -p "$tmp/helper" "$3" "$4"
SUDO
printf '#!/bin/bash\nrm -f -- %q\n' "$tmp/running" >"$tmp/sleep"
chmod 0755 "$tmp/engine" "$tmp/sudo" "$tmp/sleep"
touch "$tmp/running"
PATH="$tmp:$PATH" bash "$root/scripts/demo-firewall-watch.sh" "$tmp/demo"
[[ -f "$tmp/demo/firewall.ready" && ! -f "$tmp/rule" ]]
rm "$tmp/demo/firewall.ready"
touch "$tmp/running" "$tmp/fail-lease"
if PATH="$tmp:$PATH" bash "$root/scripts/demo-firewall-watch.sh" "$tmp/demo"; then
    echo 'FAIL: watcher ignored denied sudo authorization' >&2; exit 1
fi
[[ ! -e "$tmp/demo/firewall.ready" && ! -f "$tmp/rule" ]]

# An existing watcher owns its lock; duplicate invocations do not release it.
exec 7>"$tmp/demo/firewall.watch.lock"
flock -n 7
if PATH="$tmp:$PATH" bash "$root/scripts/demo-firewall-watch.sh" "$tmp/demo" >/dev/null 2>&1; then
    echo 'FAIL: duplicate watcher accepted' >&2; exit 1
fi
exec 7>&-

# Validate the actual installer policy with visudo, without installing files.
printf "$script_header" "$tmp" >"$tmp/sudo"
cat >>"$tmp/sudo" <<'INSTALL'
printf '%s\n' "$*" >>"$tmp/install-calls"
if [[ $1 == /usr/sbin/visudo ]]; then
    cp "$3" "$tmp/sudoers"
    /usr/sbin/visudo -cf "$3"
fi
INSTALL
PATH="$tmp:$PATH" bash "$root/scripts/install-demo-firewall.sh"
[[ $(wc -l <"$tmp/install-calls") == 5 ]]
grep -Fxq "$(id -un) ALL=(root) NOPASSWD: /usr/local/libexec/durpdeploy-demo-firewall" "$tmp/sudoers"
grep -Fq -- '-o root -g root -m 0755' "$tmp/install-calls"
grep -Fq -- '-o root -g root -m 0440' "$tmp/install-calls"
printf '#!/bin/bash\necho bad,user\n' >"$tmp/id"
chmod 0755 "$tmp/id"
: >"$tmp/install-calls"
if PATH="$tmp:$PATH" bash "$root/scripts/install-demo-firewall.sh" >/dev/null 2>&1; then
    echo 'FAIL: installer accepted sudoers metacharacters in account name' >&2; exit 1
fi
[[ ! -s "$tmp/install-calls" ]]
printf 'Demo firewall validation and lifecycle checks: PASS\n'
