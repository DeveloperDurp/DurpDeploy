# Artifact approval

Local Docker/Podman steps can save an artifact and pause before a later step.
Set `approval_artifact_path` and `approval_review_path` to distinct relative
paths under `DURPDEPLOY_STAGE_DIR`. Set `approval_review_format` to `terraform`
for `terraform show -json`, or `summary` for nonnegative `create`, `update`,
`delete`, and `read` counts. Reviews expose counts only. Generated names,
values, outputs, and all gated script output are hidden.

Set `network_mode: "bridge"` on each step that needs network access. The
default remains `none`. Host networking, privileged containers, and host
mounts are not allowed. Network access can reach services accessible from
the container network, so grant it only to trusted scripts.

For Terraform, use an image containing Bash and Terraform. The generation
step can run:

```bash
cd "$DURPDEPLOY_STAGE_DIR"
# Write main.tf here, including the remote backend configuration.
terraform init -input=false
terraform plan -input=false -out=tfplan
terraform show -json tfplan > review.json
```

Configure paths `tfplan` and `review.json`. The later apply step runs:

```bash
cp -a "$DURPDEPLOY_APPROVED_DIR/." /tmp/approved-context
cd /tmp/approved-context
terraform apply -input=false "$DURPDEPLOY_APPROVED_DIR/tfplan"
```

The approved directory is read-only. The copied working directory preserves
configuration, initialized backend data, modules, and provider lock files.
Terraform applies the saved plan; it must not generate a replacement plan.
Terraform rejects stale backend state. Follow the backend's locking guidance.
[Saved plans](https://developer.hashicorp.com/terraform/cli/commands/apply)
and [HTTP backend locking](https://developer.hashicorp.com/terraform/language/backend/http)
describe these contracts. Review JSON may contain plaintext sensitive values;
never publish it as a log or separate public artifact.

The server pins every step image ID and release variables before generation.
It encrypts the whole staging context in authenticated database chunks.
Unlinked temporary descriptors are used during capture, restore, and download.
The bundle is limited to 300 MiB; review JSON is limited to 1 MiB. Unsafe
archive entries, missing files, and altered ciphertext or checksums fail closed.

An administrator reviews and approves the exact revision and SHA-256 in the
deployment page or API. Project members with write access can download the
sensitive artifact; viewers can see the count summary only. Approval expires
after 24 hours. Rejection, cancellation, and expiry stop the deployment.
Terminal bundle chunks are deleted after seven days. Audit records retain the
decision, and gate records retain checksums, counts, times, and approver ID.

An active gated deployment reserves its environment, including while waiting.
Conflicting admissions return 409; this is not a FIFO deployment queue. Gates
require local steps with no automatic retries, a later continuation step, and
a deployment release. Runbooks, remote agents, and mixed execution are excluded.
Restart resumes a waiting review or an approved pending continuation; a crash
during execution fails the run rather than replaying an uncertain apply.

API routes under `/api/v1/deployments/{id}/artifact-gates`:

- `GET /`: review metadata and counts.
- `GET /{stepIndex}/artifact`: exact binary download, with `Cache-Control: no-store`.
- `POST /{stepIndex}/approve`: `{"revision":1,"sha256":"..."}`.
- `POST /{stepIndex}/reject`: the same checksum-bound body.

Step indices start at zero. Decisions require an administrator API token or
an administrator session with CSRF. Duplicate or stale decisions return 409.
Changing steps requires a new release and deployment; approval is never reused.
