#!/usr/bin/env bash
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
test_dir=$(mktemp -d)
trap 'rm -rf -- "$test_dir"' EXIT
mkdir "$test_dir/bin"
cat > "$test_dir/bin/uname" <<'UNAME'
#!/bin/sh
case "$1" in
    -s) printf '%s\n' "$RESTORE_TEST_OS" ;;
    -m) printf '%s\n' "$RESTORE_TEST_ARCH" ;;
    *) exit 64 ;;
esac
UNAME
cat > "$test_dir/bin/make" <<'MAKE'
#!/bin/sh
printf 'make called\n'
MAKE
cat > "$test_dir/bin/bash" <<'BASH'
#!/bin/sh
printf 'runtime=%s provider=%s\n' "$DURPDEPLOY_CONTAINER_URL" "$DOCKER_HOST"
BASH
chmod +x "$test_dir/bin/"*

for platform in Darwin:x86_64 Linux:aarch64; do
    if RESTORE_TEST_OS=${platform%%:*} RESTORE_TEST_ARCH=${platform#*:} \
        PATH="$test_dir/bin:$PATH" \
        /usr/bin/bash "$root/scripts/test-backup-restore.sh" > "$test_dir/output" 2>&1; then
        echo 'FAIL: unsupported recovery platform passed.' >&2
        exit 1
    fi
    grep -q 'requires Linux' "$test_dir/output"
    if grep -q 'make called' "$test_dir/output"; then
        echo 'FAIL: unsupported platform started the drill.' >&2
        exit 1
    fi
done

RESTORE_TEST_OS=Linux RESTORE_TEST_ARCH=x86_64 \
    DURPDEPLOY_CONTAINER_URL=unix:///configured/runtime.sock \
    DOCKER_HOST=unix:///other/runtime.sock PATH="$test_dir/bin:$PATH" \
    /usr/bin/bash "$root/scripts/test-backup-restore.sh" > "$test_dir/output" 2>&1
grep -q '^make called$' "$test_dir/output"
[[ $(grep -c '^runtime=unix:///configured/runtime.sock provider=unix:///configured/runtime.sock$' "$test_dir/output") == 2 ]]
echo 'Recovery platform rejection and selected runtime: PASS'
