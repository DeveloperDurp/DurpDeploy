#!/usr/bin/env bash
# Share one Chromium process while each test owns an isolated browser context.
set -euo pipefail
engine=${DURPDEPLOY_CONTAINER_RUNTIME:-docker}
# Private-process fixtures still use a container and need a separate pull budget.
case ${1:?browser shard is required} in
  browser-navigation|browser-permissions)
    bash scripts/ensure_fixture_image.sh \
      harbor.durp.info/microsoft/playwright:v1.61.1-noble "$engine"
    ;;
  *) : ;;
esac
name="durpdeploy-browser-${CI_JOB_ID:-local}-$$"
directory=$(mktemp -d)
browser_pid=
container_started=0
cleanup() {
  local status=$? cleanup_status=0
  trap - EXIT HUP INT TERM
  if [[ -n $browser_pid ]]; then
    # Also stop the child if startup failed before setsid created its group.
    kill -TERM "$browser_pid" 2>/dev/null || true
    kill -TERM -- "-$browser_pid" 2>/dev/null || true
    for _ in {1..30}; do
      kill -0 -- "-$browser_pid" 2>/dev/null || break
      sleep 0.1
    done
    kill -KILL -- "-$browser_pid" 2>/dev/null || true
    wait "$browser_pid" 2>/dev/null || true
    if kill -0 -- "-$browser_pid" 2>/dev/null; then cleanup_status=1; fi
  fi
  if ((container_started)); then
    "$engine" rm --force "$name" >/dev/null || cleanup_status=1
  fi
  rm -rf -- "$directory" || cleanup_status=1
  if ((status == 0 && cleanup_status != 0)); then status=1; fi
  exit "$status"
}
trap cleanup EXIT
trap 'exit 143' HUP INT TERM
executable=$(node -e 'try { process.stdout.write(require("playwright").chromium.executablePath()); } catch { process.exit(1); }' 2>/dev/null) || executable=
if [[ -x $executable ]]; then
  : > "$directory/browser.log"
  setsid "$executable" --headless --no-sandbox --disable-dev-shm-usage \
    --remote-debugging-address=127.0.0.1 --remote-debugging-port=0 \
    "--user-data-dir=$directory/profile" about:blank > "$directory/browser.log" 2>&1 &
  browser_pid=$!
else
  # Local callers without the helper image retain the container runtime.
  "$engine" run --detach --rm --pull=missing --name "$name" \
    --network=host --cap-drop=ALL --security-opt=no-new-privileges \
    --shm-size=256m --entrypoint=/bin/bash \
    harbor.durp.info/microsoft/playwright:v1.61.1-noble -lc \
    'exec /ms-playwright/chromium-*/chrome-linux*/chrome --headless --no-sandbox --disable-dev-shm-usage --remote-debugging-address=127.0.0.1 --remote-debugging-port=0 --user-data-dir=/tmp/package-browser about:blank' >/dev/null
  container_started=1
fi
export DURPDEPLOY_TEST_BROWSER_ENDPOINT=
for _ in {1..60}; do
  if ((container_started)); then
    "$engine" logs "$name" > "$directory/browser.log" 2>&1
  elif ! kill -0 "$browser_pid" 2>/dev/null; then
    cat "$directory/browser.log" >&2
    echo 'Bundled Chromium exited before startup.' >&2
    exit 1
  fi
  DURPDEPLOY_TEST_BROWSER_ENDPOINT=$(sed -n 's/^DevTools listening on //p' "$directory/browser.log")
  if [[ -n $DURPDEPLOY_TEST_BROWSER_ENDPOINT ]]; then break; fi
  sleep 1
done
[[ -n $DURPDEPLOY_TEST_BROWSER_ENDPOINT ]] || {
  echo 'Shared test browser did not become ready' >&2
  exit 1
}
if [[ $1 == -- ]]; then
  shift
  (($#)) || { echo 'A browser command is required.' >&2; exit 2; }
  "$@"
else
  bash scripts/ci_test_shard.sh "$1"
fi
