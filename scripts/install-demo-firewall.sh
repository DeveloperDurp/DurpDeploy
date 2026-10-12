#!/usr/bin/env bash
# Run once as the regular demo user; sudo asks for the installation password.
set -euo pipefail
[[ $EUID != 0 && $# == 0 ]] || {
    echo "Run as your regular account: bash $0" >&2; exit 2;
}
root=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
account=$(id -un)
[[ $account =~ ^[a-zA-Z_][a-zA-Z0-9_-]*$ ]] || {
    echo 'Account name requires manual sudoers configuration.' >&2; exit 2;
}
umask 077
directory=$(mktemp -d)
trap 'rm -rf -- "$directory"' EXIT
printf '%s ALL=(root) NOPASSWD: /usr/local/libexec/durpdeploy-demo-firewall\n' \
    "$account" >"$directory/sudoers"
sudo -v
sudo /usr/sbin/visudo -cf "$directory/sudoers"
sudo /usr/bin/install -d -o root -g root -m 0755 /usr/local/libexec
sudo /usr/bin/install -o root -g root -m 0755 \
    "$root/scripts/demo-firewall-helper.sh" \
    /usr/local/libexec/durpdeploy-demo-firewall
sudo /usr/bin/install -o root -g root -m 0440 \
    "$directory/sudoers" /etc/sudoers.d/durpdeploy-demo
printf 'Installed demo firewall helper for %s. Future make demo runs manage temporary access.\n' "$account"
