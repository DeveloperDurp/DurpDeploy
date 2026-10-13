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
    DURPDEPLOY_CONTAINER_RUNTIME=docker \
    DURPDEPLOY_CONTAINER_URL=unix:///configured/runtime.sock \
    DOCKER_HOST=unix:///other/runtime.sock PATH="$test_dir/bin:$PATH" \
    /usr/bin/bash "$root/scripts/test-backup-restore.sh" > "$test_dir/output" 2>&1
grep -q '^make called$' "$test_dir/output"
[[ $(grep -c '^runtime=unix:///configured/runtime.sock provider=unix:///configured/runtime.sock$' "$test_dir/output") == 2 ]]
echo 'Recovery platform rejection and selected runtime: PASS'

for runtime in '' invalid; do
    if RESTORE_TEST_OS=Linux RESTORE_TEST_ARCH=x86_64 \
        DURPDEPLOY_CONTAINER_RUNTIME=$runtime PATH="$test_dir/bin:$PATH" \
        /usr/bin/bash "$root/scripts/test-backup-restore.sh" > "$test_dir/output" 2>&1; then
        echo 'FAIL: unspecified or invalid runtime passed.' >&2
        exit 1
    fi
    grep -q 'set DURPDEPLOY_CONTAINER_RUNTIME' "$test_dir/output"
    if grep -q 'make called' "$test_dir/output"; then
        echo 'FAIL: invalid runtime started the drill.' >&2
        exit 1
    fi
done

cat > "$test_dir/bin/go" <<'GO'
#!/bin/sh
printf '%s\n' "${DOCKER_HOST:-unix:///discovered/runtime.sock}"
GO
cat > "$test_dir/bin/podman" <<'PODMAN'
#!/bin/sh
printf 'provider=%s application=%s\n' "$DOCKER_HOST" "$DURPDEPLOY_CONTAINER_URL" > "$RESTORE_TEST_ENGINE_LOG"
printf '%s\n' "$@" >> "$RESTORE_TEST_ENGINE_LOG"
exit 64
PODMAN
chmod +x "$test_dir/bin/go" "$test_dir/bin/podman"
cp "$test_dir/bin/podman" "$test_dir/bin/docker"

for selection in configured provider discovered; do
    configured='' provider='' expected=unix:///discovered/runtime.sock
    if [[ $selection == configured ]]; then
        configured=unix:///configured/runtime.sock
        provider=unix:///other/runtime.sock
        expected=$configured
    elif [[ $selection == provider ]]; then
        provider=unix:///provider/runtime.sock
        expected=$provider
    fi
    status=0
    DURPDEPLOY_CONTAINER_URL=$configured DOCKER_HOST=$provider \
        DURPDEPLOY_CONTAINER_RUNTIME=podman \
        RESTORE_TEST_ENGINE_LOG="$test_dir/engine" PATH="$test_dir/bin:$PATH" \
        /usr/bin/bash "$root/scripts/test-backup-restore-cleanup.sh" || status=$?
    [[ $status == 64 ]]
    grep -Fxq "provider=$expected application=$expected" "$test_dir/engine"
    grep -Fxq -- '--remote' "$test_dir/engine"
    grep -Fxq -- "--url=$expected" "$test_dir/engine"
done
test_status=0
DURPDEPLOY_CONTAINER_URL='' DOCKER_HOST=unix:///docker/runtime.sock \
    DURPDEPLOY_CONTAINER_RUNTIME=docker \
    RESTORE_TEST_ENGINE_LOG="$test_dir/engine" PATH="$test_dir/bin:$PATH" \
    /usr/bin/bash "$root/scripts/test-backup-restore-cleanup.sh" || test_status=$?
[[ $test_status == 64 ]]
grep -Fxq -- '--host=unix:///docker/runtime.sock' "$test_dir/engine"
if grep -Fxq -- '--remote' "$test_dir/engine"; then
    echo 'FAIL: Docker selection invoked Podman arguments.' >&2
    exit 1
fi
echo 'Cleanup runtime selection and SDK discovery: PASS'
