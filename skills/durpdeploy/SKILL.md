---
name: durpdeploy
description: Use when an agent needs to operate a DurpDeploy instance over HTTP — authenticate (API bearer token or web session + CSRF), create projects, steps, variables, and releases, deploy, poll deployment status, read logs, cancel, approve, or verify deploy behavior end to end. Covers the deploy flow, lifecycle and approval gates, status codes, and the CLI. Not for contributing DurpDeploy source code (see its AGENTS.md).
metadata:
  audience: agents operating a running DurpDeploy server
---

# Operating DurpDeploy over HTTP

DurpDeploy is a single-binary deployment tool that runs ordered script steps
against environments. It exposes three surfaces an agent may use:

- **JSON API** at `/api/v1/*` — bearer-token auth, no CSRF. **Prefer this.**
- **Web UI routes** (`/login`, `/projects`, …) — session cookie + CSRF token,
  HTML form semantics, redirect status codes.
- **CLI subcommands** on the `durpdeploy` binary (user/token admin).

The authoritative endpoint reference is the Swagger UI at `/api/swagger/`
(no auth) on a running server. Everything here is verified, current
behavior; when in doubt, check swagger.

## Base URL

Replace `$BASE` (e.g. `https://durpdeploy.example.com` or
`http://localhost:8080`) throughout. The server listens on `:8080` by
default behind a reverse proxy in production. `/healthz` is the public
liveness check — use it to confirm reachability first.

## CLI

Run on the host with the server's database (matches `DURPDEPLOY_DB`:

```bash
durpdeploy admin create --email X --password Y   # first admin, runs migrations
durpdeploy tokens create --user admin@example.com --name ci   # prints token ONCE
durpdeploy tokens list --user admin@example.com
durpdeploy audit prune --days 180
```

## Auth

### API tokens (recommended for agents)

Mint a token on the server host with the CLI above, or in the web UI at
`/settings/tokens` if scripting: POST the form `name=<label>&csrf_token=$CSRF`,
then GET the redirect Location (an opaque single-use flash reference,
`/settings/tokens?flash=<id>` — never the token itself) and take the plaintext
from the one-time banner in that page's body. The flash is consumed on first
read and expires if abandoned. Then:

```bash
TOKEN="ddp_pat_..."
api_get()  { curl -s -H "Authorization: Bearer $TOKEN" "$@"; }
api_post() { curl -s -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
                  -X POST -d "$1" "$2"; }
api_get "$BASE/api/v1/users/me"    # sanity check: who am I
```

- No CSRF, no cookies. Errors are JSON envelopes (`400/401/404/422`).
- Tokens inherit the owner's role. `viewer` tokens can read but never
  write (writes are rejected); `deployer` and `admin` can write.
- Browser MFA protects web logins only — API tokens are single bearer
  credentials and keep working.

### Web session + CSRF (only when driving the UI)

```bash
curl -c /tmp/dd-cookies -X POST \
    -d "email=you@example.com&password=SECRET" "$BASE/login"    # 303 on success
CSRF=$(curl -s -b /tmp/dd-cookies "$BASE/" \
    | grep -oP '<meta name="csrf-token" content="\K[^"]+' | head -1)
```

Token placement rules:

- POST/PUT/PATCH: append `csrf_token=$CSRF` to the form body.
- DELETE: pass the header `X-CSRF-Token: $CSRF` — Go does not parse form
  bodies on DELETE, so a body-only token 403s.
- Status contract: `303` = success (id or path in the `Location` header),
  `422` = validation failure, `403` = missing CSRF or viewer write.

## Agent fleet (admin only)

All paths below use the `/api/v1` prefix and require an admin token.

- `GET /admin/agents` lists agents with `administrative_status`, `draining`, `health`, `current_work`,
  `queued_work`, `last_successful_deployment`, `last_error`, `server_version`,
  `recommended_agent_version`, and `compatibility`.
- `GET /admin/agents/$ID` also includes capability `labels` and
  `environment_labels` (environment routing labels).
- `POST /admin/agents/$ID/drain` and `POST /admin/agents/$ID/resume` return
  `204`, including repeats; `404` if missing and `409` unless active/paired.
  Drain blocks new claims, retains waiting work, and allows already-issued
  work to finish. Later remote steps wait for Resume. State survives restart.
  Waiting on drained targets pauses their timeout; issued work still times out.
  `administrative_status` is `draining` while issued work remains, then `drained`
  once none remains. Both retain `draining: true` and block claims until Resume.
  The existing `status` field keeps the base registration state (e.g. `active`).
- `POST` or `DELETE /admin/agents/$ID/environments` accepts
  `{"environment_id":N}` and returns `204`; invalid IDs return `400`,
  missing resources return `404`. Repeated add is harmless.
- Capability label changes use `POST` or `DELETE /admin/agents/$ID/labels`
  with `{"label":"linux"}`. Routing changes and Drain/Resume are audited.

Health is `healthy`, `stale` (120 seconds), `offline` (600 seconds), or
`unknown`. It is separate from administrative status and uses server time.
Before the first heartbeat, health deadlines use the latest completed pairing
time, so re-pairing starts a new grace period. Draining agents still heartbeat.
Global notifications emit each stale/offline
transition and recovery once; delivery is best effort and does not block claim
maintenance. Alerts stay ordered; a full in-memory queue skips new alerts and
logs a warning without retry. Last error is a stable
outcome code with a deployment link for redacted logs. Compatibility confirms
the observed supported protocol, while agent versions remain unverified.

## Deploy flow (the common ask)

Deployments and runbook executions share a durable FIFO queue per environment.
Only one item owns the environment, including while it waits for agents or
verification. Later eligible items have status `queued`; approval waits do not
own a slot. Approval makes work eligible in original creation order without
preempting active work. Different environments can execute independently.

Deployment GET/status responses and runbook execution GET responses include
`queue_position` (1-based for queued work, otherwise 0), plus
`active_deployment_id` and `active_work_url` when that work is visible to the
caller. Viewers can read queue state. Authorized writers can cancel queued
work through the existing cancel endpoint; this does not signal the active
deployment. Server restart preserves order and repairs missed launches.
Queued runbook cancellation returns `status: "cancelled"`; active runbook
cancellation returns `status: "cancellation_requested"` while stopping work.
Unconfirmed container cleanup, lost agents, or unacknowledged cancellation
keep the environment blocked: a timeout is not proof that execution stopped.

`GET /api/v1/deployments/$ID/status` includes `waiting_for_agents`. It is
`true` while remote work is queued with no issued claim, and `false` once
claimed or terminal. The web deployment page shows “Waiting for agents”
while this flag is true; polling updates it automatically.

1. **Project** `POST /api/v1/projects` `{"name":"my-app"}` → reply has `id`.
2. **Environment** `POST /api/v1/environments` `{"name":"prod"}` → `id`.
   Environments can carry tags; agents can be routed by tag later.
3. **Steps** `POST /api/v1/projects/$PID/steps`:

```json
{
  "name": "build", "script_body": "make build",
  "interpreter": "bash",
  "sort_order": 1, "timeout_seconds": 300, "max_retries": 0,
  "execution_target": "local",
  "container_image": "registry.example/build@sha256:<digest>",
  "variable_names": ["API_URL"]
}
```

`local` means mandatory server-container execution; specify an image with
`bash`, `pwsh`, or `python3` installed. `agent` runs on matching remote agents.
`agent_execution_mode` defaults to `host`, which rejects a container image.
For remote containers, set `execution_target: "agent"`,
`agent_execution_mode: "container"`, and a valid `container_image`.
This requires an `agent/3` agent reporting a ready Docker or Podman runtime
and support for the selected container interpreter. API requests may use `powershell`; DurpDeploy
normalizes it to `pwsh`. All resolved release variables enter a step by
default; set `variable_names` only to restrict the step to those names. Local
steps exclude container/SSH client configuration names such as `PATH`, `HOME`,
`SSH_AUTH_SOCK`, or `XDG_*`; agent-host steps retain their host variable support.
Agent-container steps apply the same reserved-name restrictions as local containers.
Steps, template versions, releases, and runbook versions preserve these fields.
An empty `variable_names` list passes all resolved variables; a non-empty
list restricts that step, including remote host steps.
The admin agent API reports `execution_modes`, `container_runtimes`, and
`container_interpreters` from the last valid poll.
Upgrade the server before enabling agent containers. Keep old agents on host
steps. Do not downgrade the server while container work is pending or cleanup
is unresolved. Downgrading an agent clears its container capabilities and
fails its incompatible waiting work before claim.
An agent result of `cleanup_unconfirmed` blocks the environment queue and
retry until that agent reconciles its containers and reports a ready v3 poll.
The original terminal result remains visible after confirmation.
Re-pairing a revoked agent returns `409` while its remote cleanup remains
unconfirmed. Reconcile its workloads before revocation; a replacement
installation cannot confirm cleanup for the old installation.
After a heartbeat or cancellation timeout, the same paired agent can replay
its durable terminal report with the original claim token. This resolves the
remote uncertainty without changing a failed deployment to success. A late
`cleanup_unconfirmed` report still requires a ready v3 poll before retry.
The embedded agent pulls an image when it is missing. Container steps default
to no network; local steps can opt into `network_mode: "bridge"`. They have no
host mounts. A mutable image tag does not
freeze image contents; prefer a digest. The web/API rejects a new image-less
server step. Old image-less releases remain readable but cannot run, re-run,
or refresh; recreate their steps and create a new release (`409` on launch).

4. **Variables** (optional) `POST /api/v1/projects/$PID/variables`
   `{"name":"API_URL","value":"...","environment_id":N}` — resolved at
   deploy time for the target environment. Omit `environment_id` (or use
   `null`) for Unscoped. On projects bound to a lifecycle, create and update
   accept only its stage environments; other IDs return `422`. Projects
   without a lifecycle can use any environment.
5. **Release** (snapshot of current steps + variables)
   `POST /api/v1/projects/$PID/releases` `{"version":"1.2.0"}` → `id`.
   Later step edits do NOT affect it; `POST /projects/$PID/releases/$RID/refresh`
   re-snapshots the release, including after failed or successful deployments.
   Active or unconfirmed deployments and buffered agent logs return `409`;
   wait for completion and log flushing, then retry.
   Existing deployment steps and pinned artifacts stay unchanged. New
   deployments use the refreshed release; re-runs use the original deployment
   steps and artifact pin, with the release's current variables.
   Refresh also cannot upgrade an old image-less release.
   `DELETE /api/v1/projects/$PID/releases/$RID` returns `204` when removed,
   `404` if absent or in another project, and `409` if it has active or
   unconfirmed deployments. It also deletes schedules and terminal deployment
   history, including logs; this cannot be undone.
6. **Deploy**
   `POST /api/v1/projects/$PID/deployments`
   `{"release_id":$RID,"environment_id":$EID}` → `201` with deployment `id`.
7. **Poll** until terminal or operator action is required:

```bash
for i in {1..150}; do
  S=$(api_get "$BASE/api/v1/deployments/$DID/status" | python3 -c 'import sys,json;print(json.load(sys.stdin)["status"])')
  [[ "$S" =~ ^(failed|succeeded|cancelled|rejected|expired|cleanup_unconfirmed|pending_approval|awaiting_artifact_approval)$ ]] && break
  sleep 0.5
done
```

8. **Act on state**:
   - `awaiting_artifact_approval` → list
     `GET /api/v1/deployments/$DID/artifact-gates`, use the current gate's
     `step_index` as `$STEP`, download `/$STEP/artifact` under that gate URL,
     and independently inspect it with trusted tools.
     Review counts are unverified step claims. An admin then sends the listed
     revision and artifact SHA-256 to `/$STEP/approve` or `/$STEP/reject`
     under that gate URL. After approval, return to polling; another step can
     require another review. Follow the artifact-gate details below.
   - `rejected` or `expired` → stop polling. Resolve the rejection or expiry,
     then redeploy to generate a fresh artifact and review.
   - `pending_approval` → an admin `POST /api/v1/deployments/$DID/approve`
     with an empty body or `{}` unblocks it. The authenticated admin is
     recorded as the approver. Non-admin tokens get 403.
   - failure → `GET /api/v1/deployments/$DID/logs` (JSON lines, secrets are
      redacted) and `GET /.../logs.txt`; fix and create a new release, then redeploy with
     `POST /api/v1/deployments/$DID/redeploy`.
    - `POST /api/v1/deployments/$DID/cancel` cancels queued work or requests
      cancellation of a running deploy.
    - `cleanup_unconfirmed` means container removal failed. Retry and redeploy
      return `409`; do not re-execute until a successful startup runtime sweep
      changes the deployment to `failed`.

## Post-deployment verification and rollback

Only global admins can configure verification, because environments are
shared across projects. `POST /api/v1/environments` and
`PUT /api/v1/environments/$EID` accept:

```json
{
  "name": "prod",
  "verification_type": "http",
  "verification_target": "https://service.example.com/health",
  "verification_timeout_seconds": 30
}
```

Types are `""` (disabled), `http`, and `bash`. Timeout defaults to 30 seconds
and must be 1–300. An update that omits verification fields preserves them.
Environment responses omit `verification_target` for non-admin callers.
Targets and frozen verification snapshots are encrypted at rest and included
in secret-key rotation. Bash snapshots retain selected variables without storing a
second plaintext copy of the check script. Explicit HTTP ports must be 1–65535.
The configuration is frozen on deployment creation, including deployments
waiting for approval. HTTP performs one server-side GET; only 2xx succeeds.
Redirects, URL user credentials, loopback, link-local/metadata addresses
(including Alibaba ECS's `100.100.100.200` and AWS's `fd00:ec2::254`),
environment proxies, and DNS rebinding are rejected; private unicast service
addresses are allowed. Use a release secret variable for sensitive Bash values;
arbitrary literals embedded in a script are not automatically identified as secrets.

Bash runs after all steps in a fixed, digest-pinned official Bash 5.2 server
container, using the last step's selected variables and artifact mount. Project
images and agents never receive the check script, including for older snapshots.
The server container runtime is required even for all-agent deployments. Tools
from project images and remote host/network access are unavailable. Containers
retain the existing no-network restrictions; Bash uses privileged startup mode,
with no retries. Shell startup and loader variables (`BASH_ENV`, `ENV`, `SHELLOPTS`,
`BASHOPTS`, `CDPATH`, `GLOBIGNORE`, `PS4`, `GCONV_PATH`, `GLIBC_TUNABLES`, and `LD_*`)
are excluded by default and rejected when explicitly selected. Bash verification
requires at least one step.
An empty-step Bash deployment is rejected with 422. A scheduled attempt with
this configuration is disabled with an actionable `last_error`.
Verification output uses the same secret scrubber as deployment logs.
HTTP output also redacts the frozen URL, path segments, and query names and values,
including their URL-encoded forms. Hostnames and DNS labels are redacted without
case sensitivity, including Unicode and IDNA forms, so an echoed request cannot
expose configuration credentials.
Failure marks the deployment failed, records a verification audit event, and
emits the standard failure notification. Cancellation waits for container
cleanup as normal.

`GET /api/v1/deployments/$DID/verification` returns safe metadata (type,
timeout, status, timestamps); commands and URLs are omitted. A deployment
without a check returns `{"status":"disabled"}`. The deployment detail page
also shows verification status and redacted output in its logs.

Rollback is manual:

1. `GET /api/v1/deployments/$DID/rollback` previews exact source and target
   versions, environment, and lifecycle/approval state. It selects the latest
   earlier successful different release in the same project/environment.
2. Confirm `target_deployment_id` from that preview with
   `POST /api/v1/deployments/$DID/rollback` and
   `{"target_deployment_id":123}`. Success returns `201` with a **new**
   deployment. The web confirmation is `/deployments/$DID/rollback`.
3. Poll, approve, cancel, and inspect that new deployment as usual.

The source must be the latest terminal deployment and no execution in that
project/environment may still be active or unconfirmed. Missing history,
stale confirmation, or an overlapping submission returns `409`; a lifecycle
gate returns `422`. Rollback cannot force a gate. Viewers cannot submit it;
project authorization and admin-only approval still apply. The new deployment
reuses the prior successful deployment's frozen steps and package pin and the
release's current snapshot of variables, then runs the environment's current
verification. An explicit release refresh replaces those variables for future
deployments, re-runs, and rollbacks; historical variable values cannot be
reconstructed. It records rollback provenance and an audit entry.
Mutable container image tags
also remain mutable; use digests when exact image contents matter.

## Gates (know the 422s)

Enforced identically on web and API (source: `internal/gate/gate.go`):

- **Lifecycle order**: projects bound to a lifecycle deploy along its stage
  order. Skipping the previous stage → `422` (bypassable with `force=true`
  on the deploy request). Deploying to an environment outside the
  lifecycle → `422` and **force does not help**.
- **Approval**: stages with `requires_approval` park at `pending_approval`
  instead of running. Approve is admin-only.
- **Membership**: non-admin tokens need to be in the project; unknown or
  forbidden projects return `404`/`403`.
- **Cross-project release**: deploying a release id under the wrong
  project → `400`.

## Scheduling

`POST /api/v1/projects/$PID/schedules` with `release_id`, `environment_id`,
5-field `cron` (e.g. `* * * * *`), optional `note`, `enabled=true`. The
scheduler ticks once per minute — never test sub-minute cron expectations.
Schedule creation and updates return `404` if the selected release is deleted
before the write commits.
An old image-less server release cannot be scheduled. Existing schedules
pointing at one disable on their due run and expose an actionable `last_error`.

## Request bodies

API JSON bodies and `/api/lint` bodies are limited to **4 MiB (4,194,304
encoded bytes)**, including all script text, escaping, and JSON overhead.
Send exactly one JSON object matching the documented request schema. Unknown
fields (including nested fields), top-level `null`, additional documents, and
trailing non-whitespace data return `400 {"error":"Invalid JSON body"}`.
Oversized input returns `413 {"error":"Request body too large"}` before any
mutation, including chunked requests. Whitespace after the object is allowed
and counts toward the ceiling. Control actions with no fields accept an empty
body or `{}`; additional fields are rejected.

General web request bodies, including multipart bodies, have a **16 MiB encoded
byte** ceiling and return 413 when exceeded. Package repository forms have a
64 KiB ceiling. Login and password reauthentication retain their separate
64 KiB limits and existing 400/422 error responses. Scripts must fit within
the applicable ceiling after JSON or form encoding; runbooks share one JSON
ceiling across all steps. These are server limits; proxies may impose lower
limits.

## Runbooks

Runbooks save immutable versions of ordered steps. Create one with
`POST /api/v1/projects/$PID/runbooks` and a JSON body containing `name` and
`steps` (`name`, `script_body`, optional `interpreter`, `timeout_seconds`,
`max_retries`, `execution_target`, `agent_selectors`, `container_image`, and
`variable_names`). Local runbook steps require an image. Save the next
version with `PUT /api/v1/projects/$PID/runbooks/$BID` and `steps`.

`POST /api/v1/projects/$PID/runbooks/$BID/executions` takes
`environment_id` and optional `version_id`; omit the version or pass `0`
for the latest. Its response includes the runbook execution `id` and the
underlying `deployment_id`. Read the execution at
`/api/v1/projects/$PID/runbook-executions/$XID`, logs at `/logs`, and live
logs at `/logs/stream` (SSE by default, `?format=ndjson` for NDJSON).

HTTP headers must arrive within 5 seconds, and the complete request body
within 30 seconds from the start of the request read. Ordinary responses
have a 60-second write budget. Deployment/runbook SSE and NDJSON streams
have no total lifetime limit, but each event write/flush must finish within
60 seconds. Consume streams continuously and reconnect after disconnects.
An operator's reverse proxy may impose additional limits.

Execution actions are `POST .../$XID/cancel`, `/approve` (admin only),
and `/retry` (after a terminal status). Retry returns `409` while the source
execution has a lost or unconfirmed remote outcome; inspect the agent before
retrying. Retry also returns `409` for `cleanup_unconfirmed` until the next
successful startup runtime sweep changes the deployment to `failed`.

`GET /api/v1/projects/$PID/runbook-executions?limit=100&offset=0`
returns `{items, total, limit, offset}`. The default page has 100 items;
the maximum is 1000. `POST /api/v1/projects/$PID/runbooks/$BID/schedules`
takes `environment_id`, five-field `cron`, and optional `version_id`.
Omit the version or pass `0` to follow the latest saved version. Disable a
schedule with `POST .../schedules/$SID/disable`.

The web UI starts at `/projects/$PID/runbooks`. It supports editing,
execution history, live logs, approval, cancellation, retry, and schedules.
Deleting a project with a pending, running, approval-gated, or unconfirmed
remote runbook returns `409`. Approve an approval-gated execution, then wait
for a confirmed terminal status (or cancel it after it starts) before deleting
the project. An unconfirmed remote outcome needs operator inspection.
Deleting an environment with any active or unconfirmed remote deployment
also returns `409`.
Once deployments are terminal, environment deletion removes their history.

## Endpoint cheat sheet

| Resource | Endpoints |
|----------|-----------|
| Projects | `GET/POST /api/v1/projects`, `GET/PUT/DELETE /projects/{id}` |
| Environments | same shape under `/environments` |
| Steps | `/api/v1/projects/{id}/steps[/{stepId}]` (`POST/GET/PUT/DELETE`, `PATCH /steps/reorder`) |
| Variables | `/api/v1/projects/{id}/variables[/{varId}]` |
| Releases | `/api/v1/projects/{id}/releases[/{relId}]` (`GET/POST/DELETE`), `POST .../refresh` |
| Deployments | `POST /api/v1/projects/{id}/deployments`, `GET /api/v1/deployments` (member projects; global admins see all; optional positive `project_id` filter) |
| Deployment detail | `GET /deployments/{id}`, `/status`, `/logs`, `/logs/{logId}` |
| Actions | `POST /deployments/{id}/cancel\|redeploy`; admin-only `POST .../approve` |
| Runbooks | `/api/v1/projects/{id}/runbooks[/{runbookId}]`, `/runbook-executions[/{executionId}]` |
| Users/tokens | admin under `/api/v1/admin/...`; `/api/v1/users/me`; `POST /api/v1/tokens` (`{"name":"..."}`) mints another token for yourself — 201, plaintext is in the `id` field of the reply |

## Getting help

- Swagger: `https://<your-host>/api/swagger/` — full request/response shapes.
- Server docs: <https://github.com/DeveloperDurp/durpdeploy>.
## Passing files between deployment steps

Local container steps receive `DURPDEPLOY_STAGE_DIR=/stage`, even with a
`variable_names` restriction. Write or copy files into this directory to pass
them to later local steps of the same deployment. For example, a producing step
runs `cp build.tar "$DURPDEPLOY_STAGE_DIR/build.tar"`; a later step reads that
file at the same path. This works with the existing step, release, deployment,
and runbook endpoints; no artifact publication request is needed.

Each deployment starts with an empty directory. Files survive step completion
and retries; failed attempts can leave partial files. Publish completed outputs
with a temporary file and atomic rename when a retry must not consume partial
data. Ordinary step `/tmp` files remain private to each attempt.

The runtime also needs the digest-pinned Alpine staging helper documented in
`docs/deploy.md`; an offline installation must preload it alongside step
images before launching deployments.

The staging and approved paths are reserved: variable create/update requests
return 422 for `DURPDEPLOY_STAGE_DIR` and `DURPDEPLOY_APPROVED_DIR`, including
blank-secret updates. Step variable selections cannot include them. Staging
is writable, noexec, nosuid, and nodev, backed by a bounded temporary volume
(512 MiB plus 10,000 host pages, 20,000 inodes). Local attempts have a combined
memory ceiling of that staging capacity plus 256 MiB; process memory and
`/tmp` share this allowance with staging writes. It is removed on success,
failure, cancellation, or shutdown; startup reclaims orphaned volumes within
the configured runtime namespace. Unconfirmed removal yields
`cleanup_unconfirmed` and blocks retry until a successful startup runtime sweep
changes the deployment to `failed`, even if a later cleanup attempt removed
the remaining resources. This preserves the existing conservative recovery
contract for local execution.

Only local Docker/Podman container steps share these files. Remote agent steps
do not receive the staging directory or transferred files. Staging is neither
a cross-deployment cache nor durable storage for an approval pause or restart.
Pinned release packages remain read-only at `ARTIFACT_PATH=/artifacts`.

Local steps accept `network_mode` (`none` by default, or `bridge`). Artifact
gates use `approval_artifact_path`, `approval_review_path`, and
`approval_review_format` (`summary` or `terraform`), relative to
`DURPDEPLOY_STAGE_DIR`. A gated deployment pauses before the next step and
reserves its environment; later deployments queue until it completes, is
rejected, is cancelled, or expires. Artifact approval retains its queue slot.
Only local deployment steps without retries are
supported; runbooks and agent steps cannot use gates. Both approval paths and
an explicit review format are required together; an empty format is rejected.
`GET /api/v1/deployments/{id}/artifact-gates` returns counts, checksums,
revision, expiry, status, and approver metadata, including
`review_format` (`terraform` or `summary`), `review_source: "step_output"`
and `review_verified: false`: counts are
unverified producer claims, not an independent analysis of the artifact.
Use trusted tools to inspect the exact downloaded artifact before approving.
Checksums establish byte identity, not review accuracy. Generation and apply
scripts remain trusted; a gate does not sandbox them to plan/apply semantics.
Write-capable project members can download `/{stepIndex}/artifact`; viewers
cannot. They can also inspect `/{stepIndex}/review`, which returns `resources`
with resource addresses, ordered actions, and formatted before/after JSON
strings. Terraform sensitivity masks and known secret release variables are
redacted; unknown values are labelled `(known after apply)`. Configuration,
variables, and outputs are omitted. This producer-supplied view retains
`review_source: "step_output"` and `review_verified: false`; it is not an
independent decoding of the saved binary plan. Summary-format gates return no
resources. Expired or unavailable reviews return 409. The deployment UI's
**View resource changes** disclosure loads this view on demand.
Administrator-only
`/{stepIndex}/approve` and `/{stepIndex}/reject` accept
`{"revision":1,"sha256":"..."}`. Stale or duplicate decisions return 409.
Approved context is read-only at `DURPDEPLOY_APPROVED_DIR`; apply the saved
artifact exactly, never regenerate it. Gated scripts and logs remain visible
under normal permissions and secret-variable log redaction. Keep Terraform
JSON in the review file; printing it can expose plaintext sensitive values. Gates
expire after 24 hours; the minute worker records expiry and releases queues.
Polling is read-only, and expired downloads and decisions return 409 immediately.
Stopped container references retain pinned image IDs through waits and restarts;
maintenance removes them after terminal decisions. Encrypted terminal bundles
are removed after seven days. See `docs/artifact-approval.md` for Terraform setup.

## Generic ZIP packages

Each project has one active HTTPS ZIP repository. Saving it automatically
enables packages for future release snapshots; no separate selection is needed.

- `GET/PUT/DELETE /api/v1/projects/{id}/package-repository`
- `POST /api/v1/projects/{id}/package-repository/test` with `{"version":"1.6.0"}`
- `GET /api/v1/projects/{id}/releases/{relId}/artifact` (pin or `null`)

Repository bodies contain `url_template`, `auth_type` (`noauth`,
`bearer`, or `basic`), `username` (basic only), and `credential` (token or
password). Use exactly one `{version}` in the HTTPS URL path, for example
`https://repo.example/app/{version}/package.zip`. URL credentials, query
strings, and cross-origin redirects are rejected. Private HTTPS destinations
are allowed; loopback and link-local destinations are blocked. Configure normal
system trust for a private repository's TLS certificate; TLS verification is
never disabled.

Artifact versions cannot be dot segments (`.` or `..`) or contain path
separators. ZIP paths must fit Linux staging filesystems: at most 255 bytes per
component and 4,000 bytes per relative path.

GET returns the active configuration or `null`. PUT configures or replaces it
and returns 200. DELETE disables packages for future snapshots, retaining source
records and credentials required by existing pins. Replacing the URL template,
auth type, or username creates a new source record rather than altering old pins.
Credentials are encrypted and omitted from all responses. A blank credential
preserves the current secret only when those source fields are unchanged.
Supply new credentials when replacing an authenticated source.
To rotate a retained historical source, save its exact URL template, auth type,
and username again with an explicit credential. This reactivates that source
record and updates the credential used by existing pins without changing them.

The test endpoint downloads and validates the requested ZIP without creating a
release. Success returns `exists: true`, the resolved `url`, `version`, `sha256`,
and compressed ZIP `size`. Invalid versions/ZIPs return 422; missing configuration
returns 404; upstream fetch failures return 502 and do not claim the package is
absent. Generic templates do not provide version discovery or listing.

Release creation/refresh downloads and validates the ZIP, then pins its URL,
version, SHA-256, and size. Deployment creation freezes that pin. Re-runs keep
the original deployment pin even after release refresh. Deployments use current
repository credentials and reject content that no longer matches the pin.

Runbook create/save accepts `artifact_release_id`, selecting an existing release
in the same project that has a package pin. It is required while a project
repository is selected. The runbook version copies that pin; later refreshes of
the source release do not change it. Runbook version responses include
`artifact` (the copied pin or `null`).

Deleting the source project release removes its completed deployment history and
its own artifact pin, but retains copies held by saved runbook versions. Those
copies clear `source_release_id` and remain executable with their pinned bytes.
When editing a saved runbook, `keep_artifact_pin: true` copies its latest pin
directly, including when its source release has been deleted. This cannot be
combined with `artifact_release_id` and is not valid for creating a new runbook.
The web edit form defaults to "Keep current pinned package" for deleted sources.

Every local step receives `ARTIFACT_PATH=/artifacts`, including steps with a
variable allowlist. This variable is reserved. Extracted files are mounted
read-only and noexec. Direct execution and writes are blocked; interpreters can
still read files, and steps can copy them elsewhere. ZIP paths, links, and
special files are rejected. Limits are 300 MiB downloaded, 512 MiB extracted,
10,000 ZIP entries, 19,999 total distinct files/directories (including implicit
parents), a fixed 16 MiB ZIP metadata-read budget, and a five-minute
download timeout. The metadata budget includes footer discovery and repeated
parser reads, not payload contents; unusually metadata-heavy ZIPs are rejected.
It is not an exact memory limit and does not scale with server RAM. Temporary server files
and runtime staging volumes are removed on completion, failure, or cancellation.
Cleanup uncertainty blocks retries until runtime reconciliation succeeds.

The container image uses native `TMPDIR=/data/tmp` on its writable data volume,
not Compose's 64 MiB `/tmp` mount. Reserve about 1.3 GiB of workspace per
concurrent maximum-size deployment for the ZIP, extracted files, and transport
tar. Direct installations can select a suitable directory with `TMPDIR`.
The server holds an exclusive lease on a private `durpdeploy-artifacts`
subdirectory and reclaims stale ZIP, extraction, and tar files at startup.
Concurrent server instances must use separate `TMPDIR` directories.

Artifact-bearing deployments currently require local steps; agent steps are
rejected before dispatch. The future agent download contract is
`POST /agent/v1/deployments/{id}/artifact` with JSON `{"claim_token":"..."}`
on the separate mTLS listener. It requires the assigned agent's live step claim,
returns verified ZIP bytes with `X-Artifact-SHA256` and `X-Artifact-Size`, and
never sends repository credentials. Agent executor support is a separate change.
