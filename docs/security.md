# DurpDeploy — Security Reference

Current security boundaries, controls, and known gaps. Reviewed against the
implementation on 2026-10-10. See [the attack drill](attack-drill.md) for live checks.

---

## Threat model

DurpDeploy is a small-team internal deploy tool with trusted script authors and
operators. Global roles and project memberships restrict application access.
Agent host-mode scripts run with the agent's account authority; container
execution has the limits described below.

The server owner is responsible for deployment scripts and their effects.
Deployment cleanup covers managed staging and normal job descendants.
Deliberate supervisor sabotage and script-created copies outside staging are outside this cleanup guarantee.
The agent stops polling when it cannot confirm process or staging cleanup.
Recovery requires operator action when recorded staging is missing or replaced.
Staging transfer adds no per-job cgroup or delegation requirement.

What we defend against:

- Unauthenticated deploys
- CSRF on a teammate's browser
- Replay with a stolen CSRF token (tokens are per-session random 16 bytes)
- Password DB leak (argon2id with per-user salt)
- Accidental writes by a viewer (UI gating + CSRF gate)
- Cross-project access by a non-member (per-project authorization middleware)
- Roster tampering by a non-admin project member (per-project admin gate on member add/remove)
- A database leak alone does not disclose encrypted project, release, lifecycle,
  or deployment variable values (AES-256-GCM at rest)
- The server sends remote agents neither its database nor its encryption key
- Paired agent transport uses outbound mTLS with pinned peer fingerprints;
  pairing uses a temporary inbound callback on the agent

Each remote step creates one run for every active, paired agent that matches the
deployment environment and all required capability labels. Each pre-start claim
belongs to one agent and can expire after 60 seconds, but started work is not
requeued, replayed, or moved to the local runner. Missed heartbeats mark work
lost after 45 seconds. A cancel needs an agent acknowledgement within 30
seconds, otherwise the result is `cancel_unconfirmed` and requires host
inspection before a new deployment.

### OIDC boundary

OIDC adds browser sign-in; local password login and bearer API authentication
remain separate. Provider group removal takes effect on the next OIDC login.
Logout is local only. Use [the OIDC guide](authentik-oidc.md) for identity linking,
email verification, provider outage, and administrator recovery requirements.

### Execution boundary

Server-side steps run only in containers managed through the embedded agent's
local Docker or Podman socket. No step receives that socket or a control-plane mount;
the root filesystem is read-only, network is disabled by default (local steps
can explicitly opt into `network_mode: "bridge"`), capabilities are
dropped. Steps receive all compatible resolved variables by default; a nonempty
frozen `variable_names` list restricts the variables passed. A missing
runtime fails closed. The socket grants the control plane broad authority over
the container host, so use a dedicated host or standalone agent when that
boundary is required. A container shares its host kernel and is not a VM.
Only trusted operators may administer runtime sockets or host-mode agents.
Use separate hosts or virtual machines for workloads with different trust levels.
Host-mode scripts inherit the supervisor account's runtime authority.
Set host resource limits separately from log redaction and application authorization.
DurpDeploy does not impose per-step CPU or RAM quotas.
Agent steps still execute on their agent hosts, which must be isolated from
control-plane state if their scripts are untrusted. Historical image-less
server releases remain readable but cannot execute; issue #28's old same-UID
host execution is not a supported path for new work.

Deployments outlive their initiating HTTP request. Cancel through the API or UI;
cancel and shutdown ask the runtime to remove the step container. Startup
reconciles orphaned attempts. Killing the container client does not confirm
cleanup. An unconfirmed outcome requires inspection before retrying.

---

## Authentication

**Implementation:** `internal/auth/auth.go`

- Session cookie (`session` key, `HttpOnly`, `SameSite=Lax`).
- `AuthMiddleware` validates the cookie on session-authenticated browser routes. Redirects to
  `/login` on miss.
- Passwords hashed with **argon2id** (`time=2, memory=64 MB, threads=2`).
  Each wrong-password guess costs ~100 ms of server CPU and ~64 MB of RAM.
  Comparison uses `subtle.ConstantTimeCompare` to prevent timing attacks.
- No plaintext password is stored anywhere in the database.

### Browser MFA

Browser MFA is optional. An enrolled user completes a browser login with their
password and one current TOTP code, passkey, or recovery code. TOTP is not
phishing-resistant. Passkeys are bound to the single origin and RP ID derived
from `DURPDEPLOY_URL`. Changing its hostname or origin invalidates the stored
credential relationship and requires passkey re-enrollment.

Passkey names must contain 1 to 255 UTF-16 code units after whitespace trimming.
Registration, credential storage, and renaming use the same limit.
The server removes expired ordinary MFA challenges during periodic maintenance.
Each authentication operation also enforces challenge expiry and one-use consumption.
Administrator reset continuations use their separate acknowledgement lifecycle.

Recovery codes are one-time values. They are displayed only when first
generated or regenerated, and only their hashes are stored. Users must keep
the displayed values in approved protected storage. MFA ceremony and secret
responses use `Cache-Control: no-store`. Do not put recovery codes, TOTP
seeds, cookies, challenges, or assertions in URLs, logs, tickets, or docs.

A newly minted API token is a one-time value too. The web create flow stores
the plaintext in a short-lived single-use flash record bound to the creating
user and session, redirects with an opaque flash identifier, and consumes the
record on first display; abandoned records expire within minutes. The
plaintext never appears in a URL, and the display response sends
`Cache-Control: no-store` and `Referrer-Policy: no-referrer`.

The final `session` cookie is `HttpOnly`, `SameSite=Lax`, and `Secure` when
`DURPDEPLOY_URL` uses HTTPS. Pending MFA cookies are separate from the final
session and do not authorize protected routes. Factor completion issues a new
browser session and records `reauthenticated_at`. Factor changes, disablement,
password changes, and administrator resets invalidate the affected browser
sessions and pending challenges.

Browser MFA protects browser sessions only. API tokens are single bearer
factors: `/api/v1/*` does not prompt for MFA, and MFA disablement or an
administrator reset does not revoke API tokens. Revoke API tokens separately
when their bearer value may have leaked.

### Change your password

Open **Security** from the account menu.
If your security session is stale, select **Confirm identity** before you change your password.
For accounts with MFA, complete the configured factor check.
Enter your current password and matching new passwords of at least eight characters.
Select **Change password**.
Sign in again with your new password.

Every role, including Viewer, can change its own local password.
Validation failures keep the password unchanged and clear all password fields.
A successful change removes all browser sessions and pending MFA challenges in one transaction.
Enrolled MFA factors and API tokens remain valid.
Revoke API tokens separately if their values may have leaked.
For SSO-only accounts, change the password with the identity provider.

Use `POST /api/v1/users/me/password` for the corresponding public API operation.
Send an unrestricted global bearer token and a JSON object with `current_password`, `new_password`, and `password_confirmation`.
The API requires the current password for every role.
The API rejects project-scoped tokens and tokens with a permission ceiling.
The API returns `204` after success and `422` for invalid credentials or password fields.
New passwords must leave room for all password fields, email, and CSRF in the existing 64 KiB encoded form limit.
The API rejects oversized new passwords and new passwords with line breaks before changing credentials.

Users enroll or recover factors from **Security**. An administrator may reset
another user's MFA only after fresh reauthentication. That reset removes the
target's factors, recovery codes, browser sessions, and pending challenges.
It intentionally preserves API tokens. This is an operational contract. The
browser ceremony end-to-end proof is tracked separately from this document.

---

## CSRF protection

**Implementation:** `internal/auth/csrf.go`

- Session-authenticated browser writes require a valid `csrf_token` form field
  or `X-CSRF-Token` header. `/api/lint` is a read-only session action exempt
  from CSRF. Public login ceremonies have their own checks; `/api/v1` uses
  bearer authentication instead of browser CSRF.
- Token is per-session, random 16 bytes, stored in the `sessions` table.
- Viewer rejections (read-only role attempting a write):
  - HTMX requests → `200` + `HX-Trigger: makeToast` (red toast, page stays).
  - Non-HTMX form submits → `403` + styled error page.
- CSRF rejections are **not** written to `audit_log` (intentional — failed
  attempts must not be enumerable).

---

## Authorization

### Role-based access

Permission order is `viewer < deployer < maintainer < admin`.
Global roles, membership roles, and token ceilings each limit project operations.
Migration `064`, or SQL Server migration `049`, extends the database constraints.
See [roles](roles.md) for the capability matrix and upgrade behavior.

The browser and API reject Viewer application writes.
They also reject Deployer writes except existing-release deployment execution, retry, re-run, and cancellation.
Logout and the user's own Security settings retain the existing CSRF and reauthentication checks.

The UI uses `CanWrite` for authoring and `CanDeploy` for deployment execution.
Both helpers include the current project membership ceiling.
The middleware also rejects forged requests.
Hidden controls do not replace authorization checks.

### Per-project authorization

`RequireProjectAccess` checks project coverage and applies the membership permission ceiling.
Global administrators bypass membership and remain subject to token restrictions.
Other users need membership to read a project.
A missing project returns `404`.
A non-member receives `403`, or an unauthorized toast for browser HTMX requests.

Viewer memberships permit reads.
Deployer memberships also permit deployment execution.
Maintainer memberships also permit authoring.
Project creation adds the creator as a project administrator.

### Per-project member management

`auth.CanManageProject` permits global administrators and project administrators with global Maintainer permission.
An explicit token must also have an `admin` ceiling.
Restricted global users cannot gain authoring permission through an administrator membership.
The handlers enforce member management checks.
The UI uses the same permission check to hide member controls.

### Admin-only routes

Routes in `/admin/users/*` are gated by `RequireRole("admin")`. The
`CanWrite` templ guard is applied there too as belt-and-suspenders.

---

## Audit log

**Implementation:** `internal/audit/audit.go`, `audit.Middleware`

- Records successful state changes unless the handler suppresses or records
  the event directly. Audit insertion failures are logged without blocking
  the request.
- The middleware does not audit CSRF rejections or 4xx responses; authentication
  handlers can record separate security events directly.
- Every new state-changing route must be added to `actionMap` in
  `internal/audit/routes.go` for a stable action name. The fallback heuristic
  (method + first path segment) is lossy.
- The `actionMap` covers the user-management routes
  (`create_user`, `update_user`, `delete_user`) and the project-member routes
  (`add_project_member`, `remove_project_member`).

---

## Middleware stacks

Session-authenticated browser routes in `internal/server/server.go` use:

1. `auth.AuthMiddleware(repo)` — session → user in context.
2. `webRequestBodyLimit` — form validation and bounds before CSRF parsing.
3. `auth.CSRFMiddleware()` — token check + role gate, with the exceptions above.
4. `audit.Middleware(repo)` — records successful state changes unless a handler
   suppresses or records them directly.

The `/api/v1` group uses `auth.ApiTokenMiddleware(repo)`,
`auth.TokenScopeMiddleware()`, `auth.WriteBlockMiddleware()`, `api.RequestBodyLimit`, and audit middleware.
Project and admin gates apply inside each group. The dedicated agent listener
uses mTLS agent authentication and claim checks.

Project-scoped bearer tokens intersect selected projects, a token role, and the owner's current permissions.
Global administrators remain subject to token scope and its role ceiling.
All-project tokens can also apply a permission ceiling to the owner's current permissions.
Tokens with an explicit permission ceiling cannot manage API tokens or remove that ceiling themselves.
Scoped tokens cannot manage tokens, create projects, or use instance administration.
Resource-ID routes resolve the owning project before they return data.
Aggregate queries filter rows and totals to the same authorized projects.
Open log streams recheck stored token grants, owner access, expiry, and revocation.

Do not reorder or skip any of these.

---

## Secret encryption at rest

**Implementation:** `internal/secret/secret.go`, `internal/repository/repository.go`

The `value` columns in `variables`, `release_variables`, `lifecycle_variables`,
and `deployment_variable_snapshots` use AES-256-GCM encryption before database storage:

- **Key source:** `/etc/durpdeploy/key` (file, checked first) or
  `DURPDEPLOY_SECRET_KEY` (env, base64-encoded 32 bytes). The server calls
  `secret.LoadKey()` at startup and **refuses to boot** (`log.Fatalf`) if
  neither is configured — there is no "run with plaintext secrets" mode.
- **Encrypt path:** `Repository.CreateVariable` / `UpdateVariable` encrypt
  `value` before the INSERT/UPDATE. Release snapshot creation
  (`ReleaseHandler.CreateRelease` / `RefreshRelease`) re-encrypts each
  variable's value via `Repository.EncryptValue` before writing the
  `release_variables` row (values are never round-tripped through the DB
  in plaintext).
  Lifecycle variable writes encrypt each value. Deployment creation encrypts
  the complete resolved variable capture, including shared values and project snapshots.
- **Decrypt path:** The repository decrypts a value into a temporary Go string.
  It does not write plaintext to the database or a log. It does not put
  plaintext in an error message. `secret.Box.Decrypt` returns only fixed error
  text.
  API reads skip decryption for masked secret variables and omitted environment verification targets.
  Unreadable hidden values do not prevent metadata reads.
  Execution and snapshot operations still require readable values.
  - **Runner:** Each execution reads its decrypted deployment variable capture.
    New executions resolve current lifecycle values with frozen project values.
    Queued and active executions retain their capture.
    A frozen `variable_names` list restricts values passed to the execution runtime.
    The scrubber uses captured secret values before log storage or streaming.
- **Acceptance check:** Run this read-only query against the database:

  ```bash
  sqlite3 durpdeploy.db '
  SELECT value FROM variables
  UNION ALL SELECT value FROM release_variables
  UNION ALL SELECT value FROM lifecycle_variables
  UNION ALL SELECT value FROM deployment_variable_snapshots;'
  ```

  Each returned value must be base64 ciphertext. Verify normal variable reads
  and a deployment through the UI or API after rotation.

### Key rotation runbook

First finish deployments and confirm remote cleanup.
The command rejects both rotation modes while encrypted deployment staging chunks remain.
If rotation rejects staging chunks, finish deployment cleanup before retrying.
Back up the database, matching key, and server identity. Stop all server and
replication processes; an active server keeps the old key and can write
old-key ciphertext after the rotation transaction. For Compose:

```bash
docker compose stop app litestream
docker compose run --rm --no-deps app secret-key rotate
```

The one-off container uses the app's configured database and current key.
Keep the printed replacement key private and available until it is installed.

This one-shot command (`cmd/server/main.go: runSecretKey`):

1. Loads the **current** key via `secret.LoadKey()` (same file/env lookup
   the server uses).
2. Generates a fresh random 32-byte key.
3. In one transaction, re-encrypts project variables, release variables,
   lifecycle variables, deployment variable captures, package-repository credentials,
   verification targets, retained artifact-gate chunks, TOTP seeds, pending TOTP enrollment seeds,
   stored agent identities, pairing identities, and remote log scrub buffers.
   A failure rolls back the transaction.
4. Prints the new key (base64) to stdout.

After successful rotation, write the exact printed key to the key source used
by the app. In the supplied Compose stack, replace `secrets/durpdeploy_key`
and keep mode `0600` and ownership readable by container UID 10001. For rootful
Docker, use `sudo chown 10001:10001 secrets/durpdeploy_key`; for rootless Podman,
use `podman unshare chown 10001:10001 secrets/durpdeploy_key`. Preserve the
appropriate user-namespace mapping for other engines. Repeat the
[key-readability check](deploy.md#quick-start-docker-compose-recommended-for-self-hosting)
before restarting. The mounted `/etc/durpdeploy/key` file takes precedence
over `DURPDEPLOY_SECRET_KEY`; changing only the environment variable would
leave the old file key in use. Recreate the app and resume replication:

```bash
docker compose up -d --force-recreate app litestream
```

Keep the old key with pre-rotation backups. Install the exact key printed by
the CLI before starting the server; an independently generated key cannot
decrypt the rotated data.

Restart pending OIDC login flows after rotation. Their encrypted browser cookies
use the old key and cannot be migrated by the database transaction.
Agent dispatch payloads use agent certificates and do not require server-key rotation.
The `--plaintext` option migrates legacy project and release variables.
The command rejects this option when populated encrypted-only stores exist.

---

## Log redaction

**Implementation:** `internal/logscrub/scrubber.go`,
`internal/runner/runner.go`, `internal/agentserver/lifecycle.go`

DurpDeploy scrubs deployment logs before an SSE broadcast or a database write.
`broadcastWriter` now delegates to a `Scrubber` (`internal/runner/scrubber.go`),
built once per deployment run from that environment's secret variable
values:

- **Single compiled regular expression:** The scrubber escapes each literal
  secret with `regexp.QuoteMeta`. It sorts the secrets from longest to shortest.
  It also includes patterns for common credentials. These include bearer
  tokens, GitHub PATs, AWS keys, Slack tokens, and credential assignments. RE2
  processes the combined expression in linear time.
- **Configurable patterns:** Additional regex patterns can be added via the
  `DURPDEPLOY_EXTRA_SCRUB_PATTERNS` environment variable (comma-separated).
  These are appended to the common credential patterns at startup. A possible
  custom-pattern match remains buffered while it can still grow across an
  event or line boundary. Local tails stay in memory; persisted remote tails
  are encrypted. Once later input terminates the possible match, redacted
  output resumes. A pattern such as `.*` that can consume all future input
  necessarily remains buffered until terminal flush.
  Custom anchors and word-boundary assertions are treated as empty matches.
  This can over-redact, but prevents chunk boundaries from exposing a match
  whose assertion depends on text that was already released.
- **Buffered operation:** `broadcastWriter.Write` scrubs all text through the
  last newline in its buffer. Thus, it finds a secret in two writes. It also
  finds a secret that contains a newline.
- **Remote ingress:** The server applies the same scrubber before database and
  SSE delivery. It keeps a bounded encrypted tail in the lifecycle row so a
  plaintext secret split across events, requests, or a server restart is
  redacted before release. An initial sequence gap remains encrypted until the
  missing events arrive or a terminal agent message flushes the tail.
  If an agent is lost, revoked, or times out before completing a buffered
  fragment, DurpDeploy discards that incomplete fragment instead of publishing
  ambiguous plaintext.
- **Best-effort:** this catches known secret values and a handful of common
  token shapes. The security contract is protection against accidental
  plaintext exposure, not intentional exfiltration by an agent that encodes,
  transforms, or decorates secret data. **Redaction is best-effort. Do not
  paste secrets into your script body. Use environment variables marked
  Secret.**

## Known gaps

| Gap | Risk | Planned |
|-----|------|---------|
| **Container runtime compromise** | The embedded agent's socket can control the Docker or Podman host; container escape remains possible on a shared kernel | Use a dedicated host or disable the embedded agent and use standalone agents |
| **Audit log retention / integrity** | Pruning is opt-in through `audit prune` and an operator-managed schedule; no tamper-proofing | Operator setup; integrity remains future work |
| **Password reset flow** | Signed-in local users change their password under Security. Administrators can reset passwords through Admin → Users → Edit. | Forgotten-password recovery remains future work. |

---

## What this document does not cover

- **Compromised teammate's laptop** — if the attacker has a valid cookie +
  CSRF token, they are that teammate. No defense available client-side.
- **Server root compromise** — an attacker with root can replace the binary,
  read the DB, or sniff process memory. OS-level problem.
- **Network-level DDoS** — handled upstream (Caddy, firewall).
- **Supply chain** — `go mod verify` and pinned versions only.
- **Compromised remote agent host:** An agent can run work explicitly routed to it. Isolate it from the control-plane host. Rotate its pairing and certificate material.

See `docs/attack-drill.md` for hands-on verification of the active defenses.
