set -euo pipefail
umask 077

: "${DURPDEPLOY_STAGE_DIR:?Managed staging is required}"
: "${DURPDEPLOY_APPROVED_DIR:?An approved plan is required}"

approved_context="$DURPDEPLOY_APPROVED_DIR/terraform-context"
if [[ ! -f "$approved_context/tfplan" ||
      ! -f "$approved_context/.terraform.lock.hcl" ]]; then
    echo 'The approved plan and provider lock file are required' >&2
    exit 1
fi

# Use only the approved configuration and binary plan, never the live source.
working_context=$(mktemp -d "${TMPDIR:-/tmp}/durpdeploy-terraform-apply.XXXXXX")
trap 'rm -rf -- "$working_context"' EXIT
cp -R --preserve=mode,timestamps -- "$approved_context/." "$working_context/"
cd "$working_context"
export TF_IN_AUTOMATION=1
terraform apply -input=false -no-color -lock-timeout=60s \
    "$DURPDEPLOY_APPROVED_DIR/terraform-context/tfplan"
echo 'Approved Terraform plan applied'
