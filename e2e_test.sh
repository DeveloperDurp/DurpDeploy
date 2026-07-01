#!/usr/bin/env bash
set -euo pipefail

BASE="http://localhost:8080"
TMP=$(mktemp -d)
trap "rm -rf $TMP; kill %1 2>/dev/null || true" EXIT

echo "=== Building and starting server ==="
rm -f durpdeploy.db
go build -o "$TMP/durpdeploy" ./cmd/server
"$TMP/durpdeploy" &
sleep 2

curl_silent() { curl -s -o /dev/null -w "%{http_code}" "$@"; }
curl_body() { curl -s "$@"; }

echo "=== F3.1: Happy Path ==="
CODE=$(curl_silent -X POST -d "name=TestProject" "$BASE/projects")
[[ "$CODE" == "303" ]] || { echo "FAIL: create project got $CODE"; exit 1; }
PROJECT_ID=$(curl_body "$BASE/projects" | grep -oP 'href="/projects/\K[0-9]+' | head -1)
echo "Project ID: $PROJECT_ID"

CODE=$(curl_silent -X POST -d "name=TestEnv" "$BASE/environments")
[[ "$CODE" == "303" ]] || { echo "FAIL: create env got $CODE"; exit 1; }
ENV_ID=$(curl_body "$BASE/environments" | grep -oP 'href="/environments/\K[0-9]+' | head -1)
echo "Env ID: $ENV_ID"

CODE=$(curl_silent -X POST -d "name=Step1&script_body=echo+hello" "$BASE/projects/$PROJECT_ID/steps")
[[ "$CODE" == "200" ]] || { echo "FAIL: create step got $CODE"; exit 1; }

CODE=$(curl_silent -X POST -d "name=VAR1&value=hello&environment_id=$ENV_ID" "$BASE/projects/$PROJECT_ID/variables")
[[ "$CODE" == "303" ]] || { echo "FAIL: create variable got $CODE"; exit 1; }

CODE=$(curl_silent -X POST -d "version=1.0.0&release_notes=first" "$BASE/projects/$PROJECT_ID/releases")
[[ "$CODE" == "303" ]] || { echo "FAIL: create release got $CODE"; exit 1; }
RELEASE_ID=$(curl_body "$BASE/projects/$PROJECT_ID/releases" | grep -oP 'href="/projects/'$PROJECT_ID'/releases/\K[0-9]+' | sort -n | tail -1)
echo "Release ID: $RELEASE_ID"

DEP_URL=$(curl -s -D - -o /dev/null -X POST -d "release_id=$RELEASE_ID&environment_id=$ENV_ID" "$BASE/deployments" | grep -i "^location:" | awk '{print $2}' | tr -d '\r')
DEP_ID=$(echo "$DEP_URL" | grep -oP '/deployments/\K[0-9]+')
[[ -n "$DEP_ID" ]] || { echo "FAIL: create deployment did not redirect"; exit 1; }
echo "Deployment ID: $DEP_ID"

CODE=$(curl_silent "$BASE/deployments/$DEP_ID")
[[ "$CODE" == "200" ]] || { echo "FAIL: deployment page got $CODE"; exit 1; }

echo "=== F3.2: Cancel Path ==="
curl -s -o /dev/null -X POST -d "name=LongStep&script_body=sleep+10" "$BASE/projects/$PROJECT_ID/steps"
curl -s -o /dev/null -X POST -d "version=1.0.1" "$BASE/projects/$PROJECT_ID/releases"
CANCEL_REL=$(curl_body "$BASE/projects/$PROJECT_ID/releases" | grep -oP 'href="/projects/'$PROJECT_ID'/releases/\K[0-9]+' | sort -n | tail -1)
echo "Cancel Release ID: $CANCEL_REL"

CANCEL_URL=$(curl -s -D - -o /dev/null -X POST -d "release_id=$CANCEL_REL&environment_id=$ENV_ID" "$BASE/deployments" | grep -i "^location:" | awk '{print $2}' | tr -d '\r')
CANCEL_DEP=$(echo "$CANCEL_URL" | grep -oP '/deployments/\K[0-9]+')
[[ -n "$CANCEL_DEP" ]] || { echo "FAIL: cancel deployment did not redirect"; exit 1; }
echo "Cancel Deployment ID: $CANCEL_DEP"

for i in {1..50}; do
  if curl_body "$BASE/deployments/$CANCEL_DEP/status" | grep -q 'running'; then break; fi
  sleep 0.1
done
CODE=$(curl_silent -X POST "$BASE/deployments/$CANCEL_DEP/cancel")
[[ "$CODE" == "303" ]] || { echo "FAIL: cancel got $CODE"; exit 1; }

echo "=== F3.3: Validation Path ==="
CODE=$(curl_silent -X POST -d "name=" "$BASE/projects")
[[ "$CODE" == "422" ]] || { echo "FAIL: empty project name should be 422, got $CODE"; exit 1; }

echo "=== F3.4: Variable Fallback ==="
curl -s -o /dev/null -X POST -d "name=StepMissing&script=echo+%24%7BMISSING%7D" "$BASE/projects/$PROJECT_ID/steps"
curl -s -o /dev/null -X POST -d "version=2.0.0" "$BASE/projects/$PROJECT_ID/releases"
NEW_REL=$(curl_body "$BASE/projects/$PROJECT_ID/releases" | grep -oP 'href="/projects/'$PROJECT_ID'/releases/\K[0-9]+' | sort -n | tail -1)
curl -s -o /dev/null -X POST -d "release_id=$NEW_REL&environment_id=$ENV_ID" "$BASE/deployments"

echo "=== F3.5: Lifecycle Gate ==="
# Separate project + envs + lifecycle so the F3.1 project stays free-floating.
LC_PROJECT_ID=$(curl_body "$BASE/projects" | grep -oP 'href="/projects/\K[0-9]+' | head -1)
# We can't easily mint unique names via grep, so use a deterministic counter trick.
# Use the project list and grab the highest id.
LC_PROJECT_ID=$(curl_body "$BASE/projects" | grep -oP 'href="/projects/\K[0-9]+' | sort -n | tail -1)
LC_NAME="LC-Project-$(date +%s)"
CODE=$(curl_silent -X POST -d "name=$LC_NAME" "$BASE/projects")
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
  CODE=$(curl_silent -X POST -d "name=$E" "$BASE/environments")
  [[ "$CODE" == "303" ]] || { echo "FAIL: create env $E got $CODE"; exit 1; }
done
LC_DEV_ID=$(curl_body "$BASE/environments" | python3 -c "import sys,re; html=sys.stdin.read(); m=re.search(r'<td class=\"truncate\">$LC_DEV</td>.*?href=\"/environments/(\d+)/edit\"', html, re.S); print(m.group(1) if m else '')")
LC_TEST_ID=$(curl_body "$BASE/environments" | python3 -c "import sys,re; html=sys.stdin.read(); m=re.search(r'<td class=\"truncate\">$LC_TEST</td>.*?href=\"/environments/(\d+)/edit\"', html, re.S); print(m.group(1) if m else '')")
LC_PROD_ID=$(curl_body "$BASE/environments" | python3 -c "import sys,re; html=sys.stdin.read(); m=re.search(r'<td class=\"truncate\">$LC_PROD</td>.*?href=\"/environments/(\d+)/edit\"', html, re.S); print(m.group(1) if m else '')")
LC_OUT_ID=$(curl_body "$BASE/environments" | python3 -c "import sys,re; html=sys.stdin.read(); m=re.search(r'<td class=\"truncate\">$LC_OUT</td>.*?href=\"/environments/(\d+)/edit\"', html, re.S); print(m.group(1) if m else '')")
echo "Env IDs: dev=$LC_DEV_ID test=$LC_TEST_ID prod=$LC_PROD_ID out=$LC_OUT_ID"

# Lifecycle: Dev -> Test -> Prod
LC_LIFECYCLE_NAME="LC-$LC_TS"
CODE=$(curl_silent -X POST -d "name=$LC_LIFECYCLE_NAME" "$BASE/lifecycles")
[[ "$CODE" == "303" ]] || { echo "FAIL: create lifecycle got $CODE"; exit 1; }
LC_LIFECYCLE_ID=$(curl_body "$BASE/lifecycles" | python3 -c "import sys,re; html=sys.stdin.read(); m=re.search(r'<a href=\"/lifecycles/(\d+)\"[^>]*>$LC_LIFECYCLE_NAME</a>', html); print(m.group(1) if m else '')")
echo "Lifecycle ID: $LC_LIFECYCLE_ID"

for EID in "$LC_DEV_ID" "$LC_TEST_ID" "$LC_PROD_ID"; do
  CODE=$(curl_silent -X POST -d "environment_id=$EID" "$BASE/lifecycles/$LC_LIFECYCLE_ID/stages")
  [[ "$CODE" == "303" ]] || { echo "FAIL: add stage env=$EID got $CODE"; exit 1; }
done

# Assign lifecycle to project.
CODE=$(curl_silent -X PUT -d "name=$LC_NAME&description=&lifecycle_id=$LC_LIFECYCLE_ID" "$BASE/projects/$LC_PROJECT_ID")
[[ "$CODE" == "303" ]] || { echo "FAIL: assign lifecycle got $CODE"; exit 1; }

# Create one step + one release on the lifecycle project.
CODE=$(curl_silent -X POST -d "name=step1&script_body=exit+0" "$BASE/projects/$LC_PROJECT_ID/steps")
[[ "$CODE" == "200" ]] || { echo "FAIL: create step got $CODE"; exit 1; }
CODE=$(curl_silent -X POST -d "version=1.0.0" "$BASE/projects/$LC_PROJECT_ID/releases")
[[ "$CODE" == "303" ]] || { echo "FAIL: create release got $CODE"; exit 1; }
LC_REL_ID=$(curl_body "$BASE/projects/$LC_PROJECT_ID/releases" | grep -oP 'href="/projects/'$LC_PROJECT_ID'/releases/\K[0-9]+' | sort -n | tail -1)
echo "LC Release ID: $LC_REL_ID"

# Deploy v1 to Dev -> 303
CODE=$(curl_silent -X POST -d "release_id=$LC_REL_ID&environment_id=$LC_DEV_ID" "$BASE/deployments")
[[ "$CODE" == "303" ]] || { echo "FAIL: deploy v1 to dev got $CODE, want 303"; exit 1; }
# Wait for the dev deploy to finish (status endpoint polling).
for i in {1..50}; do
  if curl_body "$BASE/deployments/$(curl_body "$BASE/deployments" | grep -oP 'release-row-[0-9]+|deployments/\K[0-9]+' | tail -1)/status" 2>/dev/null | grep -q 'succeeded'; then break; fi
  sleep 0.1
done

# Now deploy v1 to Prod directly (skipping Test) -> 422
CODE=$(curl_silent -X POST -d "release_id=$LC_REL_ID&environment_id=$LC_PROD_ID" "$BASE/deployments")
[[ "$CODE" == "422" ]] || { echo "FAIL: deploy v1 to prod (skipping test) got $CODE, want 422"; exit 1; }
echo "  Dev->Prod skip blocked: OK (422)"

# Deploy v1 to Test -> 303
CODE=$(curl_silent -X POST -d "release_id=$LC_REL_ID&environment_id=$LC_TEST_ID" "$BASE/deployments")
[[ "$CODE" == "303" ]] || { echo "FAIL: deploy v1 to test got $CODE, want 303"; exit 1; }
sleep 0.5

# Deploy v1 to Prod after Test succeeded -> 303
CODE=$(curl_silent -X POST -d "release_id=$LC_REL_ID&environment_id=$LC_PROD_ID" "$BASE/deployments")
[[ "$CODE" == "303" ]] || { echo "FAIL: deploy v1 to prod after test got $CODE, want 303"; exit 1; }
echo "  Full Dev->Test->Prod chain: OK (303)"

# Now create v2 release, attempt to deploy to Prod without going through Dev/Test -> 422
CODE=$(curl_silent -X POST -d "version=2.0.0" "$BASE/projects/$LC_PROJECT_ID/releases")
[[ "$CODE" == "303" ]] || { echo "FAIL: create v2 got $CODE"; exit 1; }
V2_REL_ID=$(curl_body "$BASE/projects/$LC_PROJECT_ID/releases" | grep -oP 'href="/projects/'$LC_PROJECT_ID'/releases/\K[0-9]+' | sort -n | tail -1)
CODE=$(curl_silent -X POST -d "release_id=$V2_REL_ID&environment_id=$LC_PROD_ID" "$BASE/deployments")
[[ "$CODE" == "422" ]] || { echo "FAIL: deploy v2 to prod (no chain) got $CODE, want 422"; exit 1; }
echo "  New version without chain: blocked (422)"

echo "=== F3.6: Force Deploy ==="
# Create v3 release, deploy directly to Prod with force=true -> 303
CODE=$(curl_silent -X POST -d "version=3.0.0" "$BASE/projects/$LC_PROJECT_ID/releases")
[[ "$CODE" == "303" ]] || { echo "FAIL: create v3 got $CODE"; exit 1; }
V3_REL_ID=$(curl_body "$BASE/projects/$LC_PROJECT_ID/releases" | grep -oP 'href="/projects/'$LC_PROJECT_ID'/releases/\K[0-9]+' | sort -n | tail -1)
CODE=$(curl_silent -X POST -d "release_id=$V3_REL_ID&environment_id=$LC_PROD_ID&force=true" "$BASE/deployments")
[[ "$CODE" == "303" ]] || { echo "FAIL: force deploy v3 to prod got $CODE, want 303"; exit 1; }
echo "  Force deploy to prod: OK (303)"

echo "=== F3.7: Env Restriction ==="
# Project is bound to lifecycle. Try to deploy v3 to the "out" env (not in lifecycle).
# Force should NOT bypass this restriction.
CODE=$(curl_silent -X POST -d "release_id=$V3_REL_ID&environment_id=$LC_OUT_ID" "$BASE/deployments")
[[ "$CODE" == "422" ]] || { echo "FAIL: deploy to non-lifecycle env got $CODE, want 422"; exit 1; }
echo "  Deploy to non-lifecycle env (no force): blocked (422)"
CODE=$(curl_silent -X POST -d "release_id=$V3_REL_ID&environment_id=$LC_OUT_ID&force=true" "$BASE/deployments")
[[ "$CODE" == "422" ]] || { echo "FAIL: force deploy to non-lifecycle env got $CODE, want 422"; exit 1; }
echo "  Force deploy to non-lifecycle env: still blocked (422)"

echo "=== ALL E2E CHECKS PASSED ==="
