#!/usr/bin/env bash
# Sourced by the running-server suite. Retain every created example.

check_release_provenance() {
    local project release deployment body deploy_body code original replay headers status
    local json_id='import json,sys; print(json.load(sys.stdin)["id"])'
    local -r json_content_type='Content-Type: application/json' http_code_format='%{http_code}'
    project=$(api_post "{\"name\":\"CI provenance $E2E_RUN_ID\"}" "$BASE/api/v1/projects" | python3 -c "$json_id")
    api_post "{\"name\":\"CI handoff\",\"script_body\":\"printf provenance-once\",\"container_image\":\"$BASH_IMAGE\"}" \
        "$BASE/api/v1/projects/$project/steps" >/dev/null
    body=$(python3 - <<'PY'
import json
print(json.dumps({"version":"ci-v1", "repository_url":"https://git.example.test/team/app.git",
    "commit_sha":"a"*40, "artifact_ref":"registry.example.test/app:v1",
    "ci_run_url":"https://ci.example.test/runs/42"}))
PY
)
    original="$TMP/provenance-original.json"
    replay="$TMP/provenance-replay.json"
    headers="$TMP/provenance-headers.txt"
    code=$(curl -sS -H "Authorization: Bearer $API_TOKEN" -H "$json_content_type" \
        -H "Idempotency-Key: release-$E2E_RUN_ID" --data-binary "$body" \
        -o "$original" -w "$http_code_format" "$BASE/api/v1/projects/$project/releases")
    [[ "$code" == 201 ]] || { echo "FAIL: provenance release=$code"; return 1; }
    release=$(python3 -c "$json_id" < "$original")
    code=$(curl -sS -H "Authorization: Bearer $API_TOKEN" -H "$json_content_type" \
        -H "Idempotency-Key: release-$E2E_RUN_ID" --data-binary "$body" \
        -D "$headers" -o "$replay" -w "$http_code_format" "$BASE/api/v1/projects/$project/releases")
    [[ "$code" == 201 ]] && cmp -s "$original" "$replay" || { echo 'FAIL: release replay changed'; return 1; }
    python3 - "$headers" <<'PY'
import pathlib,sys
assert "idempotency-replayed: true" in pathlib.Path(sys.argv[1]).read_text().lower()
PY
    api_post '{}' "$BASE/api/v1/projects/$project/releases/$release/refresh" \
        | python3 -c 'import json,sys; d=json.load(sys.stdin); assert d["commit_sha"]=="a"*40'
    code=$(curl -sS -H "Authorization: Bearer $API_TOKEN" -H "$json_content_type" \
        -H "Idempotency-Key: release-$E2E_RUN_ID" -d '{"version":"changed"}' \
        -o /dev/null -w "$http_code_format" "$BASE/api/v1/projects/$project/releases")
    [[ "$code" == 409 ]] || { echo "FAIL: idempotency mismatch=$code"; return 1; }
    deploy_body="{\"release_id\":$release,\"environment_id\":$API_ENV_ID}"
    code=$(curl -sS -H "Authorization: Bearer $API_TOKEN" -H "$json_content_type" \
        -H "Idempotency-Key: deployment-$E2E_RUN_ID" --data-binary "$deploy_body" \
        -o "$original" -w "$http_code_format" "$BASE/api/v1/projects/$project/deployments")
    [[ "$code" == 201 ]] || { echo "FAIL: provenance deployment=$code"; return 1; }
    deployment=$(python3 -c "$json_id" < "$original")
    for _ in {1..200}; do
        status=$(api_get "$BASE/api/v1/deployments/$deployment/status" | python3 -c 'import json,sys; print(json.load(sys.stdin)["status"])')
        [[ "$status" =~ ^(succeeded|failed|cancelled|cleanup_unconfirmed)$ ]] && break
        sleep 0.1
    done
    [[ "$status" == succeeded ]] || { echo "FAIL: provenance execution=$status"; return 1; }
    code=$(curl -sS -H "Authorization: Bearer $API_TOKEN" -H "$json_content_type" \
        -H "Idempotency-Key: deployment-$E2E_RUN_ID" --data-binary "$deploy_body" \
        -o "$replay" -w "$http_code_format" "$BASE/api/v1/projects/$project/deployments")
    [[ "$code" == 201 ]] && cmp -s "$original" "$replay" || { echo 'FAIL: deployment replay changed'; return 1; }
    api_get "$BASE/api/v1/deployments/$deployment" \
        | python3 -c 'import json,sys; assert json.load(sys.stdin)["provenance"]["commit_sha"]=="a"*40'
    api_get "$BASE/api/v1/deployments/$deployment/logs" \
        | python3 -c 'import json,sys; assert sum(x["line"]=="provenance-once" for x in json.load(sys.stdin))==1'
    api_get "$BASE/api/v1/projects/$project/deployments" \
        | python3 -c 'import json,sys; assert json.load(sys.stdin)["total"]==1'
    curl_body "$BASE/projects/$project/releases/$release" \
        | python3 -c 'import sys; assert "a"*40 in sys.stdin.read()'
    curl_body "$BASE/deployments/$deployment" \
        | python3 -c 'import sys; assert "a"*40 in sys.stdin.read()'
    echo "  CI provenance release: $BASE/projects/$project/releases/$release"
    echo "  Idempotent deployment: $BASE/deployments/$deployment"
}

echo '=== Release provenance and idempotent CI handoff ==='
check_release_provenance
