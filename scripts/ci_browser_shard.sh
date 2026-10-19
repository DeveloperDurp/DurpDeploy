#!/usr/bin/env bash
# Share one Chromium process while each test owns an isolated browser context.
set -euo pipefail
engine=${DURPDEPLOY_CONTAINER_RUNTIME:-docker}
name="durpdeploy-browser-${CI_JOB_ID:-local}-$$"
cleanup() { "$engine" rm --force "$name" >/dev/null; }
trap cleanup EXIT
trap 'exit 143' HUP INT TERM
"$engine" run --detach --rm --pull=missing --name "$name" \
  --network=host --cap-drop=ALL --security-opt=no-new-privileges \
  --shm-size=256m --entrypoint=/bin/bash \
  harbor.durp.info/microsoft/playwright:v1.61.1-noble -lc \
  'exec /ms-playwright/chromium-*/chrome-linux*/chrome --headless --no-sandbox --disable-dev-shm-usage --remote-debugging-address=127.0.0.1 --remote-debugging-port=0 --user-data-dir=/tmp/package-browser about:blank' >/dev/null
export DURPDEPLOY_TEST_BROWSER_ENDPOINT=
for _ in {1..60}; do
  DURPDEPLOY_TEST_BROWSER_ENDPOINT=$("$engine" logs "$name" 2>&1 |
    sed -n 's/^DevTools listening on //p')
  if [[ -n $DURPDEPLOY_TEST_BROWSER_ENDPOINT ]]; then break; fi
  sleep 1
done
[[ -n $DURPDEPLOY_TEST_BROWSER_ENDPOINT ]] || {
  echo 'Shared test browser did not become ready' >&2
  exit 1
}
bash scripts/ci_test_shard.sh "${1:?browser shard is required}"
