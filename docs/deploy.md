# Production deployment

Run DurpDeploy with Docker Compose behind Caddy for HTTPS. The stack includes
SQLite storage, an embedded container executor, and a Litestream backup sidecar.
You need Docker with Compose, a DNS name pointing to the host, and inbound
ports 80 and 443. Restrict the separate agent port 10943 to agent networks.

## HTTP connection limits

Both Go listeners enforce these limits, whether a proxy is present or not:

| Phase | Limit |
| --- | --- |
| Request headers | 5 seconds |
| Whole request read, including headers and body | 30 seconds |
| Ordinary response writes | 60 seconds after headers are read |
| Idle keep-alive connection | 60 seconds |

Read limits are absolute deadlines, not inactivity timers. Slowly sending a
few bytes does not extend them. Existing password-form and agent-protocol
body-size limits still apply. A timeout can close the connection without an
HTTP error response; clients must not depend on a particular status code.

Deployment and runbook log streams (SSE and NDJSON), plus deployment SSE
events, have no total response lifetime limit. Each event write and flush
has a fresh 60-second deadline; a failed write ends the stream. The deadline
is cleared between events, so quiet periods do not expire healthy streams.
Clients must keep consuming output and reconnect after a disconnect.

Caddy fronts browser/API traffic, not the direct mTLS agent listener. Keep
the plain HTTP browser backend on a private network or loopback in production.
The Go limits protect backend connections, not slow clients held by a proxy:
configure defensive client header/body limits at your proxy too. Proxies must
flush SSE/NDJSON promptly and allow long-lived responses, including quiet
periods; a finite proxy response/idle timeout can still cut a stream short.
The shipped Caddy configuration has no finite upstream response lifetime
limit. Do not add a blanket response timeout to streaming paths.

---

## Quick start: Docker Compose (recommended for self-hosting)

A Docker Compose stack ships the app, Caddy (reverse proxy + Let's Encrypt
TLS), and Litestream (continuous SQLite backup to S3) in three services.
The image is based on Alpine and operates as a non-root user. Caddy and Litestream
are the official upstream images.

```bash
# 1. Clone and prep
git clone <repo> durpdeploy && cd durpdeploy
cp compose.example.yml compose.yml
$EDITOR deploy/litestream.example.yml   # fill in S3 bucket, path, region, etc.

# 2. Generate the encryption key (32 random bytes, base64)
mkdir -p secrets
openssl rand -base64 32 > secrets/durpdeploy_key
chmod 0600 secrets/durpdeploy_key
# Rootful Docker: file-backed secrets retain host ownership.
sudo chown 10001:10001 secrets/durpdeploy_key
# For rootless Podman, use instead of sudo chown:
# podman unshare chown 10001:10001 secrets/durpdeploy_key

# 3. Create compose env files
cat > compose.caddy.env <<'EOF'
DURPDEPLOY_URL=https://durpdeploy.example.com
BACKEND=app:8080
EOF

cat > compose.app.env <<'EOF'
# Optional OIDC block (omit all variables if you do not need SSO)
# DURPDEPLOY_OIDC_ISSUER=https://idp.example.com/realms/example
# DURPDEPLOY_OIDC_CLIENT_ID=durpdeploy-example
# DURPDEPLOY_OIDC_CLIENT_SECRET=<secret>
# DURPDEPLOY_OIDC_ADMIN_GROUP=durpdeploy-admin
# DURPDEPLOY_OIDC_DEPLOYER_GROUP=durpdeploy-deployer
# DURPDEPLOY_OIDC_VIEWER_GROUP=durpdeploy-viewer
# DURPDEPLOY_OIDC_DISPLAY_NAME=SSO
# DURPDEPLOY_OIDC_GROUP_CLAIM=groups
# DURPDEPLOY_OIDC_REQUIRE_EMAIL_VERIFIED=false  # default true

# Include any other app env your deployment needs, e.g. SMTP.
EOF

cat > compose.litestream.env <<'EOF'
LITESTREAM_S3_BUCKET=my-durpdeploy-backups
AWS_ACCESS_KEY_ID=AKIA...
AWS_SECRET_ACCESS_KEY=...
EOF

# Compose uses .env for interpolation of its explicit public listener setting.
cat > .env <<'EOF'
DURPDEPLOY_AGENT_PUBLIC_URL=https://durpdeploy.example.com:10943
EOF
chmod 0600 compose.app.env compose.caddy.env compose.litestream.env
# Keep AWS credentials in compose.litestream.env, not .env or compose.app.env.

# 4. Build and verify key access without printing the key
docker compose build app
docker compose run --rm --no-deps --entrypoint /bin/sh app -ec \
  'su-exec 10001:10001 test -r /etc/durpdeploy/key'

# 5. Cache the staging helper in the execution runtime, then start
STAGING_HELPER_IMAGE=docker.io/library/alpine@sha256:ce64758a109eb420d874a118f87920e625e12d3634e03b4a5573fd9f6e5d3507
docker pull "$STAGING_HELPER_IMAGE"
docker compose up -d
# With rootless Podman, use instead:
# podman pull "$STAGING_HELPER_IMAGE"
# podman compose -f compose.yml -f compose.podman.yml up -d --build

# 6. Bootstrap the first admin
docker compose run --rm --no-deps app admin create \
  --email admin@example.com --password '<strong-password>'
```

Docker Compose [file-backed secrets](https://docs.docker.com/reference/compose-file/services/#secrets)
ignore the declared `uid`, `gid`, and `mode`. The key must be readable by
container UID 10001 without granting other users access. Rootless engines and
user-namespace remapping require the corresponding mapped host ownership;
the Podman command above applies ownership inside its user namespace.
For Podman, use `podman compose -f compose.yml -f compose.podman.yml` for all
Compose commands, including the build, key-access check, and admin creation.
Keep `BACKEND=app:8080` in Caddy's environment; its default `localhost:8080`
is for a reverse proxy on the same host as a bare server binary.

### Remote agent control plane

The complete agent runbook is [`docs/agents.md`](agents.md). Read it before
opening the listener or starting an agent pairing. The short deployment
boundary is:

* The server owns SQLite, WAL/SHM files, agent records, policies, and the
  server encryption key. An agent has no database and receives none of those
  files or secrets.
* Caddy and Let's Encrypt serve browser and API HTTPS on ports 80 and 443.
  The dedicated agent listener is direct TLS 1.3 mTLS on port 10943 and does
  not route through Caddy. Publish 10943 separately and firewall it to agent
  networks.
* The server listener is always active and creates a local identity when
  needed. Production must override its localhost-oriented defaults:

  ```dotenv
  DURPDEPLOY_AGENT_LISTEN_ADDR=0.0.0.0:10943
  DURPDEPLOY_AGENT_PUBLIC_URL=https://<agent-control-host>
  DURPDEPLOY_AGENT_IDENTITY_DIR=/var/lib/durpdeploy/agent-identity
  ```

* An unpaired agent needs only a private state directory, temporary local
  pairing listener, and version. The administrator pairing flow stores the
  pinned server endpoint and agent identity. Restarts use that paired state.

  ```dotenv
  DURPDEPLOY_AGENT_STATE_DIR=/var/lib/durpdeploy-agent
  DURPDEPLOY_AGENT_LISTEN_ADDR=0.0.0.0:10943
  DURPDEPLOY_AGENT_VERSION=<agent-version>
  ```

Agent host-mode execution does not use a per-step `chroot`; scripts share the
service account. The operator or user remains responsible for scripts, supplied secrets,
network access, and their effects. Read-only filesystems do not prevent secret
exfiltration. Install standalone agents using [the agent runbook](agents.md).

DNS must be pointed at the host (port 80/443 open inbound) before the first
`docker compose up` — Caddy issues Let's Encrypt certs on first request.
See [`docs/backup-restore.md`](backup-restore.md) for the Litestream
restore drill and [`docs/security.md`](security.md) for the threat model.

### Browser MFA and public URL

Set `DURPDEPLOY_URL` to one absolute public origin, for example
`DURPDEPLOY_URL=https://durpdeploy.example.com`. Production must use HTTPS.
HTTP is accepted only for `localhost` or loopback development. The app derives
the WebAuthn RP ID from this hostname and does not trust request Host or
forwarded-host headers. Changing the hostname or origin invalidates existing
passkeys. Keep the same URL through restores and migrations, or have affected
users enroll new passkeys.

Browser MFA is optional. After the first admin signs in, each user can enroll
a TOTP authenticator or passkey from **Security**. Recovery codes are shown
once when the first factor is activated or regenerated. Store them in an
approved password manager or offline protected storage, never in tickets, chat,
or source control. An administrator can reset another user's MFA from user
management after fresh reauthentication. Resetting MFA removes that user's
browser sessions, challenges, factors, and recovery codes. It preserves API
tokens, which are separate single-bearer credentials.

### Optional OIDC sign-in

Follow the [OIDC configuration and Authentik guide](authentik-oidc.md).
It lists the required variables, callback URL, role mapping, and recovery limits.
For Compose, put the app-specific settings in `compose.app.env`.
Keep the client secret private and restart the service after changes.

### Plain Docker (binary distribution only)

If you want to run the binary behind your own reverse proxy, the image is
also runnable directly:

```bash
docker build -t durpdeploy .
docker run -d --name durpdeploy -p 8080:8080 \
  -v durpdeploy-data:/data \
  -e DURPDEPLOY_DB=/data/durpdeploy.db \
  -e DURPDEPLOY_SECRET_KEY=$(openssl rand -base64 32) \
  durpdeploy
```

You still have to bootstrap the first admin with the CLI — there is no
env-var shortcut:

```bash
docker exec -it durpdeploy /usr/local/bin/durpdeploy admin create \
  --email admin@example.com --password '<strong-password>'
```

Caddy, TLS, and Litestream are NOT included — bring your own.

---

## Database: SQLite (default), PostgreSQL, or SQL Server

DurpDeploy uses SQLite by default. SQLite does not require a separate database
server. Use SQLite for an installation with one application instance. Refer to
`docs/backup-restore.md` for the Litestream backup procedure.

PostgreSQL and SQL Server are also supported for teams that already operate
those databases. The driver is selected from `DURPDEPLOY_DB`: `postgres://`
and `postgresql://` URLs use PostgreSQL, while `sqlserver://` URLs use SQL
Server. Any other value is treated as a SQLite file path (the default).
PostgreSQL uses the SQLite migrations. SQL Server applies the embedded native
MSSQL migrations and uses the same generated query API.

```bash
# SQLite (default)
export DURPDEPLOY_DB=/var/lib/durpdeploy/durpdeploy.db

# PostgreSQL
export DURPDEPLOY_DB="postgres://durpdeploy:<password>@localhost:5432/durpdeploy?sslmode=disable"

# SQL Server (TLS is necessary by default; use a certificate trusted by the host)
export DURPDEPLOY_DB="sqlserver://durpdeploy:<password>@sqlserver.example.com:1433?database=durpdeploy"
```

Migrations run automatically on startup against whichever database
`DURPDEPLOY_DB` points at. There is no dump/import path between database
engines — pick one per environment.

Back up and restore the database together with the server secret key. MFA
records, browser sessions, recovery-code hashes, and encrypted TOTP material
are database state. Restoring one without the matching key can make encrypted
TOTP material unusable. A restore does not change the configured origin, so a
hostname change still requires passkey re-enrollment.

---

## Server-side container execution

Every new server-side step (`execution_target: "local"`) requires a
`container_image`. The control plane never executes its script on the host.
The embedded agent connects only to a local Unix socket and pulls a missing
step image automatically. Docker Compose mounts `/var/run/docker.sock` without
additional configuration. For rootless Podman, start its socket and use the
shipped override:

```bash
systemctl --user enable --now podman.socket
podman compose -f compose.yml -f compose.podman.yml up -d
```

The Podman override disables SELinux labelling for the control-plane container
so it can connect to the host user's socket; step containers retain their own
security options and receive no socket mount.

`DURPDEPLOY_CONTAINER_NAMESPACE` defaults to `durpdeploy`.

Local deployments also require the digest-pinned Alpine staging helper from
the quick-start pull command above. Cache it in the runtime that executes the
steps, using the same Podman user or Docker endpoint. The runtime pulls a
missing helper before execution. For an offline installation, transfer this
image with the runtime's `save` and `load` commands alongside every step image
before starting deployments.

The helper keeps a per-deployment tmpfs volume mounted between local steps and
retries. This path is exercised against a rootless Podman 5.8.7 Unix-socket
service, including kernel byte/inode bounds and success, failure, and cancel
cleanup. To verify another rootless installation, run the real API test against
its socket:

```bash
go tool templ generate
make swagger-ui-copy
DURPDEPLOY_CONTAINER_RUNTIME=podman \
DURPDEPLOY_CONTAINER_URL="unix://${XDG_RUNTIME_DIR}/podman/podman.sock" \
go test -tags=e2e -count=1 -run '^TestDeploymentStagingCleanupE2E$' ./internal/handler/api
```

Set `DURPDEPLOY_EMBEDDED_AGENT_ENABLED=false` to disable server-side container
execution. Remote mTLS agents remain available. The Helm chart disables the
embedded agent because native Kubernetes Job execution is tracked in
[issue #98](https://github.com/DeveloperDurp/DurpDeploy/issues/98); use
standalone agents for executable steps in Kubernetes.

Each attempt runs as non-root with a read-only root filesystem, no network by
default (`network_mode: "bridge"` explicitly enables networking for local steps),
no capabilities, no new privileges, a bounded process count, and no
host mounts. A 64 MiB temporary filesystem at `/tmp` supplies its writable
home; artifact-gated steps use 364 MiB to preserve provider context.
DurpDeploy imposes no per-step RAM ceiling or CPU quota, including on attempts
sharing `/stage`. Applicable host/runtime limits still govern execution;
operators must account for scripts consuming more host RAM and CPU. Limits on
the control plane do not automatically constrain sibling step containers
started through the runtime socket. The staging helper retains its own RAM
and CPU limits, and temporary-filesystem byte/inode bounds remain in place.
Remote-agent container limits are tracked separately in
[agent issue #14](https://github.com/DeveloperDurp/durpdeploy-agent/issues/14).
`TERM=dumb` keeps non-interactive
logs free of terminal control codes.
It receives its script on stdin and all compatible resolved release
variables by default; `variable_names` restricts the step when it is non-empty.
Images supply their own interpreter and tools. Tags are mutable even
inside an immutable release snapshot, so use digest-pinned references for
reproducibility. The mounted runtime socket gives the DurpDeploy process
host-level container control; use a dedicated host or standalone agent when
the control plane is exposed to untrusted users. Steps needing network or host
filesystem access should run
on an appropriately isolated remote agent instead. Secret redaction is
best-effort; anyone who controls the runtime can manage its containers and see
the secrets sent to them.

Old deployments and logs remain readable, but old server-side steps without
images must be recreated and captured in a **new release**. Old releases
cannot run, re-run, or refresh; due schedules that point at one disable with
an actionable `last_error`. Agent steps retain host execution on their agent.

---

## Reverse proxy trust

DurpDeploy applies login rate limits itself. It trusts forwarding headers from
loopback proxies by default. If Caddy or another trusted proxy is on a different
host, set `DURPDEPLOY_TRUSTED_PROXIES` to its IP address or CIDR. Multiple
entries are comma-separated. Only those peers may supply `X-Forwarded-For`.
The bundled Compose file assigns Caddy a fixed private address and trusts only
that address, not the rest of the container network.

If a CDN or load balancer sits in front of Caddy, also set
`DURPDEPLOY_CADDY_TRUSTED_PROXIES` in Caddy's environment to that provider's
documented egress CIDRs. Caddy parses the incoming chain from right to left,
then sends DurpDeploy one canonical client address. Do not use broad public or
private ranges: every configured address is authorized to speak for clients.

---

## Verify the installation

Open your public URL, sign in with the admin account, and confirm the dashboard
loads. Configure optional [notifications](notifications.md) through the UI.
If startup or TLS fails, check:

```bash
docker compose ps
docker compose logs --tail=100 app caddy litestream
```

---

## Maintenance

For backups, restore drills, audit pruning, and Litestream health alerts,
follow [backup and maintenance](backup-restore.md). Back up the matching
server encryption key and agent-listener identity with the database.

---

## Troubleshooting

### Forgot the admin password

There is no email-reset flow. Another administrator can reset the password
through **Admin → Users → Edit**, preserving the account's role and memberships.
If no administrator can log in, create a separate recovery administrator using
an unused email:

```bash
docker compose run --rm --no-deps app admin create \
    --email recovery-admin@example.com --password '<new-password>'
```

Log in as the recovery administrator and edit the original account. Password
changes invalidate that user's browser sessions and pending challenges; revoke
leaked API tokens separately. Remove the temporary recovery account afterward.
Deleting and recreating the original user loses memberships and token records.

### The dashboard loads but deploys fail

Check deployment logs and the configured container socket. A missing
image, unavailable runtime, or interpreter absent from the image fails the
step; installing the interpreter on the DurpDeploy host does not help. The
runner removes attempts on completion, timeout, cancellation, and shutdown,
and reconciles its labelled containers on startup. A cleanup error is not a
confirmed cancellation; inspect the container runtime before retrying. There
is no direct host execution or development-mode fallback for server steps.
