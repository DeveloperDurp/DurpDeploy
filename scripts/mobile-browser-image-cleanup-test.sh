#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
test_dir=$(mktemp -d)
trap 'rm -rf "$test_dir"' EXIT
mkdir -p "$test_dir/bin"
cat > "$test_dir/bin/docker" <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "$FAKE_DOCKER_LOG"
if [[ ${FAKE_DOCKER_FAIL:-} == signal && $1 == run ]]; then
  kill -TERM "$PPID"
  exit 143
fi
if [[ $1 == "${FAKE_DOCKER_FAIL:-}" ]]; then
  exit 41
fi
MOCK
chmod +x "$test_dir/bin/docker"

for scenario in success build run image signal supplied supplied-failure; do
  case_dir="$test_dir/$scenario"
  mkdir -p "$case_dir"
  image=""
  fail=""
  case "$scenario" in
    build | run | image | signal) fail=$scenario ;;
    supplied) image=caller-owned:fixture ;;
    supplied-failure) image=caller-owned:fixture; fail=run ;;
  esac
  result=0
  PATH="$test_dir/bin:$PATH" FAKE_DOCKER_LOG="$case_dir/docker.log" \
    FAKE_DOCKER_FAIL="$fail" make --no-print-directory -f "$root/Makefile" \
    -C "$case_dir" mobile-browser-container MOBILE_BROWSER_RUN_ID=fixture \
    MOBILE_BROWSER_IMAGE="$image" > "$case_dir/output" 2>&1 || result=$?
  case "$scenario" in
    build | run | supplied-failure)
      if [[ $result == 0 ]] || ! grep -Fq 'Error 41' "$case_dir/output"; then
        cat "$case_dir/output" >&2
        echo "mobile cleanup: lost original failure ($scenario)" >&2
        exit 1
      fi ;;
    signal)
      if [[ $result == 0 ]] || ! grep -Fq 'Error 143' "$case_dir/output"; then
        cat "$case_dir/output" >&2
        echo 'mobile cleanup: lost termination failure' >&2
        exit 1
      fi ;;
    *)
      if [[ $result != 0 ]]; then
        cat "$case_dir/output" >&2
        exit 1
      fi ;;
  esac
  if [[ -z $image ]]; then
    if ! grep -Fxq 'image rm durpdeploy-mobile-browser:fixture' \
      "$case_dir/docker.log"; then
      echo "mobile cleanup: owned image retained ($scenario)" >&2
      exit 1
    fi
  elif grep -q '^image rm ' "$case_dir/docker.log"; then
    echo "mobile cleanup: caller image removed ($scenario)" >&2
    exit 1
  fi
done

for invocation in first second; do
  case_dir="$test_dir/$invocation"
  mkdir -p "$case_dir"
  PATH="$test_dir/bin:$PATH" FAKE_DOCKER_LOG="$case_dir/docker.log" \
    make --no-print-directory -f "$root/Makefile" -C "$case_dir" \
    mobile-browser-container > "$case_dir/output" 2>&1 &
  if [[ $invocation == first ]]; then first_pid=$!; else second_pid=$!; fi
done
wait "$first_pid"
wait "$second_pid"
first_image=$(sed -n 's/^image rm //p' "$test_dir/first/docker.log")
second_image=$(sed -n 's/^image rm //p' "$test_dir/second/docker.log")
if [[ -z $first_image || -z $second_image || $first_image == "$second_image" ]]; then
  echo 'mobile cleanup: concurrent default image tags collide' >&2
  exit 1
fi
echo 'Mobile browser image ownership and failure cleanup: PASS'
