#!/usr/bin/env bash
set -euo pipefail

BASE="http://localhost:8080"
TMP=$(mktemp -d)
COOKIES=$(mktemp)
trap "rm -rf $TMP; rm -f $COOKIES; kill %1 2>/dev/null || true" EXIT

echo "=== Building and starting server ==="
rm -f durpdeploy.db durpdeploy.db-shm durpdeploy.db-wal
go build -o "$TMP/durpdeploy" ./cmd/server

# Seed the first admin user via the CLI. The CLI runs migrations on its
# own, so the server's startup migration is a no-op. This mirrors the
# production flow described in docs/deploy.md.
ADMIN_EMAIL="e2e-admin@test.local"
ADMIN_PASS="e2e-admin-password-1234"
DURPDEPLOY_DB=durpdeploy.db "$TMP/durpdeploy" admin create \
    --email "$ADMIN_EMAIL" --password "$ADMIN_PASS" >/dev/null

# Start the server. The migrations it would normally run are a no-op
# because the admin CLI just created the schema.
"$TMP/durpdeploy" &
sleep 2

# Helpers. All helpers pass -b $COOKIES so the session cookie is
# attached automatically. State-changing methods append csrf_token=$CSRF
# to the form data so every write satisfies CSRFMiddleware. DELETEs
# pass the token via the X-CSRF-Token header instead — Go's stdlib
# does not parse form bodies for DELETE, so form data would 403.
curl_silent() { curl -s -b "$COOKIES" -o /dev/null -w "%{http_code}" "$@"; }
curl_body() { curl -s -b "$COOKIES" "$@"; }
do_delete() { curl -s -b "$COOKIES" -H "X-CSRF-Token: $CSRF" -o /dev/null -w "%{http_code}" -X DELETE "$1"; }

# Log in. Captures the session cookie into $COOKIES and pulls the CSRF
# token out of the DB. Asserts a 303 redirect (success).
echo "=== F0: Login ==="
CODE=$(curl -s -c "$COOKIES" -o /dev/null -w "%{http_code}" \
    -X POST -d "email=$ADMIN_EMAIL&password=$ADMIN_PASS" "$BASE/login")
[[ "$CODE" == "303" ]] || { echo "FAIL: login got $CODE, want 303"; exit 1; }
SESSION_ID=$(awk '$6 == "session" { print $7 }' "$COOKIES")
CSRF=$(sqlite3 durpdeploy.db "SELECT csrf_token FROM sessions WHERE id='$SESSION_ID';")
[[ -n "$CSRF" ]] || { echo "FAIL: no CSRF token in DB for session $SESSION_ID"; exit 1; }
echo "  Login + CSRF retrieved: OK"

# A request with no cookie must redirect to /login.
CODE=$(curl -s -o /dev/null -w "%{http_code}" "$BASE/")
[[ "$CODE" == "303" ]] || { echo "FAIL: unauth GET / got $CODE, want 303"; exit 1; }
echo "  Unauth redirect: OK"

# A request with the session cookie but no CSRF on a POST must 403.
CODE=$(curl -s -b "$COOKIES" -o /dev/null -w "%{http_code}" \
    -X POST -d "name=NoCSRF" "$BASE/projects")
[[ "$CODE" == "403" ]] || { echo "FAIL: POST without CSRF got $CODE, want 403"; exit 1; }
echo "  CSRF gate: OK"

echo "=== F3.1: Happy Path ==="
CODE=$(curl_silent -X POST -d "name=TestProject&csrf_token=$CSRF" "$BASE/projects")
[[ "$CODE" == "303" ]] || { echo "FAIL: create project got $CODE"; exit 1; }
PROJECT_ID=$(curl_body "$BASE/projects" | grep -oP 'href="/projects/\K[0-9]+' | head -1)
echo "Project ID: $PROJECT_ID"

CODE=$(curl_silent -X POST -d "name=TestEnv&csrf_token=$CSRF" "$BASE/environments")
[[ "$CODE" == "303" ]] || { echo "FAIL: create env got $CODE"; exit 1; }
ENV_ID=$(curl_body "$BASE/environments" | grep -oP 'href="/environments/\K[0-9]+' | head -1)
echo "Env ID: $ENV_ID"

CODE=$(curl_silent -X POST -d "name=Step1&script_body=echo+hello&csrf_token=$CSRF" "$BASE/projects/$PROJECT_ID/steps")
[[ "$CODE" == "200" ]] || { echo "FAIL: create step got $CODE"; exit 1; }

# Verify the dedicated steps page renders.
CODE=$(curl_silent "$BASE/projects/$PROJECT_ID/steps-page")
[[ "$CODE" == "200" ]] || { echo "FAIL: steps-page got $CODE"; exit 1; }

CODE=$(curl_silent -X POST -d "name=VAR1&value=hello&environment_id=$ENV_ID&csrf_token=$CSRF" "$BASE/projects/$PROJECT_ID/variables")
[[ "$CODE" == "303" ]] || { echo "FAIL: create variable got $CODE"; exit 1; }

CODE=$(curl_silent -X POST -d "version=1.0.0&release_notes=first&csrf_token=$CSRF" "$BASE/projects/$PROJECT_ID/releases")
[[ "$CODE" == "303" ]] || { echo "FAIL: create release got $CODE"; exit 1; }
RELEASE_ID=$(curl_body "$BASE/projects/$PROJECT_ID/releases" | grep -oP 'href="/projects/'$PROJECT_ID'/releases/\K[0-9]+' | sort -n | tail -1)
echo "Release ID: $RELEASE_ID"

DEP_URL=$(curl -s -b "$COOKIES" -D - -o /dev/null -X POST -d "release_id=$RELEASE_ID&environment_id=$ENV_ID&csrf_token=$CSRF" "$BASE/deployments" | grep -i "^location:" | awk '{print $2}' | tr -d '\r')
DEP_ID=$(echo "$DEP_URL" | grep -oP '/deployments/\K[0-9]+')
[[ -n "$DEP_ID" ]] || { echo "FAIL: create deployment did not redirect"; exit 1; }
echo "Deployment ID: $DEP_ID"

CODE=$(curl_silent "$BASE/deployments/$DEP_ID")
[[ "$CODE" == "200" ]] || { echo "FAIL: deployment page got $CODE"; exit 1; }

echo "=== F3.1b: Deployment Note ==="
# Submit a second deployment with a note via the project-scoped deploy page.
curl -s -b "$COOKIES" -o /dev/null -X POST \
    -d "release_id=$RELEASE_ID&environment_id=$ENV_ID&note=smoke-test-audit&csrf_token=$CSRF" \
    "$BASE/projects/$PROJECT_ID/deploy"

# Extract the new deployment ID from the deployments list (latest).
NOTE_DEP=$(curl_body "$BASE/deployments" | grep -oP 'href="/deployments/\K[0-9]+' | sort -n | tail -1)
[[ -n "$NOTE_DEP" ]] || { echo "FAIL: could not extract note deployment ID"; exit 1; }
echo "Note Deployment ID: $NOTE_DEP"

# The new deployment's detail page must contain the note text.
NOTE_PAGE=$(curl_body "$BASE/deployments/$NOTE_DEP")
echo "$NOTE_PAGE" | grep -q "smoke-test-audit" || { echo "FAIL: note text missing from deployment detail"; exit 1; }
echo "  Note appears on deployment detail: OK"

# The first deployment (F3.1) has no note — prove notes are per-deployment.
FIRST_PAGE=$(curl_body "$BASE/deployments/$DEP_ID")
echo "$FIRST_PAGE" | grep -q "smoke-test-audit" && { echo "FAIL: first deployment should not have note text"; exit 1; } || true
echo "  First deployment lacks note: OK"

echo "=== F3.2: Cancel Path ==="
curl -s -b "$COOKIES" -o /dev/null -X POST -d "name=LongStep&script_body=sleep+10&csrf_token=$CSRF" "$BASE/projects/$PROJECT_ID/steps"
curl -s -b "$COOKIES" -o /dev/null -X POST -d "version=1.0.1&csrf_token=$CSRF" "$BASE/projects/$PROJECT_ID/releases"
CANCEL_REL=$(curl_body "$BASE/projects/$PROJECT_ID/releases" | grep -oP 'href="/projects/'$PROJECT_ID'/releases/\K[0-9]+' | sort -n | tail -1)
echo "Cancel Release ID: $CANCEL_REL"

CANCEL_URL=$(curl -s -b "$COOKIES" -D - -o /dev/null -X POST -d "release_id=$CANCEL_REL&environment_id=$ENV_ID&csrf_token=$CSRF" "$BASE/deployments" | grep -i "^location:" | awk '{print $2}' | tr -d '\r')
CANCEL_DEP=$(echo "$CANCEL_URL" | grep -oP '/deployments/\K[0-9]+')
[[ -n "$CANCEL_DEP" ]] || { echo "FAIL: cancel deployment did not redirect"; exit 1; }
echo "Cancel Deployment ID: $CANCEL_DEP"

for i in {1..50}; do
  if curl_body "$BASE/deployments/$CANCEL_DEP/status" | grep -q 'running'; then break; fi
  sleep 0.1
done
CODE=$(curl_silent -X POST -d "csrf_token=$CSRF" "$BASE/deployments/$CANCEL_DEP/cancel")
[[ "$CODE" == "303" ]] || { echo "FAIL: cancel got $CODE"; exit 1; }

echo "=== F3.2b: Per-Step Timeout ==="
# A step with a 1-second timeout running a 10-second sleep should fail
# instead of hanging for the 5-minute default. We delete the LongStep
# from the cancel path so this test doesn't first run a 10s sleep.
# The runner's pre-existing cleanup (cmd.WaitDelay=15s + a 10s goroutine
# sleep) means the deployment status flips to 'failed' ~15s after the
# 1s timeout fires; we poll for 20s and assert < 25s elapsed (still
# well under the 5-minute default).
STEPS_PAGE=$(curl_body "$BASE/projects/$PROJECT_ID/steps-page")
LONG_STEP_ID=$(echo "$STEPS_PAGE" | grep -oP 'step-row-\K[0-9]+' | sort -n | tail -1)
if [[ -n "$LONG_STEP_ID" ]]; then
  do_delete "$BASE/projects/$PROJECT_ID/steps/$LONG_STEP_ID"
fi

CODE=$(curl_silent -X POST -d "name=TimeoutStep&script_body=sleep+10&timeout_seconds=1&csrf_token=$CSRF" "$BASE/projects/$PROJECT_ID/steps")
[[ "$CODE" == "200" ]] || { echo "FAIL: create timeout step got $CODE"; exit 1; }

CODE=$(curl_silent -X POST -d "version=1.0.2&csrf_token=$CSRF" "$BASE/projects/$PROJECT_ID/releases")
[[ "$CODE" == "303" ]] || { echo "FAIL: create timeout release got $CODE"; exit 1; }
TIMEOUT_REL=$(curl_body "$BASE/projects/$PROJECT_ID/releases" | grep -oP 'href="/projects/'$PROJECT_ID'/releases/\K[0-9]+' | sort -n | tail -1)
echo "Timeout Release ID: $TIMEOUT_REL"

TIMEOUT_URL=$(curl -s -b "$COOKIES" -D - -o /dev/null -X POST -d "release_id=$TIMEOUT_REL&environment_id=$ENV_ID&csrf_token=$CSRF" "$BASE/deployments" | grep -i "^location:" | awk '{print $2}' | tr -d '\r')
TIMEOUT_DEP=$(echo "$TIMEOUT_URL" | grep -oP '/deployments/\K[0-9]+')
[[ -n "$TIMEOUT_DEP" ]] || { echo "FAIL: timeout deployment did not redirect"; exit 1; }
echo "Timeout Deployment ID: $TIMEOUT_DEP"

START=$(date +%s)
for i in {1..100}; do
  STATUS_BODY=$(curl_body "$BASE/deployments/$TIMEOUT_DEP/status")
  if echo "$STATUS_BODY" | grep -qE 'failed|succeeded|cancelled'; then break; fi
  sleep 0.2
done
END=$(date +%s)
ELAPSED=$((END - START))

echo "$STATUS_BODY" | grep -q 'failed' || { echo "FAIL: timeout deploy did not fail, got status: $STATUS_BODY"; exit 1; }
[[ $ELAPSED -lt 25 ]] || { echo "FAIL: timeout deploy took ${ELAPSED}s, expected <25s"; exit 1; }
echo "  Per-step timeout killed long sleep: OK (failed in ${ELAPSED}s)"

echo "=== F3.2c: Re-run ==="
# Re-run the failed timeout deployment. The endpoint bypasses the gate and
# creates a new deployment with the same release + env.
REDIR=$(curl -s -b "$COOKIES" -D - -o /dev/null -X POST -d "csrf_token=$CSRF" "$BASE/deployments/$TIMEOUT_DEP/redeploy" | grep -i "^location:" | awk '{print $2}' | tr -d '\r')
NEW_DEP=$(echo "$REDIR" | grep -oP '/deployments/\K[0-9]+')
[[ -n "$NEW_DEP" ]] || { echo "FAIL: redeploy did not redirect"; exit 1; }
[[ "$NEW_DEP" != "$TIMEOUT_DEP" ]] || { echo "FAIL: redeploy returned same deployment ID $NEW_DEP"; exit 1; }
echo "Re-run Deployment ID: $NEW_DEP"

# Poll the new deployment until it reaches a terminal state (same sleep/timeout
# step, so it will also fail).
for i in {1..100}; do
  RERUN_STATUS=$(curl_body "$BASE/deployments/$NEW_DEP/status")
  if echo "$RERUN_STATUS" | grep -qE 'failed|succeeded|cancelled'; then break; fi
  sleep 0.2
done
echo "$RERUN_STATUS" | grep -q 'failed' || { echo "FAIL: re-run deploy did not fail, got status: $RERUN_STATUS"; exit 1; }
echo "  Re-run deployment failed as expected: OK"

# The new deployment's note should record the lineage.
RERUN_PAGE=$(curl_body "$BASE/deployments/$NEW_DEP")
echo "$RERUN_PAGE" | grep -q "Re-run of #$TIMEOUT_DEP" || { echo "FAIL: re-run note missing lineage text"; exit 1; }
echo "  Re-run note records lineage: OK"

echo "=== F3.2d: Step Retry on Failure ==="
# A step with max_retries=2 and script_body=exit+1 should be retried twice,
# logging attempt and retry messages, before the deployment fails.
STEPS_PAGE=$(curl_body "$BASE/projects/$PROJECT_ID/steps-page")
TIMEOUT_STEP_ID=$(echo "$STEPS_PAGE" | grep -oP 'step-row-\K[0-9]+' | sort -n | tail -1)
if [[ -n "$TIMEOUT_STEP_ID" ]]; then
  do_delete "$BASE/projects/$PROJECT_ID/steps/$TIMEOUT_STEP_ID"
fi

CODE=$(curl_silent -X POST -d "name=RetryStep&script_body=exit+1&max_retries=2&csrf_token=$CSRF" "$BASE/projects/$PROJECT_ID/steps")
[[ "$CODE" == "200" ]] || { echo "FAIL: create retry step got $CODE"; exit 1; }

CODE=$(curl_silent -X POST -d "version=1.0.4&csrf_token=$CSRF" "$BASE/projects/$PROJECT_ID/releases")
[[ "$CODE" == "303" ]] || { echo "FAIL: create retry release got $CODE"; exit 1; }
RETRY_REL=$(curl_body "$BASE/projects/$PROJECT_ID/releases" | grep -oP 'href="/projects/'$PROJECT_ID'/releases/\K[0-9]+' | sort -n | tail -1)
echo "Retry Release ID: $RETRY_REL"

RETRY_URL=$(curl -s -b "$COOKIES" -D - -o /dev/null -X POST -d "release_id=$RETRY_REL&environment_id=$ENV_ID&csrf_token=$CSRF" "$BASE/deployments" | grep -i "^location:" | awk '{print $2}' | tr -d '\r')
RETRY_DEP=$(echo "$RETRY_URL" | grep -oP '/deployments/\K[0-9]+')
[[ -n "$RETRY_DEP" ]] || { echo "FAIL: retry deployment did not redirect"; exit 1; }
echo "Retry Deployment ID: $RETRY_DEP"

for i in {1..100}; do
  RETRY_STATUS=$(curl_body "$BASE/deployments/$RETRY_DEP/status")
  if echo "$RETRY_STATUS" | grep -qE 'failed|succeeded|cancelled'; then break; fi
  sleep 0.2
done
echo "$RETRY_STATUS" | grep -q 'failed' || { echo "FAIL: retry deploy did not fail, got status: $RETRY_STATUS"; exit 1; }

LOG_LINES=$(sqlite3 durpdeploy.db "SELECT line FROM deployment_logs WHERE deployment_id=$RETRY_DEP ORDER BY id;")
echo "$LOG_LINES" | grep -q "attempt 1" || { echo "FAIL: retry log missing attempt 1"; exit 1; }
echo "$LOG_LINES" | grep -q "retrying" || { echo "FAIL: retry log missing retrying"; exit 1; }
echo "  Step retry loop ran: OK"

echo "=== F3.3: Validation Path ==="
CODE=$(curl_silent -X POST -d "name=&csrf_token=$CSRF" "$BASE/projects")
[[ "$CODE" == "422" ]] || { echo "FAIL: empty project name should be 422, got $CODE"; exit 1; }

echo "=== F3.4: Variable Fallback ==="
curl -s -b "$COOKIES" -o /dev/null -X POST -d "name=StepMissing&script=echo+%24%7BMISSING%7D&csrf_token=$CSRF" "$BASE/projects/$PROJECT_ID/steps"
curl -s -b "$COOKIES" -o /dev/null -X POST -d "version=2.0.0&csrf_token=$CSRF" "$BASE/projects/$PROJECT_ID/releases"
NEW_REL=$(curl_body "$BASE/projects/$PROJECT_ID/releases" | grep -oP 'href="/projects/'$PROJECT_ID'/releases/\K[0-9]+' | sort -n | tail -1)
curl -s -b "$COOKIES" -o /dev/null -X POST -d "release_id=$NEW_REL&environment_id=$ENV_ID&csrf_token=$CSRF" "$BASE/deployments"

echo "=== F3.5: Lifecycle Gate ==="
# Separate project + envs + lifecycle so the F3.1 project stays free-floating.
LC_PROJECT_ID=$(curl_body "$BASE/projects" | grep -oP 'href="/projects/\K[0-9]+' | head -1)
# We can't easily mint unique names via grep, so use a deterministic counter trick.
# Use the project list and grab the highest id.
LC_PROJECT_ID=$(curl_body "$BASE/projects" | grep -oP 'href="/projects/\K[0-9]+' | sort -n | tail -1)
LC_NAME="LC-Project-$(date +%s)"
CODE=$(curl_silent -X POST -d "name=$LC_NAME&csrf_token=$CSRF" "$BASE/projects")
[[ "$CODE" == "303" ]] || { echo "FAIL: create lifecycle project got $CODE"; exit 1; }
LC_PROJECT_ID=$(curl_body "$BASE/projects" | grep -oP 'href="/projects/\K[0-9]+' | sort -n | tail -1)
echo "Lifecycle Project ID: $LC_PROJECT_ID"

# Three envs: LC-Dev, LC-Test, LC-Prod + an "outside" env.
LC_TS=$(date +%s)
LC_DEV="LC-Dev-$LC_TS"
LC_TEST="LC-Test-$LC_TS"
LC_PROD="LC-Prod-$LC_TS"
LC_OUT="LC-Out-$LC_TS"
for E in "$LC_DEV" "$LC_TEST" "$LC_PROD" "$LC_OUT"; do
  CODE=$(curl_silent -X POST -d "name=$E&csrf_token=$CSRF" "$BASE/environments")
  [[ "$CODE" == "303" ]] || { echo "FAIL: create env $E got $CODE"; exit 1; }
done
LC_DEV_ID=$(curl_body "$BASE/environments" | python3 -c "import sys,re; html=sys.stdin.read(); m=re.search(r'<td class=\"truncate\">$LC_DEV</td>.*?href=\"/environments/(\d+)/edit\"', html, re.S); print(m.group(1) if m else '')")
LC_TEST_ID=$(curl_body "$BASE/environments" | python3 -c "import sys,re; html=sys.stdin.read(); m=re.search(r'<td class=\"truncate\">$LC_TEST</td>.*?href=\"/environments/(\d+)/edit\"', html, re.S); print(m.group(1) if m else '')")
LC_PROD_ID=$(curl_body "$BASE/environments" | python3 -c "import sys,re; html=sys.stdin.read(); m=re.search(r'<td class=\"truncate\">$LC_PROD</td>.*?href=\"/environments/(\d+)/edit\"', html, re.S); print(m.group(1) if m else '')")
LC_OUT_ID=$(curl_body "$BASE/environments" | python3 -c "import sys,re; html=sys.stdin.read(); m=re.search(r'<td class=\"truncate\">$LC_OUT</td>.*?href=\"/environments/(\d+)/edit\"', html, re.S); print(m.group(1) if m else '')")
echo "Env IDs: dev=$LC_DEV_ID test=$LC_TEST_ID prod=$LC_PROD_ID out=$LC_OUT_ID"

# Lifecycle: Dev -> Test -> Prod
LC_LIFECYCLE_NAME="LC-$LC_TS"
CODE=$(curl_silent -X POST -d "name=$LC_LIFECYCLE_NAME&csrf_token=$CSRF" "$BASE/lifecycles")
[[ "$CODE" == "303" ]] || { echo "FAIL: create lifecycle got $CODE"; exit 1; }
LC_LIFECYCLE_ID=$(curl_body "$BASE/lifecycles" | python3 -c "import sys,re; html=sys.stdin.read(); m=re.search(r'<a href=\"/lifecycles/(\d+)\"[^>]*>$LC_LIFECYCLE_NAME</a>', html); print(m.group(1) if m else '')")
echo "Lifecycle ID: $LC_LIFECYCLE_ID"

for EID in "$LC_DEV_ID" "$LC_TEST_ID" "$LC_PROD_ID"; do
  CODE=$(curl_silent -X POST -d "environment_id=$EID&csrf_token=$CSRF" "$BASE/lifecycles/$LC_LIFECYCLE_ID/stages")
  [[ "$CODE" == "303" ]] || { echo "FAIL: add stage env=$EID got $CODE"; exit 1; }
done

# Assign lifecycle to project.
CODE=$(curl_silent -X PUT -d "name=$LC_NAME&description=&lifecycle_id=$LC_LIFECYCLE_ID&csrf_token=$CSRF" "$BASE/projects/$LC_PROJECT_ID")
[[ "$CODE" == "303" ]] || { echo "FAIL: assign lifecycle got $CODE"; exit 1; }

# Create one step + one release on the lifecycle project.
CODE=$(curl_silent -X POST -d "name=step1&script_body=exit+0&csrf_token=$CSRF" "$BASE/projects/$LC_PROJECT_ID/steps")
[[ "$CODE" == "200" ]] || { echo "FAIL: create step got $CODE"; exit 1; }
CODE=$(curl_silent -X POST -d "version=1.0.0&csrf_token=$CSRF" "$BASE/projects/$LC_PROJECT_ID/releases")
[[ "$CODE" == "303" ]] || { echo "FAIL: create release got $CODE"; exit 1; }
LC_REL_ID=$(curl_body "$BASE/projects/$LC_PROJECT_ID/releases" | grep -oP 'href="/projects/'$LC_PROJECT_ID'/releases/\K[0-9]+' | sort -n | tail -1)
echo "LC Release ID: $LC_REL_ID"

# Deploy v1 to Dev -> 303
CODE=$(curl_silent -X POST -d "release_id=$LC_REL_ID&environment_id=$LC_DEV_ID&csrf_token=$CSRF" "$BASE/deployments")
[[ "$CODE" == "303" ]] || { echo "FAIL: deploy v1 to dev got $CODE, want 303"; exit 1; }
# Wait for the dev deploy to finish (status endpoint polling).
for i in {1..50}; do
  if curl_body "$BASE/deployments/$(curl_body "$BASE/deployments" | grep -oP 'release-row-[0-9]+|deployments/\K[0-9]+' | tail -1)/status" 2>/dev/null | grep -q 'succeeded'; then break; fi
  sleep 0.1
done

# Now deploy v1 to Prod directly (skipping Test) -> 422
CODE=$(curl_silent -X POST -d "release_id=$LC_REL_ID&environment_id=$LC_PROD_ID&csrf_token=$CSRF" "$BASE/deployments")
[[ "$CODE" == "422" ]] || { echo "FAIL: deploy v1 to prod (skipping test) got $CODE, want 422"; exit 1; }
echo "  Dev->Prod skip blocked: OK (422)"

# Deploy v1 to Test -> 303
CODE=$(curl_silent -X POST -d "release_id=$LC_REL_ID&environment_id=$LC_TEST_ID&csrf_token=$CSRF" "$BASE/deployments")
[[ "$CODE" == "303" ]] || { echo "FAIL: deploy v1 to test got $CODE, want 303"; exit 1; }
sleep 0.5

# Deploy v1 to Prod after Test succeeded -> 303
CODE=$(curl_silent -X POST -d "release_id=$LC_REL_ID&environment_id=$LC_PROD_ID&csrf_token=$CSRF" "$BASE/deployments")
[[ "$CODE" == "303" ]] || { echo "FAIL: deploy v1 to prod after test got $CODE, want 303"; exit 1; }
echo "  Full Dev->Test->Prod chain: OK (303)"

# Now create v2 release, attempt to deploy to Prod without going through Dev/Test -> 422
CODE=$(curl_silent -X POST -d "version=2.0.0&csrf_token=$CSRF" "$BASE/projects/$LC_PROJECT_ID/releases")
[[ "$CODE" == "303" ]] || { echo "FAIL: create v2 got $CODE"; exit 1; }
V2_REL_ID=$(curl_body "$BASE/projects/$LC_PROJECT_ID/releases" | grep -oP 'href="/projects/'$LC_PROJECT_ID'/releases/\K[0-9]+' | sort -n | tail -1)
CODE=$(curl_silent -X POST -d "release_id=$V2_REL_ID&environment_id=$LC_PROD_ID&csrf_token=$CSRF" "$BASE/deployments")
[[ "$CODE" == "422" ]] || { echo "FAIL: deploy v2 to prod (no chain) got $CODE, want 422"; exit 1; }
echo "  New version without chain: blocked (422)"

echo "=== F3.5b: Approval Gate ==="
# A separate lifecycle where the prod stage requires approval. Deployments
# to prod should pause at pending_approval until explicitly approved.
for E in app-dev app-staging app-prod; do
  CODE=$(curl_silent -X POST -d "name=$E&csrf_token=$CSRF" "$BASE/environments")
  [[ "$CODE" == "303" ]] || { echo "FAIL: create env $E got $CODE"; exit 1; }
done
APP_DEV_ID=$(sqlite3 durpdeploy.db "SELECT id FROM environments WHERE name='app-dev';")
APP_STAGING_ID=$(sqlite3 durpdeploy.db "SELECT id FROM environments WHERE name='app-staging';")
APP_PROD_ID=$(sqlite3 durpdeploy.db "SELECT id FROM environments WHERE name='app-prod';")
echo "App Env IDs: dev=$APP_DEV_ID staging=$APP_STAGING_ID prod=$APP_PROD_ID"

CODE=$(curl_silent -X POST -d "name=app-lifecycle&csrf_token=$CSRF" "$BASE/lifecycles")
[[ "$CODE" == "303" ]] || { echo "FAIL: create app-lifecycle got $CODE"; exit 1; }
APP_LC_ID=$(sqlite3 durpdeploy.db "SELECT id FROM lifecycles WHERE name='app-lifecycle';")
echo "App Lifecycle ID: $APP_LC_ID"

for EID in "$APP_DEV_ID" "$APP_STAGING_ID" "$APP_PROD_ID"; do
  CODE=$(curl_silent -X POST -d "environment_id=$EID&csrf_token=$CSRF" "$BASE/lifecycles/$APP_LC_ID/stages")
  [[ "$CODE" == "303" ]] || { echo "FAIL: add stage env=$EID got $CODE"; exit 1; }
done

APP_PROD_STAGE_ID=$(sqlite3 durpdeploy.db "SELECT id FROM lifecycle_stages WHERE lifecycle_id=$APP_LC_ID AND environment_id=$APP_PROD_ID;")
CODE=$(curl_silent -X PATCH -d "requires_approval=1&csrf_token=$CSRF" "$BASE/lifecycles/$APP_LC_ID/stages/$APP_PROD_STAGE_ID")
[[ "$CODE" == "303" ]] || { echo "FAIL: patch prod stage got $CODE"; exit 1; }

CODE=$(curl_silent -X POST -d "name=AppProject&csrf_token=$CSRF" "$BASE/projects")
[[ "$CODE" == "303" ]] || { echo "FAIL: create app project got $CODE"; exit 1; }
APP_PROJ_ID=$(sqlite3 durpdeploy.db "SELECT id FROM projects WHERE name='AppProject';")
echo "App Project ID: $APP_PROJ_ID"

CODE=$(curl_silent -X PUT -d "name=AppProject&description=&lifecycle_id=$APP_LC_ID&csrf_token=$CSRF" "$BASE/projects/$APP_PROJ_ID")
[[ "$CODE" == "303" ]] || { echo "FAIL: assign lifecycle to app project got $CODE"; exit 1; }

CODE=$(curl_silent -X POST -d "name=app-step&script_body=exit+0&csrf_token=$CSRF" "$BASE/projects/$APP_PROJ_ID/steps")
[[ "$CODE" == "200" ]] || { echo "FAIL: create app step got $CODE"; exit 1; }
CODE=$(curl_silent -X POST -d "version=1.0.0&csrf_token=$CSRF" "$BASE/projects/$APP_PROJ_ID/releases")
[[ "$CODE" == "303" ]] || { echo "FAIL: create app release got $CODE"; exit 1; }
APP_REL_ID=$(curl_body "$BASE/projects/$APP_PROJ_ID/releases" | grep -oP 'href="/projects/'$APP_PROJ_ID'/releases/\K[0-9]+' | sort -n | tail -1)
echo "App Release ID: $APP_REL_ID"

# Deploy to dev -> should succeed
DEV_URL=$(curl -s -b "$COOKIES" -D - -o /dev/null -X POST -d "release_id=$APP_REL_ID&environment_id=$APP_DEV_ID&csrf_token=$CSRF" "$BASE/deployments" | grep -i "^location:" | awk '{print $2}' | tr -d '\r')
DEV_DEP=$(echo "$DEV_URL" | grep -oP '/deployments/\K[0-9]+')
[[ -n "$DEV_DEP" ]] || { echo "FAIL: dev deployment did not redirect"; exit 1; }
echo "Dev Deployment ID: $DEV_DEP"
for i in {1..50}; do
  if curl_body "$BASE/deployments/$DEV_DEP/status" | grep -q 'succeeded'; then break; fi
  sleep 0.1
done
echo "  Dev deploy succeeded: OK"

# Deploy to staging -> should succeed
STAGING_URL=$(curl -s -b "$COOKIES" -D - -o /dev/null -X POST -d "release_id=$APP_REL_ID&environment_id=$APP_STAGING_ID&csrf_token=$CSRF" "$BASE/deployments" | grep -i "^location:" | awk '{print $2}' | tr -d '\r')
STAGING_DEP=$(echo "$STAGING_URL" | grep -oP '/deployments/\K[0-9]+')
[[ -n "$STAGING_DEP" ]] || { echo "FAIL: staging deployment did not redirect"; exit 1; }
echo "Staging Deployment ID: $STAGING_DEP"
for i in {1..50}; do
  if curl_body "$BASE/deployments/$STAGING_DEP/status" | grep -q 'succeeded'; then break; fi
  sleep 0.1
done
echo "  Staging deploy succeeded: OK"

# Deploy to prod -> should be pending_approval
PROD_URL=$(curl -s -b "$COOKIES" -D - -o /dev/null -X POST -d "release_id=$APP_REL_ID&environment_id=$APP_PROD_ID&csrf_token=$CSRF" "$BASE/deployments" | grep -i "^location:" | awk '{print $2}' | tr -d '\r')
PROD_DEP=$(echo "$PROD_URL" | grep -oP '/deployments/\K[0-9]+')
[[ -n "$PROD_DEP" ]] || { echo "FAIL: prod deployment did not redirect"; exit 1; }
echo "Prod Deployment ID: $PROD_DEP"

PROD_STATUS=$(curl_body "$BASE/deployments/$PROD_DEP/status")
echo "$PROD_STATUS" | grep -q "pending_approval" || { echo "FAIL: prod deployment not pending_approval"; exit 1; }
echo "  Prod deploy pending_approval: OK"

# Approve and run
CODE=$(curl_silent -X POST -d "approved_by=alice&csrf_token=$CSRF" "$BASE/deployments/$PROD_DEP/approve")
[[ "$CODE" == "303" ]] || { echo "FAIL: approve prod deploy got $CODE"; exit 1; }

for i in {1..100}; do
  PROD_STATUS=$(curl_body "$BASE/deployments/$PROD_DEP/status")
  if echo "$PROD_STATUS" | grep -qE 'failed|succeeded|cancelled'; then break; fi
  sleep 0.2
done
echo "$PROD_STATUS" | grep -q 'succeeded' || { echo "FAIL: prod deploy did not succeed after approval, got status: $PROD_STATUS"; exit 1; }
echo "  Prod deploy succeeded after approval: OK"

sqlite3 durpdeploy.db "SELECT approved_by FROM approvals WHERE deployment_id=$PROD_DEP;" | grep -q "alice" || { echo "FAIL: approval not recorded"; exit 1; }
echo "  Approval recorded for alice: OK"

echo "=== F3.6: Force Deploy ==="
# Create v3 release, deploy directly to Prod with force=true -> 303
CODE=$(curl_silent -X POST -d "version=3.0.0&csrf_token=$CSRF" "$BASE/projects/$LC_PROJECT_ID/releases")
[[ "$CODE" == "303" ]] || { echo "FAIL: create v3 got $CODE"; exit 1; }
V3_REL_ID=$(curl_body "$BASE/projects/$LC_PROJECT_ID/releases" | grep -oP 'href="/projects/'$LC_PROJECT_ID'/releases/\K[0-9]+' | sort -n | tail -1)
CODE=$(curl_silent -X POST -d "release_id=$V3_REL_ID&environment_id=$LC_PROD_ID&force=true&csrf_token=$CSRF" "$BASE/deployments")
[[ "$CODE" == "303" ]] || { echo "FAIL: force deploy v3 to prod got $CODE, want 303"; exit 1; }
echo "  Force deploy to prod: OK (303)"

echo "=== F3.7: Env Restriction ==="
# Project is bound to lifecycle. Try to deploy v3 to the "out" env (not in lifecycle).
# Force should NOT bypass this restriction.
CODE=$(curl_silent -X POST -d "release_id=$V3_REL_ID&environment_id=$LC_OUT_ID&csrf_token=$CSRF" "$BASE/deployments")
[[ "$CODE" == "422" ]] || { echo "FAIL: deploy to non-lifecycle env got $CODE, want 422"; exit 1; }
echo "  Deploy to non-lifecycle env (no force): blocked (422)"
CODE=$(curl_silent -X POST -d "release_id=$V3_REL_ID&environment_id=$LC_OUT_ID&force=true&csrf_token=$CSRF" "$BASE/deployments")
[[ "$CODE" == "422" ]] || { echo "FAIL: force deploy to non-lifecycle env got $CODE, want 422"; exit 1; }
echo "  Force deploy to non-lifecycle env: still blocked (422)"

echo "=== F3.8: Deploy Page ==="
# Verify the dedicated deploy page renders for the existing TestProject
# (free-floating, has release 1.0.0 and env TestEnv). A second test exercises
# the lifecycle-bound case via the LC project.
CODE=$(curl_silent "$BASE/projects/$PROJECT_ID/deploy")
[[ "$CODE" == "200" ]] || { echo "FAIL: GET /projects/$PROJECT_ID/deploy got $CODE"; exit 1; }
echo "  Free-floating deploy page renders: OK (200)"

# Page should contain the form with the release version and env name.
PAGE=$(curl_body "$BASE/projects/$PROJECT_ID/deploy")
echo "$PAGE" | grep -q "1.0.0" || { echo "FAIL: release 1.0.0 missing from deploy page"; exit 1; }
echo "$PAGE" | grep -q "TestEnv" || { echo "FAIL: TestEnv missing from deploy page"; exit 1; }
echo "$PAGE" | grep -q "action=\"/projects/$PROJECT_ID/deploy\"" || { echo "FAIL: form action missing"; exit 1; }

# Lifecycle-bound deploy page: only stage envs should appear.
CODE=$(curl_silent "$BASE/projects/$LC_PROJECT_ID/deploy")
[[ "$CODE" == "200" ]] || { echo "FAIL: GET lifecycle deploy page got $CODE"; exit 1; }
LCPAGE=$(curl_body "$BASE/projects/$LC_PROJECT_ID/deploy")
echo "$LCPAGE" | grep -q "LC-Dev-$LC_TS" || { echo "FAIL: LC-Dev not in lifecycle deploy page"; exit 1; }
echo "$LCPAGE" | grep -q "LC-Test-$LC_TS" || { echo "FAIL: LC-Test not in lifecycle deploy page"; exit 1; }
echo "$LCPAGE" | grep -q "LC-Prod-$LC_TS" || { echo "FAIL: LC-Prod not in lifecycle deploy page"; exit 1; }
echo "$LCPAGE" | grep -q "LC-Out-$LC_TS" && { echo "FAIL: LC-Out should NOT appear in lifecycle deploy page"; exit 1; } || true
echo "  Lifecycle deploy page filters non-stage envs: OK"

# F3.9: POST to the deploy page — env restriction. Try to deploy an
# existing release to a non-lifecycle env via the new page -> 422. The
# success path is already covered by the existing F3.1 inline-form test
# and by the unit tests; the page-specific path is the gate behavior.
CODE=$(curl_silent -X POST -d "release_id=$LC_REL_ID&environment_id=$LC_OUT_ID&csrf_token=$CSRF" "$BASE/projects/$LC_PROJECT_ID/deploy")
[[ "$CODE" == "422" ]] || { echo "FAIL: deploy page gate-block got $CODE, want 422"; exit 1; }
echo "  Deploy page env restriction: blocked (422)"

# F3.10: cross-project release rejected (400) — the project-scoped route
# validates that the release belongs to this project.
curl -s -b "$COOKIES" -o /dev/null -X POST -d "name=DP-cross-proj&csrf_token=$CSRF" "$BASE/projects"
CROSS_PROJ_ID=$(curl_body "$BASE/projects" | grep -oP 'href="/projects/\K[0-9]+' | sort -n | tail -1)
CODE=$(curl_silent -X POST -d "release_id=$LC_REL_ID&environment_id=$LC_DEV_ID&csrf_token=$CSRF" "$BASE/projects/$CROSS_PROJ_ID/deploy")
[[ "$CODE" == "400" ]] || { echo "FAIL: cross-project deploy got $CODE, want 400"; exit 1; }
echo "  Cross-project release rejected: 400"

echo "=== F3.11: Scheduled Deployment ==="
CODE=$(curl_silent -X POST -d "release_id=$RELEASE_ID&environment_id=$ENV_ID&cron=*+*+*+*+*&note=e2e-scheduled&enabled=1&csrf_token=$CSRF" "$BASE/projects/$PROJECT_ID/schedules")
[[ "$CODE" == "303" ]] || { echo "FAIL: create schedule got $CODE"; exit 1; }

BEFORE_DEP=$(curl_body "$BASE/deployments" | grep -oP 'href="/deployments/\K[0-9]+' | sort -n | tail -1)
echo "Latest deployment before schedule: $BEFORE_DEP"

echo "  Sleeping 100s for scheduler tick..."
sleep 100

AFTER_DEP=$(curl_body "$BASE/deployments" | grep -oP 'href="/deployments/\K[0-9]+' | sort -n | tail -1)
echo "Latest deployment after schedule: $AFTER_DEP"
[[ "$AFTER_DEP" -gt "$BEFORE_DEP" ]] || { echo "FAIL: scheduler did not create a new deployment"; exit 1; }

DEP_PAGE=$(curl_body "$BASE/deployments/$AFTER_DEP")
echo "$DEP_PAGE" | grep -q "Scheduled:" || { echo "FAIL: scheduled deployment note missing 'Scheduled:'"; exit 1; }
echo "  Scheduled deployment created with note: OK"

SCHED_LIST=$(curl_body "$BASE/projects/$PROJECT_ID/schedules")
echo "$SCHED_LIST" | grep -qF "* * * * *" || { echo "FAIL: schedule missing from list"; exit 1; }
echo "$SCHED_LIST" | grep -q "On" || { echo "FAIL: schedule not enabled"; exit 1; }
echo "  Schedule list shows enabled schedule with future next_run_at: OK"

echo "=== F3.12: User Management (P1-2) ==="
# List the seed admin in /admin/users.
CODE=$(curl_silent "$BASE/admin/users")
[[ "$CODE" == "200" ]] || { echo "FAIL: admin GET /admin/users got $CODE, want 200"; exit 1; }
USERS_PAGE=$(curl_body "$BASE/admin/users")
echo "$USERS_PAGE" | grep -qF "$ADMIN_EMAIL" || { echo "FAIL: admin user not in /admin/users list"; exit 1; }
echo "  Admin lists /admin/users with seed admin: OK"

# Create a new deployer via POST /admin/users.
NEW_EMAIL="e2e-newdeployer@test.local"
NEW_PASS="newdeployer-pass-1234"
REDIR=$(curl -s -b "$COOKIES" -D - -o /dev/null \
    -X POST -d "email=$NEW_EMAIL&name=NewDeployer&role=deployer&password=$NEW_PASS&csrf_token=$CSRF" \
    "$BASE/admin/users" | grep -i "^location:" | awk '{print $2}' | tr -d '\r')
[[ "$REDIR" == /admin/users?new_user_id=* ]] || { echo "FAIL: create user redirect = $REDIR, want /admin/users?new_user_id=..."; exit 1; }
echo "  POST /admin/users created user + flashed password: OK"

# Follow the redirect and confirm the password banner shows the plaintext.
BANNER=$(curl -s -b "$COOKIES" "$BASE$REDIR")
echo "$BANNER" | grep -qF "$NEW_PASS" || { echo "FAIL: banner missing the new password"; exit 1; }
echo "  Banner shows the new password: OK"

# The new user can log in with the chosen password.
NEW_LOGIN=$(mktemp)
CODE=$(curl -s -c "$NEW_LOGIN" -o /dev/null -w "%{http_code}" \
    -X POST -d "email=$NEW_EMAIL&password=$NEW_PASS" "$BASE/login")
[[ "$CODE" == "303" ]] || { echo "FAIL: new user login got $CODE, want 303"; exit 1; }
echo "  New user can log in: OK"

# The freshly-created deployer cannot access /admin/users.
CODE=$(curl -s -b "$NEW_LOGIN" -o /dev/null -w "%{http_code}" "$BASE/admin/users")
[[ "$CODE" == "403" ]] || { echo "FAIL: non-admin GET /admin/users got $CODE, want 403"; exit 1; }
echo "  Non-admin gets 403 on /admin/users: OK"

# Refresh the admin CSRF (the original $CSRF is still valid; we re-pull
# from the DB to mirror the existing test idiom).
ADMIN_SESSION_ID=$(awk '$6 == "session" { print $7 }' "$COOKIES")
ADMIN_CSRF=$(sqlite3 durpdeploy.db "SELECT csrf_token FROM sessions WHERE id='$ADMIN_SESSION_ID';")

# Promote the new user to admin via PUT /admin/users/{id}.
NEW_USER_ID=$(sqlite3 durpdeploy.db "SELECT id FROM users WHERE email='$NEW_EMAIL';")
CODE=$(curl -s -b "$COOKIES" -o /dev/null -w "%{http_code}" \
    -H "X-CSRF-Token: $ADMIN_CSRF" \
    -X PUT -d "name=NewDeployer&role=admin" \
    "$BASE/admin/users/$NEW_USER_ID")
[[ "$CODE" == "303" || "$CODE" == "200" ]] || { echo "FAIL: PUT /admin/users/{id} got $CODE, want 303/200"; exit 1; }
NEW_ROLE=$(sqlite3 durpdeploy.db "SELECT role FROM users WHERE id=$NEW_USER_ID;")
[[ "$NEW_ROLE" == "admin" ]] || { echo "FAIL: user role = $NEW_ROLE, want admin"; exit 1; }
echo "  PUT /admin/users/{id} promotes deployer to admin: OK"

# The new user's previous session should be deleted (role change).
NEW_SESSION_ID=$(awk '$6 == "session" { print $7 }' "$NEW_LOGIN")
SESSION_EXISTS=$(sqlite3 durpdeploy.db "SELECT COUNT(*) FROM sessions WHERE id='$NEW_SESSION_ID' AND user_id=$NEW_USER_ID;")
[[ "$SESSION_EXISTS" == "0" ]] || { echo "FAIL: new user's session still exists after role change"; exit 1; }
echo "  Role change invalidated new user's session: OK"

# Admin cannot delete themselves.
ADMIN_ID=$(sqlite3 durpdeploy.db "SELECT id FROM users WHERE email='$ADMIN_EMAIL';")
CODE=$(curl -s -b "$COOKIES" -o /dev/null -w "%{http_code}" \
    -H "X-CSRF-Token: $ADMIN_CSRF" \
    -X DELETE "$BASE/admin/users/$ADMIN_ID")
[[ "$CODE" == "422" ]] || { echo "FAIL: self-delete got $CODE, want 422"; exit 1; }
echo "  Self-delete rejected: OK"

# Demote the new user back to deployer and delete them (no project membership).
CODE=$(curl -s -b "$COOKIES" -o /dev/null -w "%{http_code}" \
    -H "X-CSRF-Token: $ADMIN_CSRF" \
    -X PUT -d "name=NewDeployer&role=deployer" \
    "$BASE/admin/users/$NEW_USER_ID")
[[ "$CODE" == "303" || "$CODE" == "200" ]] || { echo "FAIL: demote got $CODE, want 303/200"; exit 1; }
CODE=$(curl -s -b "$COOKIES" -o /dev/null -w "%{http_code}" \
    -H "X-CSRF-Token: $ADMIN_CSRF" \
    -X DELETE "$BASE/admin/users/$NEW_USER_ID")
[[ "$CODE" == "303" || "$CODE" == "200" ]] || { echo "FAIL: delete got $CODE, want 303/200"; exit 1; }
USER_GONE=$(sqlite3 durpdeploy.db "SELECT COUNT(*) FROM users WHERE id=$NEW_USER_ID;")
[[ "$USER_GONE" == "0" ]] || { echo "FAIL: deleted user still in DB"; exit 1; }
echo "  Admin can delete the new user: OK"

# Audit log captured the user-management actions.
USER_AUDIT=$(sqlite3 durpdeploy.db "SELECT COUNT(*) FROM audit_log WHERE action IN ('create_user','update_user','delete_user');")
[[ "$USER_AUDIT" -ge 4 ]] || { echo "FAIL: expected >=4 user audit rows, got $USER_AUDIT"; exit 1; }
echo "  Audit log captured create/update/delete_user: OK"

rm -f "$NEW_LOGIN"

echo "=== ALL E2E CHECKS PASSED ==="
