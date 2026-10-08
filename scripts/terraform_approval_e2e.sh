#!/usr/bin/env bash
# Running-server demo: retain real plans and decisions for browser inspection.
terraform_approval_e2e() {
    local engine image project environment release payload plan apply deployment state gates checksum revision code action
    local -r json_id='import json,sys; print(json.load(sys.stdin)["id"])'
    local prefix="terraform-approval"
    local script_dir
    script_dir=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
    engine=${DURPDEPLOY_CONTAINER_RUNTIME:-}
    if [[ -z "$engine" ]]; then
        for engine in docker podman; do
            if command -v "$engine" >/dev/null && "$engine" info >/dev/null 2>&1; then
                break
            fi
        done
    fi
    local runtime_args=()
    if [[ -n "${DURPDEPLOY_CONTAINER_URL:-}" ]]; then
        if [[ "$engine" == podman ]]; then
            runtime_args=(--remote "--url=$DURPDEPLOY_CONTAINER_URL")
        else
            runtime_args=("--host=$DURPDEPLOY_CONTAINER_URL")
        fi
    fi
    image=localhost/durpdeploy-gate-terraform:e2e
    echo '=== Persistent Terraform approval examples ==='
    "$engine" "${runtime_args[@]}" build --tag "$image" \
        --file "$script_dir/testdata/artifact-gate-terraform.Dockerfile" \
        "$script_dir/.." >/dev/null
    project=$(api_post "{\"name\":\"$prefix\",\"description\":\"Real Terraform plan/apply approval demo; built-in terraform_data only, no cloud resources.\"}" \
        "$BASE/api/v1/projects" | python3 -c "$json_id")
    # State refresh can lose Terraform sensitivity flags on deleted values.
    # Register the secret with DurpDeploy as well as marking it sensitive in HCL.
    api_post '{"name":"TF_VAR_demo_secret","value":"terraform-e2e-sensitive-value","secret":true}' \
        "$BASE/api/v1/projects/$project/variables" | python3 -c "$json_id" >/dev/null
    environment=$ENV_ID
    plan=$(cat <<'SCRIPT'
set -eu
mkdir /tmp/context
cd /tmp/context
# Seed disposable local state so the review has real updates and deletions.
cat > main.tf <<'TF'
variable "demo_secret" {
  type = string
  sensitive = true
}
resource "terraform_data" "modified_demo" {
  input = {
    message = "Original message"
    token = var.demo_secret
  }
}
resource "terraform_data" "removed_demo" {
  input = {
    message = "Removed only after the saved plan is approved"
    token = var.demo_secret
  }
}
TF
terraform init -input=false -no-color
terraform apply -auto-approve -input=false -no-color
cat > main.tf <<'TF'
variable "demo_secret" {
  type = string
  sensitive = true
}
resource "terraform_data" "approval_demo" {
  input = {
    message = "Created only after the saved plan is approved"
    token = var.demo_secret
  }
}
resource "terraform_data" "modified_demo" {
  input = {
    message = "Modified only after the saved plan is approved"
    token = var.demo_secret
  }
}
output "message" {
  value = terraform_data.approval_demo.output.message
  sensitive = true
}
output "modified_message" {
  value = terraform_data.modified_demo.output.message
  sensitive = true
}
TF
terraform plan -input=false -out=tfplan
terraform show -json tfplan > review.json
cp -a /tmp/context "$DURPDEPLOY_STAGE_DIR/context"
SCRIPT
)
    apply=$(cat <<'SCRIPT'
set -eu
cp -a "$DURPDEPLOY_APPROVED_DIR/context" /tmp/context
cd /tmp/context
terraform apply -input=false -no-color "$DURPDEPLOY_APPROVED_DIR/context/tfplan"
test "$(terraform state list)" = "$(printf 'terraform_data.approval_demo\nterraform_data.modified_demo')"
test "$(terraform output -raw modified_message)" = "Modified only after the saved plan is approved"
terraform plan -input=false -no-color -detailed-exitcode
terraform show -json > applied.json
cp terraform.tfstate applied.json "$DURPDEPLOY_STAGE_DIR/"
SCRIPT
)
    payload=$(python3 -c 'import json,sys; print(json.dumps({"name":"Terraform plan","script_body":sys.argv[1],"container_image":sys.argv[2],"approval_artifact_path":"context/tfplan","approval_review_path":"context/review.json","approval_review_format":"terraform"}))' "$plan" "$image")
    api_post "$payload" "$BASE/api/v1/projects/$project/steps" | python3 -c "$json_id" >/dev/null
    payload=$(python3 -c 'import json,sys; print(json.dumps({"name":"Terraform apply","script_body":sys.argv[1],"container_image":sys.argv[2]}))' "$apply" "$image")
    api_post "$payload" "$BASE/api/v1/projects/$project/steps" | python3 -c "$json_id" >/dev/null
    release=$(api_post '{"version":"approval-demo"}' "$BASE/api/v1/projects/$project/releases" | python3 -c "$json_id")
    for action in approve reject pending; do
        deployment=$(api_post "{\"release_id\":$release,\"environment_id\":$environment}" \
            "$BASE/api/v1/projects/$project/deployments" | python3 -c "$json_id")
        for _ in {1..300}; do
            state=$(api_get "$BASE/api/v1/deployments/$deployment/status" | python3 -c 'import json,sys; print(json.load(sys.stdin)["status"])')
            [[ "$state" == awaiting_artifact_approval || "$state" =~ ^(failed|cancelled|cleanup_unconfirmed)$ ]] && break
            sleep 0.2
        done
        [[ "$state" == awaiting_artifact_approval ]] || {
            echo "FAIL: Terraform plan did not pause, status=$state" >&2; return 1;
        }
        gates=$(api_get "$BASE/api/v1/deployments/$deployment/artifact-gates")
        printf '%s' "$gates" | python3 -c 'import json,sys; gates=json.load(sys.stdin); assert len(gates)==1; g=gates[0]; assert g["status"]=="awaiting" and g["review"]=={"create":1,"update":1,"delete":1,"read":0}; assert g["review_verified"] is False'
        checksum=$(printf '%s' "$gates" | python3 -c 'import json,sys; print(json.load(sys.stdin)[0]["sha256"])')
        revision=$(printf '%s' "$gates" | python3 -c 'import json,sys; print(json.load(sys.stdin)[0]["revision"])')
        curl -fsS -H "Authorization: Bearer $API_TOKEN" \
            "$BASE/api/v1/deployments/$deployment/artifact-gates/0/artifact" > "$TMP/terraform-plan"
        python3 -c 'import hashlib,sys; assert hashlib.sha256(open(sys.argv[1],"rb").read()).hexdigest()==sys.argv[2]' "$TMP/terraform-plan" "$checksum"
        local page
        page=$(curl_body "$BASE/deployments/$deployment/artifact-gates")
        [[ "$page" == *'Artifact review'* && "$page" == *'Approve and continue'* ]] || {
            echo 'FAIL: Terraform approval controls missing from web page' >&2; return 1;
        }
        page=$(curl_body "$BASE/deployments/$deployment")
        [[ "$page" != *'Sensitive script hidden'* && "$page" == *'set -eu'* ]] || {
            echo 'FAIL: Terraform script source hidden on deployment page' >&2; return 1;
        }
        api_get "$BASE/api/v1/deployments/$deployment/logs.txt" | python3 -c 'import sys; text=sys.stdin.read(); assert "Terraform will perform the following actions" in text; assert "terraform-e2e-sensitive-value" not in text'
        api_get "$BASE/api/v1/deployments/$deployment/artifact-gates/0/review" | python3 -c '
import json,sys
d=json.load(sys.stdin)
assert d["review_source"]=="step_output" and d["review_verified"] is False
resources={r["address"]:r for r in d["resources"]}
assert len(resources)==3
created=resources["terraform_data.approval_demo"]
modified=resources["terraform_data.modified_demo"]
removed=resources["terraform_data.removed_demo"]
assert created["actions"]==["create"]
assert modified["actions"]==["update"]
assert removed["actions"]==["delete"]
assert json.loads(created["after"])["input"]["message"]=="Created only after the saved plan is approved"
assert json.loads(modified["before"])["input"]["message"]=="Original message"
assert json.loads(modified["after"])["input"]["message"]=="Modified only after the saved plan is approved"
assert json.loads(removed["before"])["input"]["message"]=="Removed only after the saved plan is approved"
for resource,field in [(created,"after"),(modified,"before"),(modified,"after"),(removed,"before")]:
    assert json.loads(resource[field])["input"]["token"]=="[REDACTED]"
assert "terraform-e2e-sensitive-value" not in json.dumps(d)
'
        page=$(curl_body "$BASE/deployments/$deployment/artifact-gates/0/review")
        [[ "$page" == *'data-terraform-plan'* && "$page" == *'terraform_data.approval_demo'* && "$page" == *'terraform_data.modified_demo'* && "$page" == *'terraform_data.removed_demo'* && "$page" == *'[REDACTED]'* && "$page" != *'terraform-e2e-sensitive-value'* ]] || {
            echo 'FAIL: readable Terraform review missing or sensitive value exposed' >&2; return 1;
        }
        if [[ "$action" == pending ]]; then
            echo "  Project retained: $prefix ($BASE/projects/$project)"
            echo "  Awaiting your approval: $BASE/deployments/$deployment (expires after 24 hours)"
            continue
        fi
        if [[ "$action" == approve ]]; then
            code=$(curl_silent -X POST \
                -d "revision=$revision&sha256=$checksum&csrf_token=$CSRF" \
                "$BASE/deployments/$deployment/artifact-gates/0/approve")
            [[ "$code" == 303 ]] || { echo "FAIL: web Terraform approval got $code" >&2; return 1; }
            for _ in {1..300}; do
                state=$(api_get "$BASE/api/v1/deployments/$deployment/status" | python3 -c 'import json,sys; print(json.load(sys.stdin)["status"])')
                [[ "$state" =~ ^(succeeded|failed|cancelled|cleanup_unconfirmed)$ ]] && break
                sleep 0.2
            done
            [[ "$state" == succeeded ]] || { echo "FAIL: approved Terraform apply status=$state" >&2; return 1; }
            echo "  Approved and applied: $BASE/deployments/$deployment"
        else
            code=$(api_post_code "{\"revision\":$revision,\"sha256\":\"$checksum\"}" \
                "$BASE/api/v1/deployments/$deployment/artifact-gates/0/reject")
            [[ "$code" == 200 ]] || { echo "FAIL: Terraform rejection got $code" >&2; return 1; }
            state=$(api_get "$BASE/api/v1/deployments/$deployment/status" | python3 -c 'import json,sys; print(json.load(sys.stdin)["status"])')
            [[ "$state" == rejected ]] || { echo "FAIL: rejected Terraform status=$state" >&2; return 1; }
            echo "  Rejected: $BASE/deployments/$deployment"
        fi
    done
}

terraform_approval_e2e
