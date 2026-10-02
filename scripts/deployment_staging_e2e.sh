#!/usr/bin/env bash
# Sourced by e2e_test.sh: public API and session/CSRF web deployment handoff.
deployment_staging_e2e() {
    local project env release variable producer consumer payload code dep path state logs
    local -r json_id='import json,sys; print(json.load(sys.stdin)["id"])'
    local prefix="stage-handoff-$(date +%s)-$$"
    echo "=== Deployment file handoff ==="
    # Given: two container steps explicitly publish and consume a shared file.
    project=$(api_post "{\"name\":\"$prefix\"}" "$BASE/api/v1/projects" | python3 -c "$json_id")
    env=$(api_post "{\"name\":\"$prefix\"}" "$BASE/api/v1/environments" | python3 -c "$json_id")
    variable=$(api_post '{"name":"LIMITED","value":"selected"}' "$BASE/api/v1/projects/$project/variables" | python3 -c "$json_id")
    code=$(api_post_code '{"name":"DURPDEPLOY_STAGE_DIR","value":"/override"}' "$BASE/api/v1/projects/$project/variables")
    [[ "$code" == 422 ]] || { echo "FAIL: API staging override got $code"; return 1; }
    code=$(curl_silent -X POST --data-urlencode 'name=DURPDEPLOY_STAGE_DIR' --data-urlencode 'value=/override' -d "csrf_token=$CSRF" "$BASE/projects/$project/variables")
    [[ "$code" == 422 ]] || { echo "FAIL: web staging override got $code"; return 1; }
    code=$(curl -s -H "Authorization: Bearer $API_TOKEN" -H 'Content-Type: application/json' -X PUT -d '{"name":"DURPDEPLOY_STAGE_DIR","value":"/override"}' -o /dev/null -w '%{http_code}' "$BASE/api/v1/projects/$project/variables/$variable")
    [[ "$code" == 422 ]] || { echo "FAIL: API rename staging override got $code"; return 1; }
    code=$(curl_silent -X PUT --data-urlencode 'name=DURPDEPLOY_STAGE_DIR' --data-urlencode 'value=/override' -d "csrf_token=$CSRF" "$BASE/projects/$project/variables/$variable")
    [[ "$code" == 422 ]] || { echo "FAIL: web rename staging override got $code"; return 1; }
    payload=$(python3 -c 'import json,sys; print(json.dumps({"name":"override","script_body":"exit 0","container_image":sys.argv[1],"variable_names":["DURPDEPLOY_STAGE_DIR"]}))' "$BASH_IMAGE")
    code=$(api_post_code "$payload" "$BASE/api/v1/projects/$project/steps")
    [[ "$code" == 400 ]] || { echo "FAIL: API selected staging override got $code"; return 1; }
    code=$(curl_silent -X POST --data-urlencode 'name=override' --data-urlencode 'script_body=exit 0' --data-urlencode 'variable_names=DURPDEPLOY_STAGE_DIR' -d "container_image=$BASH_IMAGE&csrf_token=$CSRF" "$BASE/projects/$project/steps")
    [[ "$code" == 422 ]] || { echo "FAIL: web selected staging override got $code"; return 1; }
    producer=$(cat <<'SCRIPT'
set -eu
test "$DURPDEPLOY_STAGE_DIR" = /stage
test ! -e /tmp/attempt-marker
touch /tmp/attempt-marker
mkdir -p "$DURPDEPLOY_STAGE_DIR/nested folder"
if ! test -e "$DURPDEPLOY_STAGE_DIR/retry-marker"; then
    test ! -e "$DURPDEPLOY_STAGE_DIR/nested folder/payload"
    printf partial > "$DURPDEPLOY_STAGE_DIR/nested folder/payload"
    touch "$DURPDEPLOY_STAGE_DIR/retry-marker"
    exit 7
fi
printf 'hello\nworld\n' > "$DURPDEPLOY_STAGE_DIR/nested folder/payload.tmp"
mv "$DURPDEPLOY_STAGE_DIR/nested folder/payload.tmp" "$DURPDEPLOY_STAGE_DIR/nested folder/payload"
SCRIPT
)
    consumer=$(cat <<'SCRIPT'
set -eu
test "$DURPDEPLOY_STAGE_DIR" = /stage
test "$LIMITED" = selected
test ! -e /tmp/attempt-marker
file="$DURPDEPLOY_STAGE_DIR/nested folder/payload"
test "$(cat "$file")" = "$(printf 'hello\nworld')"
test "$(wc -c < "$file")" -eq 12
awk '$5 == "/stage" { if ($6 !~ /rw/ || $6 !~ /noexec/ || $6 !~ /nosuid/ || $6 !~ /nodev/) exit 1; found=1 } END { exit !found }' /proc/self/mountinfo
printf 'stage-handoff-content-and-size-ok\n'
SCRIPT
)
    payload=$(python3 -c 'import json,sys; print(json.dumps({"name":"producer","script_body":sys.argv[1],"container_image":sys.argv[2],"max_retries":1}))' "$producer" "$BASH_IMAGE")
    api_post "$payload" "$BASE/api/v1/projects/$project/steps" >/dev/null
    payload=$(python3 -c 'import json,sys; print(json.dumps({"name":"consumer","script_body":sys.argv[1],"container_image":sys.argv[2],"variable_names":["LIMITED"]}))' "$consumer" "$BASH_IMAGE")
    api_post "$payload" "$BASE/api/v1/projects/$project/steps" >/dev/null
    release=$(api_post '{"version":"1"}' "$BASE/api/v1/projects/$project/releases" | python3 -c "$json_id")
    for path in api web; do
        # When: each surface launches a new deployment of the same snapshot.
        if [[ "$path" == api ]]; then
            dep=$(api_post "{\"release_id\":$release,\"environment_id\":$env}" "$BASE/api/v1/projects/$project/deployments" | python3 -c "$json_id")
        else
            dep=$(curl -s -b "$COOKIES" -D - -o /dev/null -X POST -d "release_id=$release&environment_id=$env&csrf_token=$CSRF" "$BASE/projects/$project/deploy" | awk 'tolower($1)=="location:" {gsub("\r", "", $2); sub("/deployments/", "", $2); print $2}')
            [[ "$dep" =~ ^[0-9]+$ ]] || { echo 'FAIL: web staging deployment did not redirect'; return 1; }
        fi
        # Then: retry and handoff succeed, and the next deployment starts empty.
        for _ in {1..150}; do
            state=$(api_get "$BASE/api/v1/deployments/$dep/status" | python3 -c 'import json,sys; print(json.load(sys.stdin)["status"])')
            [[ "$state" =~ ^(succeeded|failed|cancelled|cleanup_unconfirmed)$ ]] && break
            sleep 0.2
        done
        [[ "$state" == succeeded ]] || { echo "FAIL: $path staging deployment status=$state"; return 1; }
        logs=$(curl_body "$BASE/deployments/$dep/logs.txt")
        [[ "$logs" == *stage-handoff-content-and-size-ok* && "$logs" == *retrying* ]] || {
            echo "FAIL: $path staging handoff or retry evidence missing"; return 1;
        }
        echo "  $path file handoff, retry, and deployment isolation: OK"
    done
}

deployment_staging_e2e
