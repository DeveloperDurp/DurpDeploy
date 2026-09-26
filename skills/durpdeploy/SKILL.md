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
`/settings/tokens` (Post the form `name=<label>&csrf_token=$CSRF` if
scripting — the plaintext token appears in the `Location` query string as
`new_token=...` and is never shown again). Then:

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
  "interpreter": "bash",            // bash | pwsh | python3 (default bash)
  "sort_order": 1, "timeout_seconds": 300, "max_retries": 0,
  "execution_target": "local"       // local | remote (remote needs a paired agent)
}
```

4. **Variables** (optional) `POST /api/v1/projects/$PID/variables`
   `{"name":"API_URL","value":"...","environment_id":N}` — resolved at
   deploy time for the target environment.
5. **Release** (immutable snapshot of current steps + variables)
   `POST /api/v1/projects/$PID/releases` `{"version":"1.2.0"}` → `id`.
   Later step edits do NOT affect it; `POST /projects/$PID/releases/$RID/refresh`
   re-snapshots.
6. **Deploy**
   `POST /api/v1/projects/$PID/deployments`
   `{"release_id":$RID,"environment_id":$EID}` → `201` with deployment `id`.
7. **Poll** until terminal:

```bash
for i in {1..150}; do
  S=$(api_get "$BASE/api/v1/deployments/$DID/status" | python3 -c 'import sys,json;print(json.load(sys.stdin)["status"])')
  [[ "$S" =~ ^(failed|succeeded|cancelled|pending_approval)$ ]] && break
  sleep 0.5
done
```

8. **Act on state**:
   - `pending_approval` → an admin `POST /api/v1/deployments/$DID/approve`
     (`{"approved_by":"alice"}`) unblocks it. Non-admin tokens get 403.
   - failure → `GET /api/v1/deployments/$DID/logs` (JSON lines, secrets are
     redacted) and `GET /.../logs.txt`; fix, refresh release, redeploy with
     `POST /api/v1/deployments/$DID/redeploy`.
   - `POST /api/v1/deployments/$DID/cancel` stops a running deploy.

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

## Runbooks

Runbooks save immutable versions of ordered steps. Create one with
`POST /api/v1/projects/$PID/runbooks` and a JSON body containing `name` and
`steps` (`name`, `script_body`, optional `interpreter`, `timeout_seconds`,
`max_retries`, `execution_target`, and `agent_selectors`). Save the next
version with `PUT /api/v1/projects/$PID/runbooks/$BID` and `steps`.

`POST /api/v1/projects/$PID/runbooks/$BID/executions` takes
`environment_id` and optional `version_id`; omit the version or pass `0`
for the latest. Its response includes the runbook execution `id` and the
underlying `deployment_id`. Read the execution at
`/api/v1/projects/$PID/runbook-executions/$XID`, logs at `/logs`, and live
logs at `/logs/stream` (SSE by default, `?format=ndjson` for NDJSON).
Execution actions are `POST .../$XID/cancel`, `/approve` (admin only),
and `/retry` (after a terminal status).

`GET /api/v1/projects/$PID/runbook-executions?limit=100&offset=0`
returns `{items, total, limit, offset}`. The default page has 100 items;
the maximum is 1000. `POST /api/v1/projects/$PID/runbooks/$BID/schedules`
takes `environment_id`, five-field `cron`, and optional `version_id`.
Omit the version or pass `0` to follow the latest saved version. Disable a
schedule with `POST .../schedules/$SID/disable`.

The web UI starts at `/projects/$PID/runbooks`. It supports editing,
execution history, live logs, approval, cancellation, retry, and schedules.

## Endpoint cheat sheet

| Resource | Endpoints |
|----------|-----------|
| Projects | `GET/POST /api/v1/projects`, `GET/PUT/DELETE /projects/{id}` |
| Environments | same shape under `/environments` |
| Steps | `/api/v1/projects/{id}/steps[/{stepId}]` (`POST/GET/PUT/DELETE`, `PATCH /steps/reorder`) |
| Variables | `/api/v1/projects/{id}/variables[/{varId}]` |
| Releases | `/api/v1/projects/{id}/releases[/{relId}]`, `POST .../refresh` |
| Deployments | `POST /api/v1/projects/{id}/deployments`, `GET /api/v1/deployments` (list) |
| Deployment detail | `GET /deployments/{id}`, `/status`, `/logs`, `/logs/{logId}` |
| Actions | `POST /deployments/{id}/cancel\|redeploy`; admin-only `POST .../approve` |
| Runbooks | `/api/v1/projects/{id}/runbooks[/{runbookId}]`, `/runbook-executions[/{executionId}]` |
| Users/tokens | admin under `/api/v1/admin/...`; `/api/v1/users/me`; `POST /api/v1/tokens` (`{"name":"..."}`) mints another token for yourself — 201, plaintext is in the `id` field of the reply |

## Getting help

- Swagger: `https://<your-host>/api/swagger/` — full request/response shapes.
- Server docs: <https://github.com/DeveloperDurp/durpdeploy>.
