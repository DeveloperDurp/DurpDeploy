#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
runner=${RUNNER_PATH_OVERRIDE:-$root/scripts/go_test.sh}
test_dir=$(mktemp -d)
trap 'rm -rf -- "$test_dir"' EXIT
mkdir -p "$test_dir/bin"

# Given: a provider that has both run-owned and unrelated containers.
cat > "$test_dir/cleanup" <<'ENGINE'
#!/usr/bin/env bash
set -euo pipefail
if [[ $1 == --host ]]; then
    if [[ ${SCENARIO:-} == no-provider ]]; then exit 2; fi
    printf '%s\n' "${DOCKER_HOST:-unix:///fixture/rootless/docker.sock}"
    exit 0
fi
if [[ ${SCENARIO:-} == no-provider || ${SCENARIO:-} == provider-lost ]]; then exit 2; fi
for session in "$@"; do
    [[ $session == durpdeploy-go-test.* ]] || exit 65
    if [[ ${SCENARIO:-} == list-failure ]]; then exit 43; fi
    case ${SCENARIO:-} in
    removal-failure | failure-removal) exit 44 ;;
    *) ;;
    esac
    if [[ -f $CASE_DIR/resources/$session ]]; then
        rm "$CASE_DIR/resources/$session"
        echo "$session"
    fi
done
ENGINE

cat > "$test_dir/bin/go" <<'GO'
#!/usr/bin/env bash
set -euo pipefail
if [[ $1 == build && $2 == -o ]]; then
    cp "$FAKE_CLEANUP" "$3"
    exit 0
fi
[[ $1 == test && $2 == -count=1 && $3 == ./fixture ]] || exit 68
[[ -n $TESTCONTAINERS_SESSION_ID && $TESTCONTAINERS_SESSION_ID != caller-session ]] || exit 69
printf '%s\n' "$TESTCONTAINERS_SESSION_ID" >> "$CASE_DIR/session"
mkdir -p "$CASE_DIR/resources"
touch "$CASE_DIR/resources/$TESTCONTAINERS_SESSION_ID"
if [[ ${SCENARIO:-} == no-provider ]]; then
    rm "$CASE_DIR/resources/$TESTCONTAINERS_SESSION_ID"
fi
case ${SCENARIO:-} in
failure | timeout | failure-removal) exit 42 ;;
nested | startup-race)
    child_scenario=nested-child
    if [[ $SCENARIO == startup-race ]]; then child_scenario=startup-child; fi
    OUTER_RUNNER_PID=$PPID SCENARIO="$child_scenario" \
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
cat > "$test_dir/bin/mv" <<'MV'
#!/usr/bin/env bash
set -euo pipefail
if [[ ${SCENARIO:-} == startup-child && $1 == */process-group.tmp ]]; then
    cat "$1" > "$CASE_DIR/child"
    kill -TERM "$OUTER_RUNNER_PID"
    sleep 300
fi
exec "$REAL_MV" "$@"
MV
chmod +x "$test_dir/cleanup" "$test_dir/bin/go"
chmod +x "$test_dir/bin/mv"
export FAKE_CLEANUP="$test_dir/cleanup"
export REAL_MV=$(command -v mv)

for scenario in success failure timeout INT TERM HUP nested startup-race removal-failure failure-removal list-failure no-provider provider-lost; do
    case_dir="$test_dir/$scenario"
    mkdir -p "$case_dir"
    touch "$case_dir/unrelated"
    status=0
    # When: the runner succeeds, fails, or is interrupted.
    PATH="$test_dir/bin:$PATH" CASE_DIR="$case_dir" SCENARIO="$scenario" \
        RUNNER_PATH="$runner" \
        TESTCONTAINERS_SESSION_ID=caller-session \
        bash "$runner" -count=1 ./fixture \
        > "$case_dir/stdout" 2> "$case_dir/output" || status=$?
    case $scenario in
    success | no-provider) expected=0 ;;
    failure | timeout | failure-removal) expected=42 ;;
    INT) expected=130 ;;
    TERM | nested | startup-race) expected=143 ;;
    HUP) expected=129 ;;
    removal-failure | list-failure | provider-lost) expected=1 ;;
    *) echo "FAIL: unexpected scenario $scenario" >&2; exit 1 ;;
    esac
    # Then: status is preserved, owned resources are removed, others survive.
    if [[ $status != "$expected" ]]; then
        cat "$case_dir/output" >&2
        echo "FAIL: $scenario status=$status expected=$expected" >&2
        exit 1
    fi
    case $scenario in
    removal-failure | failure-removal | list-failure | provider-lost) ;;
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
    if [[ $scenario == startup-race ]]; then
        # A child stopped before its startup handshake must exit without
        # running tests, even if its parent was killed before registration.
        for attempt in {1..120}; do
            child=$(cat "$case_dir/child")
            if ! kill -0 "$child" 2>/dev/null; then break; fi
            state=$(ps -o stat= -p "$child" || true)
            if [[ $state == Z* ]]; then break; fi
            sleep 0.05
        done
    fi
    if [[ -f $case_dir/child ]] && kill -0 "$(cat "$case_dir/child")" 2>/dev/null; then
        # A killed child may briefly remain as a zombie until init reaps it.
        state=$(ps -o stat= -p "$(cat "$case_dir/child")")
        if [[ $state != Z* ]]; then
            child=$(cat "$case_dir/child")
            kill -KILL -- "-$child" 2>/dev/null || true
            kill -KILL "$child" 2>/dev/null || true
            echo "FAIL: child survived $scenario" >&2
            exit 1
        fi
    fi
done

# Given two independent worktrees, each invocation owns a distinct session.
for invocation in first second; do
    mkdir -p "$test_dir/$invocation"
    PATH="$test_dir/bin:$PATH" CASE_DIR="$test_dir/$invocation" SCENARIO=success \
        bash "$runner" -count=1 ./fixture \
        > "$test_dir/$invocation/output" 2>&1 &
    if [[ $invocation == first ]]; then first_pid=$!; else second_pid=$!; fi
done
wait "$first_pid"
wait "$second_pid"
[[ $(cat "$test_dir/first/session") != "$(cat "$test_dir/second/session")" ]]

# A healthy provider needs no Docker/Podman CLI or Bash 4 features.
for tool in bash dirname mktemp rm mkdir touch cp find sleep; do
    ln -s "$(command -v "$tool")" "$test_dir/bin/$tool"
done
cat > "$test_dir/bash3-env" <<'BASH3'
unset BASHPID
shopt() {
    case "$*" in *globstar*) return 1 ;; *) ;; esac
    builtin shopt "$@"
}
BASH3
mkdir -p "$test_dir/no-cli"
PATH="$test_dir/bin" CASE_DIR="$test_dir/no-cli" SCENARIO=success \
    BASH_ENV="$test_dir/bash3-env" \
    DOCKER_HOST='unix:///tmp/fixture provider.sock' \
    bash "$runner" -count=1 ./fixture
if compgen -G "$test_dir/no-cli/resources/*" >/dev/null; then exit 1; fi
echo 'Go test container ownership, failures, interruption and isolation: PASS'
