#!/usr/bin/env bash
# Verify explicit binary reuse, fail-closed selection, and local build fallback.
set -euo pipefail
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
work=$(mktemp -d)
trap 'rm -rf -- "$work"' EXIT
mkdir -p "$work/scripts"
cp "$root/scripts/e2e_build_server.sh" "$work/scripts/"
cat > "$work/scripts/build_server.sh" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
[[ $1 == -o ]]
printf '#!/bin/sh\nprintf "local build\\n"\n' > "$2"
chmod 0755 "$2"
SH
helper=$work/scripts/e2e_build_server.sh
env -u DURPDEPLOY_E2E_BINARY bash "$helper" "$work/local"
[[ $("$work/local") == 'local build' ]]
printf '#!/bin/sh\nprintf "reused build\\n"\n' > "$work/source"
chmod 0755 "$work/source"
DURPDEPLOY_E2E_BINARY=$work/source bash "$helper" "$work/reused"
[[ $("$work/reused") == 'reused build' ]]
cmp "$work/source" "$work/reused"
for invalid in "$work/missing" "$work/not-executable"; do
  printf 'not executable\n' > "$work/not-executable"
  if DURPDEPLOY_E2E_BINARY=$invalid bash "$helper" "$work/rejected"; then
    echo 'FAIL: invalid explicit binary fell back to a local build' >&2
    exit 1
  fi
  [[ ! -e $work/rejected ]]
done
printf 'E2E binary reuse and local fallback: PASS\n'
