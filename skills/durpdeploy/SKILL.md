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

## Deploy flow (the common ask)

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
`bash`, `pwsh`, or `python3` installed. `agent` runs on matching remote agents
without a container image. API requests may use `powershell`; DurpDeploy
normalizes it to `pwsh`. All resolved release variables enter a step by
default; set `variable_names` only to restrict the step to those names. Local
steps exclude container/SSH client configuration names such as `PATH`, `HOME`,
`SSH_AUTH_SOCK`, or `XDG_*`; agent steps retain their host variable support.
The embedded agent pulls an image when it is missing. Container steps have no
network or host mounts. A mutable image tag does not
freeze image contents; prefer a digest. The web/API rejects a new image-less
server step. Old image-less releases remain readable but cannot run, re-run,
or refresh; recreate their steps and create a new release (`409` on launch).

4. **Variables** (optional) `POST /api/v1/projects/$PID/variables`
   `{"name":"API_URL","value":"...","environment_id":N}` — resolved at
   deploy time for the target environment.
5. **Release** (immutable snapshot of current steps + variables)
   `POST /api/v1/projects/$PID/releases` `{"version":"1.2.0"}` → `id`.
   Later step edits do NOT affect it; `POST /projects/$PID/releases/$RID/refresh`
   re-snapshots a current release, but cannot upgrade an old image-less one.
   `DELETE /api/v1/projects/$PID/releases/$RID` returns `204` when removed,
   `404` if absent or in another project, and `409` if it has active or
   unconfirmed deployments. It also deletes schedules and terminal deployment
   history, including logs; this cannot be undone.
6. **Deploy**
   `POST /api/v1/projects/$PID/deployments`
   `{"release_id":$RID,"environment_id":$EID}` → `201` with deployment `id`.
7. **Poll** until terminal:

```bash
for i in {1..150}; do
  S=$(api_get "$BASE/api/v1/deployments/$DID/status" | python3 -c 'import sys,json;print(json.load(sys.stdin)["status"])')
  [[ "$S" =~ ^(failed|succeeded|cancelled|cleanup_unconfirmed|pending_approval)$ ]] && break
  sleep 0.5
done
```

8. **Act on state**:
   - `pending_approval` → an admin `POST /api/v1/deployments/$DID/approve`
     (`{"approved_by":"alice"}`) unblocks it. Non-admin tokens get 403.
   - failure → `GET /api/v1/deployments/$DID/logs` (JSON lines, secrets are
      redacted) and `GET /.../logs.txt`; fix, create a new release or refresh
      a current one, then redeploy with
     `POST /api/v1/deployments/$DID/redeploy`.
    - `POST /api/v1/deployments/$DID/cancel` stops a running deploy.
    - `cleanup_unconfirmed` means container removal failed. Retry and redeploy
      return `409`; do not re-execute until a successful startup runtime sweep
      changes the deployment to `failed`.

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
| Deployments | `POST /api/v1/projects/{id}/deployments`, `GET /api/v1/deployments` (list) |
| Deployment detail | `GET /deployments/{id}`, `/status`, `/logs`, `/logs/{logId}` |
| Actions | `POST /deployments/{id}/cancel\|redeploy`; admin-only `POST .../approve` |
| Runbooks | `/api/v1/projects/{id}/runbooks[/{runbookId}]`, `/runbook-executions[/{executionId}]` |
| Users/tokens | admin under `/api/v1/admin/...`; `/api/v1/users/me`; `POST /api/v1/tokens` (`{"name":"..."}`) mints another token for yourself — 201, plaintext is in the `id` field of the reply |

## Getting help

- Swagger: `https://<your-host>/api/swagger/` — full request/response shapes.
- Server docs: <https://github.com/DeveloperDurp/durpdeploy>.
## Generic ZIP packages

Projects can select one HTTPS ZIP repository for new release snapshots.

- `GET/POST /api/v1/projects/{id}/package-repositories`
- `GET/PUT/DELETE /api/v1/projects/{id}/package-repositories/{repositoryId}`
- `GET/PUT /api/v1/projects/{id}/artifact-repository`
- `GET /api/v1/projects/{id}/releases/{relId}/artifact` (pin or `null`)

Repository bodies contain `name`, `url_template`, `auth_type` (`noauth`,
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

Select with `{"repository_id":123}`; zero disables packages for future
snapshots. Credentials are encrypted and omitted from all responses. A blank
credential on update preserves the current secret. Pinned repositories allow
name and credential changes only and cannot be deleted while referenced.

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

Artifact-bearing deployments currently require local steps; agent steps are
rejected before dispatch. The future agent download contract is
`POST /agent/v1/deployments/{id}/artifact` with JSON `{"claim_token":"..."}`
on the separate mTLS listener. It requires the assigned agent's live step claim,
returns verified ZIP bytes with `X-Artifact-SHA256` and `X-Artifact-Size`, and
never sends repository credentials. Agent executor support is a separate change.
