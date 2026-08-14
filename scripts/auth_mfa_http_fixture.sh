#!/usr/bin/env bash
# Source this from an auth/MFA scenario. The caller owns app startup and its
# process trap; this helper only owns per-scenario HTTP files and diagnostics.

AUTH_MFA_HTTP_FIXTURE_DIR=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
AUTH_MFA_HTTP_FIXTURE_ROOT=$(CDPATH= cd -- "$AUTH_MFA_HTTP_FIXTURE_DIR/.." && pwd)

if ! declare -p AUTH_MFA_USER_EMAILS >/dev/null 2>&1; then
    declare -gA AUTH_MFA_USER_EMAILS=()
    declare -gA AUTH_MFA_USER_IDS=()
    declare -gA AUTH_MFA_USER_PASSWORDS=()
fi

auth_mfa_fixture_init() {
    if (( $# < 2 || $# > 4 )); then
        echo "usage: auth_mfa_fixture_init SCENARIO BASE_URL [DB_QUERY] [SERVER_LOG]" >&2
        return 2
    fi

    local scenario=$1
    local base=${2%/}

    if [[ ! "$scenario" =~ ^[a-z0-9]+([a-z0-9-]*[a-z0-9])?$ ]]; then
        echo "auth/MFA fixture: scenario ID must be lowercase kebab-case" >&2
        return 2
    fi
    if [[ ! "$base" =~ ^https?://[^/]+([/].*)?$ ]]; then
        echo "auth/MFA fixture: base URL must be HTTP(S)" >&2
        return 2
    fi
    if ! command -v curl >/dev/null 2>&1 || ! command -v python3 >/dev/null 2>&1; then
        echo "auth/MFA fixture: curl and python3 are required" >&2
        return 2
    fi
    if [[ -n "${AUTH_MFA_FIXTURE_TMP:-}" && -d "$AUTH_MFA_FIXTURE_TMP" ]]; then
        echo "auth/MFA fixture: cleanup the active scenario before reinitializing" >&2
        return 2
    fi

    AUTH_MFA_SCENARIO=$scenario
    AUTH_MFA_BASE_URL=$base
    AUTH_MFA_DB_QUERY=${3:-}
    AUTH_MFA_SERVER_LOG=${4:-}
    AUTH_MFA_ENGINE=${AUTH_MFA_ENGINE:-sqlite}
    AUTH_MFA_HTTP_TIMEOUT_SECONDS=${AUTH_MFA_HTTP_TIMEOUT_SECONDS:-15}
    if [[ ! "$AUTH_MFA_HTTP_TIMEOUT_SECONDS" =~ ^[1-9][0-9]*$ ]]; then
        echo "auth/MFA fixture: HTTP timeout must be a positive integer" >&2
        return 2
    fi

    AUTH_MFA_FIXTURE_TMP=$(mktemp -d "${TMPDIR:-/tmp}/durpdeploy-auth-mfa-${scenario}.XXXXXX") || return 1
    chmod 700 "$AUTH_MFA_FIXTURE_TMP"
    AUTH_MFA_SECRET_FILE="$AUTH_MFA_FIXTURE_TMP/secrets"
    AUTH_MFA_REDACTOR="$AUTH_MFA_FIXTURE_TMP/redact.py"
    AUTH_MFA_HTTP_HEADERS="$AUTH_MFA_FIXTURE_TMP/response.headers"
    AUTH_MFA_HTTP_BODY="$AUTH_MFA_FIXTURE_TMP/response.body"
    AUTH_MFA_CURL_STDERR="$AUTH_MFA_FIXTURE_TMP/curl.stderr"
    AUTH_MFA_ARTIFACT_ROOT=${AUTH_MFA_ARTIFACT_DIR:-"$AUTH_MFA_HTTP_FIXTURE_ROOT/artifacts/auth-mfa"}
    AUTH_MFA_ARTIFACT_PATH="$AUTH_MFA_ARTIFACT_ROOT/$scenario"
    AUTH_MFA_FAILED=0
    AUTH_MFA_CLEANUP_OUTCOME=pending
    AUTH_MFA_FAILURE_MESSAGE=""
    AUTH_MFA_HTTP_STATUS=""
    AUTH_MFA_LAST_REQUEST_METHOD=""
    AUTH_MFA_LAST_REQUEST_PATH=""
    AUTH_MFA_LAST_DB_LABEL=""
    AUTH_MFA_LAST_DB_EXPECTED=""
    AUTH_MFA_LAST_DB_ACTUAL=""
    AUTH_MFA_USER_EMAILS=()
    AUTH_MFA_USER_IDS=()
    AUTH_MFA_USER_PASSWORDS=()

    : > "$AUTH_MFA_SECRET_FILE"
    chmod 600 "$AUTH_MFA_SECRET_FILE"
    auth_mfa_write_redactor || {
        rm -rf -- "$AUTH_MFA_FIXTURE_TMP"
        return 1
    }
}

auth_mfa_write_redactor() {
    cat > "$AUTH_MFA_REDACTOR" <<'PY'
import re
import sys
from pathlib import Path
from urllib.parse import quote, quote_plus

secret_file = Path(sys.argv[1])
source = sys.argv[2]
destination = sys.argv[3]

if source == "-":
    text = sys.stdin.buffer.read().decode("utf-8", "replace")
else:
    text = Path(source).read_bytes().decode("utf-8", "replace")

secrets = [line.rstrip("\n") for line in secret_file.read_text("utf-8").splitlines()]
for secret in sorted({value for value in secrets if value}, key=len, reverse=True):
    for value in {secret, quote(secret, safe=""), quote_plus(secret)}:
        text = text.replace(value, "<redacted>")

text = re.sub(
    r"(?im)^(authorization:\s*bearer\s+)[^\r\n]+",
    r"\1<redacted>",
    text,
)
text = re.sub(r"(?i)\bbearer\s+ddp_pat_[^\s&\"'<>;,]+", "Bearer <redacted>", text)
text = re.sub(r"(?i)\bddp_pat_[A-Za-z0-9_-]+", "ddp_pat_<redacted>", text)
text = re.sub(
    r"(?im)^(set-cookie:\s*[^=]+=)[^;\r\n]+",
    r"\1<redacted>",
    text,
)
text = re.sub(r"(?im)^(cookie:\s*)[^\r\n]+", r"\1<redacted>", text)

keys = (
    "password|passphrase|session(?:_id)?|csrf(?:_token)?|challenge(?:_token|_csrf)?|"
    "recovery(?:_code)?|totp(?:_seed|_code)?|seed|token|authorization|credential(?:_id)?|"
    "webauthn(?:_[a-z_]+)?|passkey|publickey|rawid|clientdatajson|authenticatordata|"
    "signature|userhandle|assertion|blob"
)
text = re.sub(
    rf"(?i)([\"']?(?:{keys})[\w.-]*[\"']?\s*[:=]\s*[\"']?)([^&\s,\"'<>;]+)",
    r"\1<redacted>",
    text,
)
text = re.sub(
    rf"(?is)(name=[\"']?(?:{keys})[\w.-]*[\"']?[^>]*\bvalue=[\"']?)([^\"'>\s]+)",
    r"\1<redacted>",
    text,
)
text = re.sub(
    r"(?is)(id=[\"']totp-manual-key[\"'][^>]*>)[^<]+",
    r"\1<redacted>",
    text,
)
text = re.sub(
    r"(?is)(class=[\"'][^\"']*recovery-code[^\"']*[\"'][^>]*>)[^<]+",
    r"\1<redacted>",
    text,
)

if len(text.encode("utf-8")) > 131072:
    text = text[:131072] + "\n[redacted artifact truncated]\n"

if destination == "-":
    sys.stdout.write(text)
else:
    Path(destination).write_text(text, encoding="utf-8")
PY
}

auth_mfa_fixture_cleanup() {
    local cleanup_status=0
    local tmp=${AUTH_MFA_FIXTURE_TMP:-}

    if [[ -n "$tmp" && -e "$tmp" ]]; then
        rm -rf -- "$tmp" || cleanup_status=1
    fi
    if (( cleanup_status == 0 )); then
        AUTH_MFA_CLEANUP_OUTCOME=complete
    else
        AUTH_MFA_CLEANUP_OUTCOME=failed
    fi
    if [[ ${AUTH_MFA_FAILED:-0} == 1 && -d ${AUTH_MFA_ARTIFACT_PATH:-} ]]; then
        python3 - "$AUTH_MFA_ARTIFACT_PATH/summary.txt" \
            "$AUTH_MFA_CLEANUP_OUTCOME" <<'PY' || true
import sys
from pathlib import Path

summary = Path(sys.argv[1])
if summary.exists():
    lines = [line for line in summary.read_text("utf-8").splitlines()
             if not line.startswith("cleanup=")]
    lines.append(f"cleanup={sys.argv[2]}")
    summary.write_text("\n".join(lines) + "\n", encoding="utf-8")
PY
    fi
    if (( cleanup_status != 0 )); then
        echo "auth/MFA fixture: temporary-file cleanup failed" >&2
        return 1
    fi
    return 0
}

auth_mfa_fixture_active() {
    [[ -n ${AUTH_MFA_FIXTURE_TMP:-} && -d "$AUTH_MFA_FIXTURE_TMP" ]]
}

auth_mfa_secret_add() {
    local secret=${1-}

    [[ -n "$secret" && "$secret" != *$'\n'* ]] || return 0
    grep -Fqx -- "$secret" "$AUTH_MFA_SECRET_FILE" 2>/dev/null ||
        printf '%s\n' "$secret" >> "$AUTH_MFA_SECRET_FILE"
}

auth_mfa_register_payload_secrets() {
    local payload=${1-}

    [[ -n "$payload" ]] || return 0
    local payload_file="$AUTH_MFA_FIXTURE_TMP/request.payload"

    printf '%s' "$payload" > "$payload_file"
    python3 - "$AUTH_MFA_SECRET_FILE" "$payload_file" <<'PY'
import json
import re
import sys
from pathlib import Path
from urllib.parse import parse_qsl

secret_file = sys.argv[1]
payload = Path(sys.argv[2]).read_text("utf-8", "replace")
key_re = re.compile(
    r"password|passphrase|session|csrf|challenge|recovery|totp|seed|token|"
    r"credential|webauthn|passkey|publickey|rawid|clientdata|authenticator|"
    r"signature|userhandle|assertion|blob|code",
    re.I,
)
values = []

def visit(value):
    if isinstance(value, dict):
        for key, item in value.items():
            if key_re.search(str(key)) and isinstance(item, (str, int, float)):
                values.append(str(item))
            visit(item)
    elif isinstance(value, list):
        for item in value:
            visit(item)

try:
    visit(json.loads(payload))
except (TypeError, ValueError):
    pass
for key, value in parse_qsl(payload, keep_blank_values=True):
    if key_re.search(key):
        values.append(value)
for match in re.finditer(
    r'''(?i)(?:password|session|csrf|challenge|recovery|totp|seed|token|credential|webauthn|passkey|code)[\w.-]*\s*[:=]\s*["']?([^&\s,"'<>;]+)''',
    payload,
):
    values.append(match.group(1))

with open(secret_file, "a", encoding="utf-8") as output:
    for value in values:
        if value and "\n" not in value:
            output.write(value + "\n")
PY
}

auth_mfa_register_header_secret() {
    local header=${1-}

    if [[ "$header" =~ ^[Aa]uthorization:[[:space:]]*[Bb]earer[[:space:]]+(.+)$ ]]; then
        auth_mfa_secret_add "${BASH_REMATCH[1]}"
    elif [[ "$header" =~ ^[Xx]-?[Cc][Ss][Rr][Ff]-?[Tt]oken:[[:space:]]*(.+)$ ]]; then
        auth_mfa_secret_add "${BASH_REMATCH[1]}"
    fi
    auth_mfa_register_payload_secrets "$header"
}

auth_mfa_register_cookie_secrets() {
    local jar=${1-}
    local domain flag path secure expires name value

    [[ -f "$jar" ]] || return 0
    while IFS=$'\t' read -r domain flag path secure expires name value; do
        [[ "$domain" == \#* && "$domain" != \#HttpOnly_* ]] && continue
        [[ -n "$name" && -n "$value" ]] && auth_mfa_secret_add "$value"
    done < "$jar"
    return 0
}

auth_mfa_redact_text() {
    local text=${1-}

    if ! printf '%s' "$text" | python3 "$AUTH_MFA_REDACTOR" "$AUTH_MFA_SECRET_FILE" - -; then
        printf '%s' '<redacted diagnostic unavailable>'
    fi
}

auth_mfa_redact_file() {
    local source=$1
    local destination=$2

    python3 "$AUTH_MFA_REDACTOR" "$AUTH_MFA_SECRET_FILE" "$source" "$destination"
}

auth_mfa_artifact_copy() {
    local source=$1
    local destination=$2

    [[ -f "$source" ]] || return 0
    if ! auth_mfa_redact_file "$source" "$destination"; then
        printf '%s\n' '[redacted diagnostic unavailable]' > "$destination"
    fi
}

auth_mfa_write_summary() {
    local directory=$1
    local safe_message safe_path

    safe_message=$(auth_mfa_redact_text "${AUTH_MFA_FAILURE_MESSAGE:-}")
    safe_path=$(auth_mfa_redact_text "${AUTH_MFA_LAST_REQUEST_PATH:-}")
    {
        printf 'scenario_id=%s\n' "$AUTH_MFA_SCENARIO"
        printf 'engine=%s\n' "$AUTH_MFA_ENGINE"
        printf 'base_url=%s\n' "$(auth_mfa_redact_text "$AUTH_MFA_BASE_URL")"
        printf 'command=curl -X %s %s\n' \
            "${AUTH_MFA_LAST_REQUEST_METHOD:-none}" "${safe_path:-none}"
        printf 'request_method=%s\n' "${AUTH_MFA_LAST_REQUEST_METHOD:-none}"
        printf 'request_path=%s\n' "${safe_path:-none}"
        printf 'http_status=%s\n' "${AUTH_MFA_HTTP_STATUS:-none}"
        printf 'db_assertion=%s\n' "${AUTH_MFA_LAST_DB_LABEL:-none}"
        printf 'db_expected=%s\n' "${AUTH_MFA_LAST_DB_EXPECTED:-none}"
        printf 'db_actual=%s\n' "${AUTH_MFA_LAST_DB_ACTUAL:-none}"
        printf 'failure=%s\n' "${safe_message:-unexpected fixture failure}"
        printf 'cleanup=%s\n' "$AUTH_MFA_CLEANUP_OUTCOME"
    } > "$directory/summary.txt"
}

auth_mfa_write_artifact() {
    rm -rf -- "$AUTH_MFA_ARTIFACT_PATH"
    mkdir -p "$AUTH_MFA_ARTIFACT_PATH" || return 1
    chmod 700 "$AUTH_MFA_ARTIFACT_PATH"
    auth_mfa_write_summary "$AUTH_MFA_ARTIFACT_PATH"
    auth_mfa_artifact_copy "$AUTH_MFA_HTTP_HEADERS" "$AUTH_MFA_ARTIFACT_PATH/response.headers"
    auth_mfa_artifact_copy "$AUTH_MFA_HTTP_BODY" "$AUTH_MFA_ARTIFACT_PATH/response.body"
    auth_mfa_artifact_copy "$AUTH_MFA_CURL_STDERR" "$AUTH_MFA_ARTIFACT_PATH/curl.stderr"
    if [[ -n ${AUTH_MFA_SERVER_LOG:-} && -f "$AUTH_MFA_SERVER_LOG" ]]; then
        auth_mfa_artifact_copy "$AUTH_MFA_SERVER_LOG" "$AUTH_MFA_ARTIFACT_PATH/server.log"
    fi
}

auth_mfa_fail() {
    local message=${1:-unexpected fixture failure}
    local safe_message

    AUTH_MFA_FAILED=1
    AUTH_MFA_FAILURE_MESSAGE=$message
    auth_mfa_write_artifact || true
    safe_message=$(auth_mfa_redact_text "$message")
    printf 'FAIL: auth/MFA scenario %s: %s\n' "$AUTH_MFA_SCENARIO" "$safe_message" >&2
    return 1
}

auth_mfa_cookie_jar() {
    local name=$1
    local jar

    auth_mfa_fixture_active || return 1
    if [[ ! "$name" =~ ^[A-Za-z0-9][A-Za-z0-9_-]*$ ]]; then
        auth_mfa_fail "cookie-jar name is invalid"
        return 1
    fi
    jar="$AUTH_MFA_FIXTURE_TMP/cookies-$name"
    : > "$jar"
    chmod 600 "$jar"
    printf '%s\n' "$jar"
}

auth_mfa_http_request() {
    if (( $# < 3 )); then
        auth_mfa_fail "HTTP request requires cookie jar, method, and path"
        return 1
    fi

    local jar=$1
    local method=${2^^}
    local path=$3
    local body=${4-}
    local content_type=${5-}
    local url status header
    local -a curl_args
    local -a extra_headers=()
    local index

    auth_mfa_fixture_active || return 1
    if [[ ! "$method" =~ ^(GET|POST|PUT|PATCH|DELETE)$ || "$path" != /* ]]; then
        auth_mfa_fail "HTTP request method or path is invalid"
        return 1
    fi
    if [[ "$jar" != "-" && ( ! -f "$jar" || ! -r "$jar" || ! -w "$jar" ) ]]; then
        auth_mfa_fail "cookie jar is missing or inaccessible"
        return 1
    fi
    url="$AUTH_MFA_BASE_URL$path"
    AUTH_MFA_LAST_REQUEST_METHOD=$method
    AUTH_MFA_LAST_REQUEST_PATH=$path
    AUTH_MFA_HTTP_STATUS=""
    auth_mfa_register_payload_secrets "$path"
    auth_mfa_register_payload_secrets "$body"

    for ((index = 6; index <= $#; index += 1)); do
        header=${!index}
        extra_headers+=("$header")
        auth_mfa_register_header_secret "$header"
    done
    if [[ "$jar" != "-" ]]; then
        auth_mfa_register_cookie_secrets "$jar"
    fi

    : > "$AUTH_MFA_HTTP_HEADERS"
    : > "$AUTH_MFA_HTTP_BODY"
    : > "$AUTH_MFA_CURL_STDERR"
    curl_args=(
        -sS
        --connect-timeout 3
        --max-time "$AUTH_MFA_HTTP_TIMEOUT_SECONDS"
        -D "$AUTH_MFA_HTTP_HEADERS"
        -o "$AUTH_MFA_HTTP_BODY"
        -w '%{http_code}'
        -X "$method"
    )
    if [[ "$jar" != "-" ]]; then
        curl_args+=(-b "$jar" -c "$jar")
    fi
    if [[ -n "$content_type" ]]; then
        curl_args+=(-H "Content-Type: $content_type")
        auth_mfa_register_header_secret "Content-Type: $content_type"
    fi
    if [[ -n "$body" ]]; then
        curl_args+=(--data-raw "$body")
    fi
    for header in "${extra_headers[@]}"; do
        curl_args+=(-H "$header")
    done

    if ! status=$(curl "${curl_args[@]}" "$url" 2>"$AUTH_MFA_CURL_STDERR"); then
        auth_mfa_fail "HTTP request failed"
        return 1
    fi
    status=${status//$'\r'/}
    if [[ ! "$status" =~ ^[0-9]{3}$ ]]; then
        auth_mfa_fail "HTTP request did not return a status code"
        return 1
    fi
    AUTH_MFA_HTTP_STATUS=$status
    if [[ "$jar" != "-" ]]; then
        auth_mfa_register_cookie_secrets "$jar"
    fi
}

auth_mfa_form_request() {
    if (( $# < 4 )); then
        auth_mfa_fail "form request requires cookie jar, method, path, and form data"
        return 1
    fi

    local jar=$1
    local method=$2
    local path=$3
    local form=$4
    shift 4
    auth_mfa_http_request "$jar" "$method" "$path" "$form" \
        'application/x-www-form-urlencoded' "$@"
}

auth_mfa_api_request() {
    if (( $# < 3 )); then
        auth_mfa_fail "API request requires token, method, and path"
        return 1
    fi

    local token=$1
    local method=$2
    local path=$3
    local body=${4-}
    local content_type=""

    [[ -n "$token" ]] || {
        auth_mfa_fail "API request token is empty"
        return 1
    }
    auth_mfa_secret_add "$token"
    [[ -n "$body" ]] && content_type='application/json'
    auth_mfa_http_request - "$method" "$path" "$body" "$content_type" \
        "Authorization: Bearer $token"
}

_auth_mfa_header_value() {
    local name=$1

    python3 - "$AUTH_MFA_HTTP_HEADERS" "$name" <<'PY'
import sys
from pathlib import Path

path = Path(sys.argv[1])
name = sys.argv[2].lower()
blocks = []
for line in path.read_text("utf-8", "replace").splitlines():
    if line.startswith("HTTP/"):
        blocks.append([])
    elif blocks:
        blocks[-1].append(line)
for block in reversed(blocks):
    for line in block:
        if ":" not in line:
            continue
        key, value = line.split(":", 1)
        if key.lower() == name:
            print(value.strip())
            raise SystemExit(0)
raise SystemExit(1)
PY
}

auth_mfa_assert_status() {
    local expected=$1

    if [[ ! "$expected" =~ ^[0-9]{3}$ ]]; then
        auth_mfa_fail "expected HTTP status is invalid"
        return 1
    fi
    if [[ "$AUTH_MFA_HTTP_STATUS" != "$expected" ]]; then
        auth_mfa_fail "expected HTTP status $expected, got ${AUTH_MFA_HTTP_STATUS:-none}"
        return 1
    fi
}

auth_mfa_assert_header() {
    local name=$1
    local value

    if ! value=$(_auth_mfa_header_value "$name"); then
        auth_mfa_fail "response omitted required header $name"
        return 1
    fi
    auth_mfa_secret_add "$value"
}

auth_mfa_assert_header_contains() {
    local name=$1
    local expected=$2
    local value

    auth_mfa_secret_add "$expected"
    if ! value=$(_auth_mfa_header_value "$name"); then
        auth_mfa_fail "response omitted required header $name"
        return 1
    fi
    auth_mfa_secret_add "$value"
    if [[ "$value" != *"$expected"* ]]; then
        auth_mfa_fail "response header $name did not contain the expected value"
        return 1
    fi
}

auth_mfa_assert_body_contains() {
    local label=$1
    local expected=$2

    auth_mfa_secret_add "$expected"
    if ! grep -Fq -- "$expected" "$AUTH_MFA_HTTP_BODY"; then
        auth_mfa_fail "response body omitted $label"
        return 1
    fi
}

auth_mfa_assert_body_not_contains() {
    local label=$1
    local forbidden=$2

    auth_mfa_secret_add "$forbidden"
    if grep -Fq -- "$forbidden" "$AUTH_MFA_HTTP_BODY"; then
        auth_mfa_fail "response body disclosed $label"
        return 1
    fi
}

auth_mfa_assert_cookie() {
    local jar=$1
    local expected=$2
    local domain flag path secure expires name value

    [[ -f "$jar" ]] || {
        auth_mfa_fail "cookie jar is missing"
        return 1
    }
    while IFS=$'\t' read -r domain flag path secure expires name value; do
        [[ "$domain" == \#* && "$domain" != \#HttpOnly_* ]] && continue
        if [[ "$name" == "$expected" ]]; then
            auth_mfa_secret_add "$value"
            return 0
        fi
    done < "$jar"
    auth_mfa_fail "cookie jar omitted required cookie $expected"
    return 1
}

auth_mfa_assert_db_count() {
    if (( $# != 3 )); then
        auth_mfa_fail "database count assertion requires label, expected count, and query"
        return 1
    fi

    local label=$1
    local expected=$2
    local query=$3
    local actual

    AUTH_MFA_LAST_DB_LABEL=$label
    AUTH_MFA_LAST_DB_EXPECTED=$expected
    AUTH_MFA_LAST_DB_ACTUAL=""
    if [[ ! "$expected" =~ ^[0-9]+$ || -z ${AUTH_MFA_DB_QUERY:-} ]]; then
        auth_mfa_fail "database count assertion is not configured"
        return 1
    fi
    if ! actual=$("$AUTH_MFA_DB_QUERY" "$query" 2>"$AUTH_MFA_FIXTURE_TMP/db.stderr"); then
        auth_mfa_fail "database count query failed for $label"
        return 1
    fi
    actual=${actual//[[:space:]]/}
    if [[ ! "$actual" =~ ^[0-9]+$ ]]; then
        auth_mfa_fail "database count query returned a non-count for $label"
        return 1
    fi
    AUTH_MFA_LAST_DB_ACTUAL=$actual
    if [[ "$actual" != "$expected" ]]; then
        auth_mfa_fail "database count mismatch for $label"
        return 1
    fi
}

auth_mfa_assert_audit_count() {
    if (( $# < 2 || $# > 3 )); then
        auth_mfa_fail "audit assertion requires action, expected count, and optional user ID"
        return 1
    fi

    local action=$1
    local expected=$2
    local user_id=${3-}
    local query="SELECT COUNT(*) FROM audit_log WHERE action = '$action'"

    if [[ ! "$action" =~ ^[a-z0-9_]+$ || ( -n "$user_id" && ! "$user_id" =~ ^[0-9]+$ ) ]]; then
        auth_mfa_fail "audit assertion selector is invalid"
        return 1
    fi
    if [[ -n "$user_id" ]]; then
        query+=" AND user_id = $user_id"
    fi
    auth_mfa_assert_db_count "audit action $action" "$expected" "$query"
}

auth_mfa_urlencode() {
    local value=$1
    local encoded=""
    local char hex
    local index

    for ((index = 0; index < ${#value}; index += 1)); do
        char=${value:index:1}
        case "$char" in
        [a-zA-Z0-9.~_-]) encoded+=$char ;;
        ' ') encoded+='+' ;;
        *)
            printf -v hex '%%%02X' "'$char"
            encoded+=$hex
            ;;
        esac
    done
    printf '%s' "$encoded"
}

auth_mfa_login() {
    local jar=$1
    local email=$2
    local password=$3
    local form

    auth_mfa_secret_add "$password"
    form="email=$(auth_mfa_urlencode "$email")&password=$(auth_mfa_urlencode "$password")"
    auth_mfa_form_request "$jar" POST /login "$form"
}

auth_mfa_csrf_from_cookies() {
    local jar=$1
    local path=${2:-/}
    local token

    auth_mfa_http_request "$jar" GET "$path" || return 1
    if ! token=$(python3 - "$AUTH_MFA_HTTP_BODY" <<'PY'
import re
import sys
from pathlib import Path

body = Path(sys.argv[1]).read_text("utf-8", "replace")
match = re.search(r'<meta\s+name=["\']csrf-token["\']\s+content=["\']([^"\']+)', body)
if match:
    print(match.group(1))
    raise SystemExit(0)
raise SystemExit(1)
PY
); then
        auth_mfa_fail "authenticated page omitted CSRF metadata"
        return 1
    fi
    auth_mfa_secret_add "$token"
    printf '%s\n' "$token"
}

auth_mfa_mint_api_token() {
    local jar=$1
    local csrf=$2
    local name=$3
    local form location token

    auth_mfa_secret_add "$csrf"
    form="name=$(auth_mfa_urlencode "$name")&csrf_token=$(auth_mfa_urlencode "$csrf")"
    auth_mfa_form_request "$jar" POST /settings/tokens "$form" || return 1
    auth_mfa_assert_status 303 || return 1
    if ! location=$(_auth_mfa_header_value Location); then
        auth_mfa_fail "token mint response omitted Location header"
        return 1
    fi
    if ! token=$(printf '%s' "$location" | python3 -c '
import sys
from urllib.parse import parse_qs, unquote, urlparse

location = sys.stdin.read().strip()
print(unquote(parse_qs(urlparse(location).query).get("new_token", [""])[0]))
'); then
        auth_mfa_fail "token mint response had an invalid redirect"
        return 1
    fi
    if [[ ! "$token" =~ ^ddp_pat_.+ ]]; then
        auth_mfa_fail "token mint response omitted bearer token"
        return 1
    fi
    auth_mfa_secret_add "$token"
    printf '%s\n' "$token"
}

auth_mfa_create_user() {
    if (( $# != 5 )); then
        auth_mfa_fail "user creation requires admin token, label, role, email, and password"
        return 1
    fi

    local admin_token=$1
    local label=$2
    local role=$3
    local email=$4
    local password=$5
    local payload user_id

    if [[ ! "$label" =~ ^[A-Za-z0-9][A-Za-z0-9_-]*$ || ! "$role" =~ ^(admin|deployer|viewer)$ ]]; then
        auth_mfa_fail "user creation label or role is invalid"
        return 1
    fi
    auth_mfa_secret_add "$admin_token"
    auth_mfa_secret_add "$password"
    payload=$(printf '%s\0%s\0%s\0%s' "$email" "$label" "$password" "$role" |
        python3 -c '
import json
import sys

email, name, password, role = sys.stdin.buffer.read().split(b"\0")
print(json.dumps({
    "email": email.decode(),
    "name": name.decode(),
    "password": password.decode(),
    "role": role.decode(),
}))
') || {
        auth_mfa_fail "could not encode user creation request"
        return 1
    }
    auth_mfa_api_request "$admin_token" POST /api/v1/admin/users "$payload" || return 1
    auth_mfa_assert_status 201 || return 1
    if ! user_id=$(python3 - "$AUTH_MFA_HTTP_BODY" <<'PY'
import json
import sys
from pathlib import Path

value = json.loads(Path(sys.argv[1]).read_text("utf-8"))
identifier = value.get("id")
if isinstance(identifier, int):
    print(identifier)
    raise SystemExit(0)
raise SystemExit(1)
PY
); then
        auth_mfa_fail "user creation response omitted numeric ID"
        return 1
    fi
    AUTH_MFA_USER_EMAILS["$label"]=$email
    AUTH_MFA_USER_IDS["$label"]=$user_id
    AUTH_MFA_USER_PASSWORDS["$label"]=$password
}

auth_mfa_user_email() {
    local label=$1

    [[ -v "AUTH_MFA_USER_EMAILS[$label]" ]] || {
        auth_mfa_fail "unknown helper user label"
        return 1
    }
    printf '%s\n' "${AUTH_MFA_USER_EMAILS[$label]}"
}

auth_mfa_user_id() {
    local label=$1

    [[ -v "AUTH_MFA_USER_IDS[$label]" ]] || {
        auth_mfa_fail "unknown helper user label"
        return 1
    }
    printf '%s\n' "${AUTH_MFA_USER_IDS[$label]}"
}

auth_mfa_user_password() {
    local label=$1

    [[ -v "AUTH_MFA_USER_PASSWORDS[$label]" ]] || {
        auth_mfa_fail "unknown helper user label"
        return 1
    }
    printf '%s\n' "${AUTH_MFA_USER_PASSWORDS[$label]}"
}
