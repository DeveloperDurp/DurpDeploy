#!/bin/bash -p
# Install root-owned; only this fixed runtime rule may be changed by callers.
set -euo pipefail
readonly PATH=/usr/bin:/bin
export PATH
[[ $EUID == 0 ]] || { echo 'This helper must run through sudo.' >&2; exit 1; }
[[ $# == 2 && ($1 == lease || $1 == release) && $2 =~ ^[0-9]{5}$ ]] || {
    echo 'Usage: demo-firewall-helper lease|release PORT' >&2; exit 2;
}
port=$((10#$2))
(( port >= 32768 && port <= 60999 )) || {
    echo 'Only TCP ports 32768-60999 in the public zone are allowed.' >&2; exit 2;
}
readonly rule="rule port port=\"$port\" protocol=\"tcp\" log prefix=\"durp-demo \" limit value=\"1/m\" accept"
# /run is root-owned. Serialize renewal and release, including across demos.
umask 077
exec 9>/run/durpdeploy-demo-firewall.lock
/usr/bin/flock -x 9
firewall() {
    /usr/bin/env -i PATH=/usr/bin:/bin LC_ALL=C \
        /usr/bin/firewall-cmd --zone=public "$@"
}
# Adding an already-present rule does not refresh its expiry. Remove only our
# tagged rule first; ordinary port/service rules are never touched.
result=0
firewall --remove-rich-rule="$rule" >/dev/null 2>&1 || result=$?
[[ $result == 0 || $result == 12 ]] || {
    echo "Could not remove demo rule (firewalld exit $result)." >&2; exit 1;
}
if [[ $1 == lease ]]; then
    firewall --add-rich-rule="$rule" --timeout=120s >/dev/null
fi
