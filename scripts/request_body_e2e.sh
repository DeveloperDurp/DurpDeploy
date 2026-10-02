#!/usr/bin/env bash
# Sourced by e2e_test.sh after its authenticated API fixture is ready.

echo "=== Request body hardening: API and web ==="
BODY_DIR="$TMP/request-bodies"
mkdir -p "$BODY_DIR"
python3 - "$BODY_DIR" <<'PY'
import json
import pathlib
import sys
import urllib.parse

directory = pathlib.Path(sys.argv[1])
limit = 4 << 20
payload = {"name": "body-limit-api", "script_body": "", "container_image": "alpine:3.20"}
overhead = len(json.dumps(payload, separators=(",", ":")).encode())
payload["script_body"] = "a" * (limit - overhead)
body = json.dumps(payload, separators=(",", ":")).encode()
assert len(body) == limit
(directory / "maximum.json").write_bytes(body)
(directory / "oversized.json").write_bytes(body + b" ")
(directory / "trailing.json").write_text('{"name":"body-rejected"}' + " " * limit)
(directory / "large.form").write_text(urllib.parse.urlencode({
    "name": "body-limit-web", "script_body": "a" * limit, "container_image": "alpine:3.20"
}))
(directory / "oversized.form").write_text("name=body-rejected&padding=" + "a" * (16 << 20))
(directory / "oversized.multipart").write_text(
    '--boundary\r\nContent-Disposition: form-data; name="name"\r\n\r\nbody-rejected\r\n'
    '--boundary\r\nContent-Disposition: form-data; name="padding"\r\n\r\n'
    + "a" * (16 << 20) + '\r\n--boundary--\r\n'
)
PY

BODY_COUNT_BEFORE=$(api_get "$BASE/api/v1/environments" | python3 -c 'import sys,json;print(json.load(sys.stdin)["total"])')
for BODY_JSON in \
    '{"name":"body-rejected","extra":true}' \
    '{"name":"body-rejected"} {}' \
    '{"name":"body-rejected"} trailing' \
    'null'; do
    CODE=$(api_post_code "$BODY_JSON" "$BASE/api/v1/environments")
    [[ "$CODE" == 400 ]] || { echo "FAIL: invalid API body got $CODE"; exit 1; }
    CODE=$(api_post_code "$BODY_JSON" "$BASE/api/v1/tokens")
    [[ "$CODE" == 400 ]] || { echo "FAIL: invalid token body got $CODE"; exit 1; }
done
CODE=$(api_post_code '{"name":"body-rejected","steps":[{"name":"step","script_body":"echo hi","extra":true}]}' \
    "$BASE/api/v1/projects/$API_PROJECT_ID/runbooks")
[[ "$CODE" == 400 ]] || { echo "FAIL: unknown nested field got $CODE"; exit 1; }
CODE=$(api_post_code '{"extra":true}' "$BASE/api/v1/admin/maintenance")
[[ "$CODE" == 400 ]] || { echo "FAIL: control action ignored body, got $CODE"; exit 1; }

for BODY_ENCODING in known chunked; do
    BODY_HEADERS=()
    if [[ "$BODY_ENCODING" == chunked ]]; then
        BODY_HEADERS=(-H 'Transfer-Encoding: chunked')
    fi
    for BODY_FILE in oversized.json trailing.json; do
        CODE=$(curl -sS -H "Authorization: Bearer $API_TOKEN" \
            -H 'Content-Type: application/json' "${BODY_HEADERS[@]}" \
            --data-binary "@$BODY_DIR/$BODY_FILE" -o "$BODY_DIR/error.json" \
            -w '%{http_code}' "$BASE/api/v1/environments")
        [[ "$CODE" == 413 ]] || { echo "FAIL: $BODY_ENCODING oversized API body got $CODE"; exit 1; }
        python3 - "$BODY_DIR/error.json" <<'PY'
import json, sys
assert json.load(open(sys.argv[1])) == {"error": "Request body too large"}
PY
    done
    for BODY_FILE in oversized.form oversized.multipart; do
        BODY_TYPE=application/x-www-form-urlencoded
        if [[ "$BODY_FILE" == oversized.multipart ]]; then
            BODY_TYPE='multipart/form-data; boundary=boundary'
        fi
        CODE=$(curl -sS -b "$COOKIES" -H "X-CSRF-Token: $CSRF" \
            -H "Content-Type: $BODY_TYPE" "${BODY_HEADERS[@]}" \
            --data-binary "@$BODY_DIR/$BODY_FILE" -o /dev/null -w '%{http_code}' \
            "$BASE/environments")
        [[ "$CODE" == 413 ]] || { echo "FAIL: $BODY_ENCODING oversized web body got $CODE"; exit 1; }
    done
done
BODY_COUNT_AFTER=$(api_get "$BASE/api/v1/environments" | python3 -c 'import sys,json;print(json.load(sys.stdin)["total"])')
[[ "$BODY_COUNT_AFTER" == "$BODY_COUNT_BEFORE" ]] || { echo "FAIL: rejected bodies mutated environments"; exit 1; }

CODE=$(curl -sS -H "Authorization: Bearer $API_TOKEN" -H 'Content-Type: application/json' \
    --data-binary "@$BODY_DIR/maximum.json" -o "$BODY_DIR/created.json" -w '%{http_code}' \
    "$BASE/api/v1/templates")
[[ "$CODE" == 201 ]] || { echo "FAIL: maximum-size JSON script got $CODE"; exit 1; }
BODY_TEMPLATE_ID=$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["id"])' "$BODY_DIR/created.json")
api_get "$BASE/api/v1/templates/$BODY_TEMPLATE_ID" > "$BODY_DIR/stored.json"
python3 - "$BODY_DIR/maximum.json" "$BODY_DIR/stored.json" <<'PY'
import json, sys
expected, actual = (json.load(open(path)) for path in sys.argv[1:])
assert actual["script_body"] == expected["script_body"]
PY
CODE=$(curl -sS -H "Authorization: Bearer $API_TOKEN" -X DELETE \
    -o /dev/null -w '%{http_code}' "$BASE/api/v1/templates/$BODY_TEMPLATE_ID")
[[ "$CODE" == 204 ]] || { echo "FAIL: cleanup maximum-size template got $CODE"; exit 1; }

CODE=$(curl -sS -b "$COOKIES" -H "X-CSRF-Token: $CSRF" \
    -H 'Content-Type: application/x-www-form-urlencoded' --data-binary "@$BODY_DIR/large.form" \
    -o /dev/null -w '%{http_code}' "$BASE/templates")
[[ "$CODE" == 303 ]] || { echo "FAIL: large web script form got $CODE"; exit 1; }
BODY_TEMPLATE_ID=$(api_item_id_by_name "$BASE/api/v1/templates" body-limit-web)
api_get "$BASE/api/v1/templates/$BODY_TEMPLATE_ID" > "$BODY_DIR/stored.json"
python3 - "$BODY_DIR/stored.json" <<'PY'
import json, sys
assert len(json.load(open(sys.argv[1]))["script_body"]) == 4 << 20
PY
CODE=$(curl -sS -H "Authorization: Bearer $API_TOKEN" -X DELETE \
    -o /dev/null -w '%{http_code}' "$BASE/api/v1/templates/$BODY_TEMPLATE_ID")
[[ "$CODE" == 204 ]] || { echo "FAIL: cleanup large web template got $CODE"; exit 1; }
CODE=$(curl -sS -b "$COOKIES" -H 'Content-Type: application/json' \
    -d '{"script":"echo hi","extra":true}' -o /dev/null -w '%{http_code}' "$BASE/api/lint")
[[ "$CODE" == 400 ]] || { echo "FAIL: lint accepted unknown field, got $CODE"; exit 1; }
echo "  Strict JSON, size boundaries, rejected mutations, and large scripts: OK"
