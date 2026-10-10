#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
test_dir=$(mktemp -d)
trap 'rm -rf -- "$test_dir"' EXIT
mkdir -p "$test_dir/bin"

# Given: a provider that has both run-owned and unrelated containers.
cat > "$test_dir/bin/docker" <<'ENGINE'
#!/usr/bin/env bash
set -euo pipefail
if [[ ${0##*/} == podman ]]; then
    [[ $1 == --remote && $2 == --url && $3 == "$DOCKER_HOST" ]] || exit 64
    shift 3
else
    [[ $1 == --host && $2 == "$DOCKER_HOST" ]] || {
        echo 'FAIL: cleanup queried the default daemon instead of Testcontainers' >&2
        exit 64
    }
    shift 2
fi
case $1 in
ps)
    session=
    for argument in "$@"; do
        case $argument in
        label=org.testcontainers.sessionId=*) session=${argument#*=*=} ;;
        esac
    done
    [[ $session == durpdeploy-go-test.* && $* == *'label=org.testcontainers=true'* ]] || exit 65
    if [[ ${SCENARIO:-} == list-failure ]]; then exit 43; fi
    if [[ -f $CASE_DIR/resources/$session ]]; then echo "$session"; fi
    ;;
rm)
    [[ $2 == -f && $3 == -v && $4 == durpdeploy-go-test.* && $# == 4 ]] || exit 66
    case ${SCENARIO:-} in
    removal-failure | failure-removal) exit 44 ;;
    esac
    rm "$CASE_DIR/resources/$4"
    echo "$4"
    if [[ ${SCENARIO:-} == reaper-race ]]; then exit 44; fi
    ;;
*) exit 67 ;;
esac
ENGINE

cat > "$test_dir/bin/go" <<'GO'
#!/usr/bin/env bash
set -euo pipefail
if [[ $1 == run ]]; then
    # Testcontainers may discover a rootless endpoint without DOCKER_HOST.
    printf '%s\n' "${DOCKER_HOST:-unix:///fixture/rootless/docker.sock}"
    exit 0
fi
[[ $1 == test && $2 == -count=1 && $3 == ./fixture ]] || exit 68
[[ -n $TESTCONTAINERS_SESSION_ID && $TESTCONTAINERS_SESSION_ID != caller-session ]] || exit 69
printf '%s\n' "$TESTCONTAINERS_SESSION_ID" >> "$CASE_DIR/session"
mkdir -p "$CASE_DIR/resources"
touch "$CASE_DIR/resources/$TESTCONTAINERS_SESSION_ID"
case ${SCENARIO:-} in
failure | timeout | failure-removal) exit 42 ;;
nested)
    OUTER_RUNNER_PID=$PPID SCENARIO=nested-child \
        bash "$RUNNER_PATH" -count=1 ./fixture &
    wait
    ;;
nested-child)
    sleep 300 &
    printf '%s' "$!" > "$CASE_DIR/child"
    kill -TERM "$OUTER_RUNNER_PID"
    wait
    ;;
INT | TERM | HUP)
    # A child must be stopped before resource removal begins.
    sleep 300 &
    printf '%s' "$!" > "$CASE_DIR/child"
    kill -"$SCENARIO" "$PPID"
    wait
    ;;
*) exit 0 ;;
esac
GO
chmod +x "$test_dir/bin/docker" "$test_dir/bin/go"

for scenario in success failure timeout INT TERM HUP nested removal-failure failure-removal reaper-race list-failure; do
    case_dir="$test_dir/$scenario"
    mkdir -p "$case_dir"
    touch "$case_dir/unrelated"
    status=0
    # When: the runner succeeds, fails, or is interrupted.
    PATH="$test_dir/bin:$PATH" CASE_DIR="$case_dir" SCENARIO="$scenario" \
        RUNNER_PATH="$root/scripts/go_test.sh" \
        TESTCONTAINERS_SESSION_ID=caller-session \
        bash "$root/scripts/go_test.sh" -count=1 ./fixture \
        > "$case_dir/stdout" 2> "$case_dir/output" || status=$?
    expected=0
    case $scenario in
    failure | timeout | failure-removal) expected=42 ;;
    INT) expected=130 ;;
    TERM | nested) expected=143 ;;
    HUP) expected=129 ;;
    removal-failure | list-failure) expected=1 ;;
    esac
    # Then: status is preserved, owned resources are removed, others survive.
    if [[ $status != "$expected" ]]; then
        cat "$case_dir/output" >&2
        echo "FAIL: $scenario status=$status expected=$expected" >&2
        exit 1
    fi
    case $scenario in
    removal-failure | failure-removal | list-failure) ;;
    *)
        if compgen -G "$case_dir/resources/*" >/dev/null; then
            echo "FAIL: $scenario leaked" >&2
            exit 1
        fi
        ;;
    esac
    [[ -f $case_dir/unrelated ]] || exit 1
    [[ ! -s $case_dir/stdout ]] || {
        echo "FAIL: cleanup polluted test output ($scenario)" >&2
        exit 1
    }
    if [[ -f $case_dir/child ]] && kill -0 "$(cat "$case_dir/child")" 2>/dev/null; then
        # A killed child may briefly remain as a zombie until init reaps it.
        state=$(ps -o stat= -p "$(cat "$case_dir/child")")
        [[ $state == Z* ]] || { echo "FAIL: child survived $scenario" >&2; exit 1; }
    fi
done

# Given two independent worktrees, each invocation owns a distinct session.
for invocation in first second; do
    mkdir -p "$test_dir/$invocation"
    PATH="$test_dir/bin:$PATH" CASE_DIR="$test_dir/$invocation" SCENARIO=success \
        bash "$root/scripts/go_test.sh" -count=1 ./fixture \
        > "$test_dir/$invocation/output" 2>&1 &
    if [[ $invocation == first ]]; then first_pid=$!; else second_pid=$!; fi
done
wait "$first_pid"
wait "$second_pid"
[[ $(cat "$test_dir/first/session") != "$(cat "$test_dir/second/session")" ]]

# Podman must target the same provider socket used by Testcontainers.
mv "$test_dir/bin/docker" "$test_dir/bin/podman"
# Keep a host Docker CLI from winning provider discovery in this fixture.
for tool in bash dirname mktemp mv rm mkdir touch; do
    ln -s "$(command -v "$tool")" "$test_dir/bin/$tool"
done
mkdir -p "$test_dir/podman"
PATH="$test_dir/bin" CASE_DIR="$test_dir/podman" SCENARIO=success \
    DOCKER_HOST='unix:///tmp/fixture provider.sock' \
    bash "$root/scripts/go_test.sh" -count=1 ./fixture
if compgen -G "$test_dir/podman/resources/*" >/dev/null; then exit 1; fi
echo 'Go test container ownership, failures, interruption and isolation: PASS'
