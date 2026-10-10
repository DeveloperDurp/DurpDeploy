# DurpDeploy — Security Reference

Current security boundaries, controls, and known gaps. Reviewed against the
implementation on 2026-10-10. See [the attack drill](attack-drill.md) for live checks.

---

## Threat model

DurpDeploy is a small-team internal deploy tool with trusted script authors and
operators. Global roles and project memberships restrict application access.
Agent host-mode scripts run with the agent's account authority; container
execution has the limits described below.

What we defend against:

- Unauthenticated deploys
- CSRF on a teammate's browser
- Replay with a stolen CSRF token (tokens are per-session random 16 bytes)
- Password DB leak (argon2id with per-user salt)
- Accidental writes by a viewer (UI gating + CSRF gate)
- Cross-project access by a non-member (per-project authorization middleware)
- Roster tampering by a non-admin project member (per-project admin gate on member add/remove)
- A leaked `variables`/`release_variables` DB file, on its own, does not disclose secret values (AES-256-GCM at rest)
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

**Roles:** `admin`, `deployer`, `viewer` (global. Enforced by the
`CHECK (role IN ('admin', 'deployer', 'viewer'))` constraint in
`migrations/011_auth.sql` and by `validRoles` in `internal/handler/users.go`).
Per-project member roles are `admin`, `deployer`
(`migrations/013_project_members.sql`).  
**Reference:** `docs/roles.md`

Two-layer defense for viewer read-only enforcement:

1. **CSRF middleware** — rejects application writes from a viewer at
   the protocol layer (always fires on the actual write attempt).
2. **CanWrite templ guard** — hides write affordances in the UI so a viewer
   never sees a useless form. `pages.CanWrite(ctx)` / `components.canWrite(ctx)`
   return `false` for viewers.

Both layers are necessary. Without the templ guard, the user interface has dead-end controls.
skipping the middleware leaves a security hole.

Logout is allowed with CSRF protection. A viewer may manage only their own
Security settings after the normal session, CSRF, and fresh-reauthentication
checks. A viewer cannot manage another user, deployments, projects, or tokens.

### Per-project authorization

**Implementation:** `internal/auth/projectaccess.go`

- `RequireProjectAccess` middleware: A global administrator bypasses membership
  checks and still receives the project ID in context. For other users, a
  missing project returns 404. A non-member gets 403 on API/native requests or
  a 200 response with an unauthorized toast on browser HTMX requests. Members proceed to
  the handler; its validation and result determine the response status.
- `CreateProject` auto-adds the creator as project admin.
- `ListProjects` filters by membership for non-admins.

### Per-project member management

**Implementation:** `internal/handler/project_members.go`

`RequireProjectAccess` only enforces a **binary** "is a member" check — it
admits any member (per-project admin or deployer), including a global viewer
who has membership, to the project route handler. The finer-grained
"is a per-project admin" rule
for member add/remove is enforced **at the handler level** via
`CanManageProject(ctx, repo, user, projectID)`:

- Returns `true` for a global `admin` or a project member whose per-project
  `role` is `admin`.
- When it returns `false`, `AddMember` / `RemoveMember` return `403` for
  native requests or `200` with an unauthorized toast for browser HTMX requests.

This keeps project deployers and global viewers from editing the roster while still
letting them access the other project routes. `CanManageProject` also gates the
add/remove controls in the Members section of the project edit page (UI layer),
mirroring the CanWrite two-layer pattern.

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
3. `auth.CSRFMiddleware()` — token check + viewer gate, with the exceptions above.
4. `audit.Middleware(repo)` — records successful state changes unless a handler
   suppresses or records them directly.

The `/api/v1` group uses `auth.ApiTokenMiddleware(repo)`,
`auth.WriteBlockMiddleware()`, `api.RequestBodyLimit`, and audit middleware.
Project and admin gates apply inside each group. The dedicated agent listener
uses mTLS agent authentication and claim checks.

Do not reorder or skip any of these.

---

## Secret encryption at rest

**Implementation:** `internal/secret/secret.go`, `internal/repository/repository.go`

The `value` column of both `variables` and `release_variables` is
AES-256-GCM encrypted before it ever reaches SQLite:

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
- **Decrypt path:** The repository decrypts a value into a temporary Go string.
  It does not write plaintext to the database or a log. It does not put
  plaintext in an error message. `secret.Box.Decrypt` returns only fixed error
  text.
  - **Runner:** `DeploymentRunner.Run` receives plaintext from
    `ListReleaseVariablesByRelease`. A server step receives all compatible
    resolved variables by default; a non-empty frozen `variable_names` list
    restricts what reaches the execution runtime. The scrubber still considers
    the resolved secret values before logs are stored or streamed.
- **Acceptance check:** `sqlite3 durpdeploy.db 'select * from variables'`
  shows only base64 ciphertext in `value`. The app reads/writes normally
  through the UI because the repository layer decrypts/encrypts
  transparently.

### Key rotation runbook

**Current limitation:** the CLI does not rotate encrypted TOTP seeds, stored
agent identity ciphertext, or remote log scrub buffers. Installing its new key
leaves those records encrypted with the old key and breaks TOTP verification.
Do not use it on an instance containing these records until rotation covers
them. Preserve the matching key with each database backup. This is an
implementation gap, not a database-recovery procedure.

For an eligible instance, first finish deployments and confirm remote cleanup.
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
3. In one transaction, re-encrypts variables, release variables,
   package-repository credentials, environment and deployment verification
   targets, and retained artifact-gate chunks. A failure rolls back the
   transaction. Other encrypted records listed in the limitation above are
   not migrated by this command.
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
| **Password reset flow** | No self-service reset. Administrators reset passwords through Admin → Users → Edit | Self-service recovery remains future work |
| **Incomplete server-key rotation** | TOTP seeds, stored agent identity ciphertext, and remote log scrub buffers are not re-encrypted by the CLI | Extend rotation before using it on instances containing these records |

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
