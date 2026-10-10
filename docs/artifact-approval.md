# Artifact approval

Local Docker/Podman steps can save an artifact and pause before a later step.
Set `approval_artifact_path` and `approval_review_path` to distinct relative
paths under `DURPDEPLOY_STAGE_DIR`. Set `approval_review_format` to `terraform`
for `terraform show -json`, or `summary` for nonnegative `create`, `update`,
`delete`, and `read` counts in a nonempty JSON object. Omitted counts are zero;
unknown keys, null counts, and non-object summaries are rejected.
The count summary is available to viewers. Write-capable project members can
open **View resource changes** on the deployment page to inspect Terraform
resource addresses, actions, and before/after values. Sensitive subtrees are
masked using Terraform's sensitivity metadata; missing or malformed metadata
hides the affected subtree. Known secret release variables are scrubbed too.
Unknown values are labelled `(known after apply)`. Configuration, variables,
and outputs are omitted from the resource review. Gated scripts and logs use
the same display rules as other deployments, including normal secret-variable
log redaction. Terraform's JSON output can contain plaintext sensitive values;
write it to the review file rather than printing it. Both paths and an
explicit review format are required together; an empty format is rejected.

Review counts are unverified claims supplied by the deployment step. The API
reports `review_source: "step_output"` and `review_verified: false`. A separate
review file can be stale or misleading even when every checksum is valid.
Before approval, download and independently inspect the exact artifact using
trusted tools. For Terraform, run `terraform show` with a trusted toolchain and
providers, then compare the result with the review. Checksums bind immutable
bytes; they do not establish that the summary accurately describes those bytes.
The rotation CLI re-encrypts retained artifact chunks in the same transaction
as variables, package credentials, and verification targets. It currently skips
TOTP seeds, stored agent identity ciphertext, and remote log scrub buffers;
do not rotate instances containing those records. See the
[rotation limitation](security.md#key-rotation-runbook).
`--plaintext` cannot migrate encrypted artifact gates, package credentials,
or verification targets. Eligible instances must stop the server for rotation
and install the exact CLI-generated key before restarting.
Generation and continuation scripts, container images, and credentials belong
to the existing trusted-team deployment model. Gates do not constrain a
malicious script to only plan, or prove that it applies the approved artifact.

Set `network_mode: "bridge"` on each step that needs network access. The
default remains `none`. Host networking, privileged containers, and host
mounts are not allowed. Network access can reach services accessible from
the container network, so grant it only to trusted scripts.

For Terraform, use an image containing Bash and Terraform. The generation
step can run:

```bash
mkdir /tmp/terraform-context
cd /tmp/terraform-context
# Write main.tf here, including the remote backend configuration.
terraform init -input=false
terraform plan -input=false -out=tfplan
terraform show -json tfplan > review.json
cp -a /tmp/terraform-context/. "$DURPDEPLOY_STAGE_DIR/"
```

Configure paths `tfplan` and `review.json`. The later apply step runs:

```bash
cp -a "$DURPDEPLOY_APPROVED_DIR/." /tmp/approved-context
cd /tmp/approved-context
terraform apply -input=false "$DURPDEPLOY_APPROVED_DIR/tfplan"
```

Staging is `noexec`. Execute Terraform and provider plugins from `/tmp`, then
copy the generated files into staging. Gated generation and continuation steps
have 364 MiB of bounded executable scratch space; other steps have 64 MiB.
The approved directory is read-only. The copied working directory preserves
configuration, initialized backend data, modules, and provider lock files.
Terraform applies the saved plan; it must not generate a replacement plan.
Terraform rejects stale backend state. Follow the backend's locking guidance.
[Saved plans](https://developer.hashicorp.com/terraform/cli/commands/apply)
and [HTTP backend locking](https://developer.hashicorp.com/terraform/language/backend/http)
describe these contracts. Review JSON may contain plaintext sensitive values;
never publish it as a log or separate public artifact.

The server pins every step image ID and release variables before generation.
Stopped container references retain those exact images through approval waits
and server restarts, including locally built images. Minute maintenance removes
the references after terminal decisions. Explicit container removal can remove
these references; image pruning alone preserves them.
It encrypts the whole staging context in authenticated database chunks.
Unlinked temporary descriptors are used during capture, restore, and download.
The bundle is limited to 300 MiB; review JSON is limited to 1 MiB. Unsafe
archive entries, missing files, and altered ciphertext or checksums fail closed.

An administrator reviews and approves the exact revision and SHA-256 in the
deployment page or API once `decision_ready` is true. A saved gate can have
status `awaiting` while its deployment is still `publishing_artifact` and
cleaning up containers. The decision buttons appear only after cleanup has
finished and the deployment is `awaiting_artifact_approval`. The panel polls
every three seconds, so no manual refresh is needed. Failed web decisions
return to deployment details with a warning and current gate state; failed
decisions do not create successful approval/rejection audit entries.

Approval expires after 24 hours. Project members with write access can download
the sensitive artifact; viewers can see the count summary only.
Rejection, cancellation, and expiry stop the deployment.
The minute worker records expiry and advances queues; polling reads state only.
Expired artifacts and decisions are blocked immediately, before that worker runs.
Terminal bundle chunks are deleted after seven days. Audit records retain the
decision, and gate records retain checksums, counts, times, and approver ID.

An active gated deployment reserves its project/environment pair, including
while waiting. Later jobs for that pair enter its FIFO queue. Other projects
can deploy to the same environment concurrently, and that project can deploy
to other environments concurrently. Approval resumes the
same deployment without giving up its slot; rejection, cancellation, or expiry
releases the slot for the next deployment. Gates
require local steps with no automatic retries, a later continuation step, and
a deployment release. Runbooks, remote agents, and mixed execution are excluded.
Restart resumes a waiting review or an approved pending continuation; a crash
during execution fails the run rather than replaying an uncertain apply.

API routes under `/api/v1/deployments/{id}/artifact-gates`:

- `GET /`: review metadata and counts.
- `GET /{stepIndex}/artifact`: exact binary download, with `Cache-Control: no-store`.
- `GET /{stepIndex}/review`: writer-only redacted Terraform resource changes,
  with `review_source: "step_output"`, `review_verified: false`, and
  `Cache-Control: no-store`. The web equivalent renders the same changes on
  demand. Summary-format gates return an empty resource list.
- `POST /{stepIndex}/approve`: `{"revision":1,"sha256":"..."}`.
- `POST /{stepIndex}/reject`: the same checksum-bound body.

Step indices start at zero. Decisions require an administrator API token or
an administrator session with CSRF. Duplicate or stale decisions return 409.
Changing steps requires a new release and deployment; approval is never reused.
