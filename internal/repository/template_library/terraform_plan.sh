set -euo pipefail
umask 077

: "${DURPDEPLOY_STAGE_DIR:?Managed staging is required}"
: "${TF_CONFIG_DIR:?Set TF_CONFIG_DIR}"

staged_context="$DURPDEPLOY_STAGE_DIR/terraform-context"
if [[ -e "$staged_context" || -L "$staged_context" ]]; then
    echo 'Terraform context already exists in managed staging' >&2
    exit 1
fi

if [[ "$TF_CONFIG_DIR" != /* || ! -d "$TF_CONFIG_DIR" ||
      ! -f "$TF_CONFIG_DIR/.terraform.lock.hcl" ]]; then
    echo 'Use an absolute configuration directory with a committed provider lock file' >&2
    exit 1
fi

# Start from configuration only. Never import local state or cached credentials.
context=$(mktemp -d "${TMPDIR:-/tmp}/durpdeploy-terraform-plan.XXXXXX")
trap 'rm -rf -- "$context"' EXIT
export TF_PLAN_CONTEXT="$context"
python3 - <<'PY'
import os
import shutil

source = os.path.realpath(os.environ["TF_CONFIG_DIR"])
destination = os.path.realpath(os.environ["TF_PLAN_CONTEXT"])
if os.path.commonpath([source, destination]) in (source, destination):
    raise SystemExit("Keep Terraform configuration outside deployment staging")
shutil.copytree(source, destination, symlinks=True,
                dirs_exist_ok=True,
                ignore=shutil.ignore_patterns(
                    ".git", ".terraform", "terraform.tfstate*", "*.tfplan",
                    "tfplan", "review.json"))
PY

cd "$context"
export TF_IN_AUTOMATION=1
terraform init -input=false -no-color -lockfile=readonly

# A local backend would lose durable state when managed staging is cleaned.
python3 - <<'PY'
import json
from pathlib import Path

metadata = Path(".terraform/terraform.tfstate")
backend = json.loads(metadata.read_text()).get("backend", {}) if metadata.exists() else {}
if backend.get("type") in (None, "local"):
    raise SystemExit("Configure a remote backend with state locking before deployment")
PY

terraform validate -no-color
terraform plan -input=false -no-color -lock-timeout=60s -out=tfplan
terraform show -json tfplan > review.json
# Preserve executable modes, not private SELinux labels from scratch space.
# Claim the destination without replacing context created during planning.
mkdir -- "$staged_context"
cp -R --preserve=mode,timestamps --sparse=never --reflink=never \
    -- "$context/." "$staged_context/"
echo 'Terraform plan is ready for human approval. No infrastructure was applied.'
