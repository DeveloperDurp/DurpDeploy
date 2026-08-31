# DurpDeploy — Production Deploy Runbook (Debian 12)

This walks through provisioning a fresh Debian 12 VM to run DurpDeploy behind
Caddy with automatic HTTPS. At the end you will have:

- A `durpdeploy` system user that runs the Go binary with systemd.
- Caddy in front, terminating TLS and reverse-proxying to `localhost:8080`.
- A first admin user created via the CLI.
- The dashboard reachable at `https://<your-host>/`.

**Time:** ~20 minutes on a fresh VM, assuming DNS is already pointed.

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
cp deploy/litestream.example.yml deploy/litestream.yml
$EDITOR deploy/litestream.yml   # fill in S3 bucket, path, region, etc.

# 2. Generate the encryption key (32 random bytes, base64)
mkdir -p secrets
openssl rand -base64 32 > secrets/durpdeploy_key
chmod 0600 secrets/durpdeploy_key

# 3. Create compose env files
cat > compose.caddy.env <<'EOF'
DURPDEPLOY_URL=https://durpdeploy.example.com
# Optional backend override (default: app:8080 in compose)
# BACKEND=app:8080
EOF

cat > compose.app.env <<'EOF'
# Optional OIDC block (omit all variables if you do not need SSO)
# DURPDEPLOY_OIDC_ISSUER=https://idp.example.com/realms/example
# DURPDEPLOY_OIDC_CLIENT_ID=durpdeploy-example
# DURPDEPLOY_OIDC_CLIENT_SECRET=REPLACE_WITH_CLIENT_SECRET
# DURPDEPLOY_OIDC_ADMIN_GROUP=durpdeploy-admin
# DURPDEPLOY_OIDC_DEPLOYER_GROUP=durpdeploy-deployer
# DURPDEPLOY_OIDC_VIEWER_GROUP=durpdeploy-viewer
# DURPDEPLOY_OIDC_DISPLAY_NAME=SSO
# DURPDEPLOY_OIDC_GROUP_CLAIM=groups
# DURPDEPLOY_OIDC_REQUIRE_EMAIL_VERIFIED=false  # default true

# Include any other app env your deployment needs, e.g. SMTP or secret key.
EOF

cat > compose.litestream.env <<'EOF'
LITESTREAM_S3_BUCKET=my-durpdeploy-backups
AWS_ACCESS_KEY_ID=AKIA...
AWS_SECRET_ACCESS_KEY=...
EOF

# Keep root .env out of the runtime path for compose app container env to avoid
# leaking AWS credentials into the application process.

# 4. Build and start
docker compose up -d --build

# 5. Bootstrap the first admin
docker compose exec app admin create \
  --email admin@example.com --password '<strong-password>'
```

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
* The server listener requires all three variables together:

  ```dotenv
  DURPDEPLOY_AGENT_LISTEN_ADDR=0.0.0.0:10943
  DURPDEPLOY_AGENT_PUBLIC_URL=https://<agent-control-host>
  DURPDEPLOY_AGENT_IDENTITY_DIR=/var/lib/durpdeploy/agent-identity
  ```

* An unpaired agent needs only a private state directory, temporary local
  pairing listener, and version. The administrator pairing flow stores the
  pinned server endpoint and agent identity; restarts use that paired state.

  ```dotenv
  DURPDEPLOY_AGENT_STATE_DIR=/var/lib/durpdeploy-agent
  DURPDEPLOY_AGENT_LISTEN_ADDR=0.0.0.0:10943
  DURPDEPLOY_AGENT_VERSION=<agent-version>
  ```

The optional Compose `agent` profile is a co-located demonstration only. It
has a private state volume and no server database, server key, Docker socket,
or inbound listener port. Production agents should run remotely on the host where
the deployment commands belong and must execute commands under the distinct
`durpdeploy-runner` identity.
Use either:

```bash
docker compose --profile agent up -d --build agent
podman compose --profile agent up -d --build agent
```

Do not run both commands for the same agent. For systemd, install
`systemd/durpdeploy-agent.service`; it requires the separate
`durpdeploy-runner` execution identity. The agent needs `CAP_SETUID`,
`CAP_SETGID`, `CAP_SETPCAP`, `CAP_SYS_ADMIN`, and `CAP_SYS_CHROOT` only while
it creates the read-only chroot, configures the cgroup, and drops to that
identity; `setpriv` clears every child capability set before Bash starts. Keep
`/etc/durpdeploy-agent.env` mode `0600`, and use the commands in
[`docs/agents.md`](agents.md) for start, status, logs, restart, and stop.

`CAP_SETPCAP` lets `setpriv` drop the child's entire capability bounding set.
The Docker Compose root entrypoint passes this limited setup set to the
non-root agent as ambient capabilities; no step inherits any of them.

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

OIDC is disabled unless an OIDC-specific variable is set. When enabled, all
necessary values must be present. Use placeholders for deployment-specific
values, never a client secret in documentation:

```ini
DURPDEPLOY_URL=https://<public-host>
DURPDEPLOY_OIDC_ISSUER=https://<issuer-host>
DURPDEPLOY_OIDC_CLIENT_ID=<client-id>
DURPDEPLOY_OIDC_CLIENT_SECRET=<secret>
DURPDEPLOY_OIDC_ADMIN_GROUP=<admin-group>
DURPDEPLOY_OIDC_DEPLOYER_GROUP=<deployer-group>
DURPDEPLOY_OIDC_VIEWER_GROUP=<viewer-group>
DURPDEPLOY_OIDC_DISPLAY_NAME=<display-name>
DURPDEPLOY_OIDC_GROUP_CLAIM=<claim-name>
DURPDEPLOY_OIDC_REQUIRE_EMAIL_VERIFIED=<true|false>
```

The issuer and `DURPDEPLOY_URL` must be HTTPS origins. Register exactly
`DURPDEPLOY_URL + /login/oidc/callback` at the provider. The application asks
for `openid`, `profile`, and `email`. The password login remains beside the SSO
link, and password login uses the most recently stored local role.

When unset, or set to `true`, the callback requires the ID token to contain the
literal JSON boolean `email_verified: true`. Explicit lowercase
`DURPDEPLOY_OIDC_REQUIRE_EMAIL_VERIFIED=false` accepts a present literal JSON
boolean `email_verified: true` or `email_verified: false`, after normal ID token
signature, issuer, audience, and nonce verification. Missing, null, string, and
numeric claims remain rejected. This weakens identity assurance, so use it only
where Authentik independently establishes address ownership. The first matching email links
exactly one local account. An email with no match is JIT-created with an empty
password. Groups map to admin, deployer, or
viewer, in that precedence order, and each successful OIDC login synchronizes
name, email, and role. A role change invalidates that user's browser sessions.
Group removal takes effect on the next OIDC login. There is no SCIM or provider
back-channel deprovisioning, and no instant deprovisioning between logins.

OIDC reauthentication is completed by the provider and then bound to the local
session and stored OIDC identity. Local logout clears only the DurpDeploy
session, not the provider session. The application does not persist provider
tokens, authorization codes, or raw claims, and OIDC does not issue or protect
API tokens. If provider discovery or login is unavailable, password login,
existing sessions, health checks, and bearer API authentication continue to work.
An OIDC-created empty-password account is recovered by an administrator through
the existing local user recovery process. There is no self-service password reset.

Use the Debian 12 bare-metal procedure if you must control the host. This
procedure includes a cgroup v2 sandbox and custom kernel settings. Compose is
sufficient for most small teams.

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

## Prerequisites

- A Debian 12 VM with a **public IP** and root/sudo access.
- A **DNS A record** pointing your hostname (e.g. `durpdeploy.example.com`)
  at that IP. Caddy cannot get a certificate from Let's Encrypt without it.
- Ports **80 and 443 open inbound**. The Go server listens on `localhost:8080`
  only — it is never exposed directly.

### Firewall (ufw)

```bash
sudo apt update && sudo apt install -y ufw
sudo ufw default deny incoming
sudo ufw default allow outgoing
sudo ufw allow 22/tcp        # SSH — restrict to your IP in production
sudo ufw allow 80/tcp        # Caddy HTTP (redirect + ACME challenge)
sudo ufw allow 443/tcp      # Caddy HTTPS
sudo ufw enable
sudo ufw status verbose
```

---

## Step 1 — Install Caddy

```bash
sudo apt install -y debian-keyring debian-archive-keyring apt-transport-https curl
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' \
  | sudo gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' \
  | sudo tee /etc/apt/sources.list.d/caddy-stable.list
sudo apt update
sudo apt install -y caddy
```

Verify:

```bash
caddy version
```

---

## Step 2 — Create the durpdeploy user and directories

```bash
sudo useradd --system --shell /usr/sbin/nologin --home /var/lib/durpdeploy durpdeploy
sudo install -d -o durpdeploy -g durpdeploy -m 0750 /var/lib/durpdeploy
sudo install -d -o root  -g root  -m 0750 /var/log/caddy
```

`/var/lib/durpdeploy` holds the SQLite database (and WAL/SHM). The `durpdeploy`
user owns it. No other account can read it. `/var/log/caddy` is owned by root
because Caddy initially operates as root. It changes to the `caddy` user for
the listener. It writes the log file as configured.

---

## Step 3 — Build and install the durpdeploy binary

Build on your **workstation** (not the VM — keeps the Go toolchain off prod):

```bash
# on your workstation, in the durpdeploy checkout
make build                       # produces ./durpdeploy
rsync -avz ./durpdeploy user@<vm-host>:/tmp/durpdeploy
```

Then on the VM:

```bash
sudo install -m 0755 /tmp/durpdeploy /usr/local/bin/durpdeploy
/usr/local/bin/durpdeploy version   # should print: durpdeploy dev
```

---

## Step 4 — Generate the secret encryption key

`variables`/`release_variables.value` is encrypted at rest (AES-256-GCM,
see `docs/security.md`). The server **refuses to boot** without a key, so
this has to exist before the first start:

```bash
sudo install -d -o durpdeploy -g durpdeploy -m 0750 /etc/durpdeploy
openssl rand -base64 32 | sudo -u durpdeploy tee /etc/durpdeploy/key >/dev/null
sudo chmod 0600 /etc/durpdeploy/key
```

As an alternative, set `DURPDEPLOY_SECRET_KEY` to the same base64, 32-byte value
in the systemd unit's `Environment=`. If the file and variable are present, the
server uses the file. If you lose this key, you cannot decrypt the stored
secrets. Back up the key with the database.

---

## Step 5 — Set up the runner sandbox (durpdeploy-runner user + cgroups)

Deployment steps no longer run as the `durpdeploy` user directly (P1-4). A
low-privileged `durpdeploy-runner` account is used instead, so a buggy or
malicious step script cannot read the SQLite DB or the secret key. The script cannot write
outside its own scratch chroot.

```bash
# Dedicated, unprivileged, no-login user for running step scripts.
sudo useradd --system --no-create-home --shell /usr/sbin/nologin durpdeploy-runner
# setpriv clears the service's sandbox-management capabilities before Bash.
command -v setpriv >/dev/null # provided by Debian/Ubuntu's util-linux package
```

The runner fails the deployment rather than falling back to `durpdeploy` if
this user, `setpriv`, a required chroot bind mount, or cgroup delegation is
unavailable.

Cgroup v2 is used to cap CPU/memory/PIDs per deployment. Create the parent
cgroup and hand ownership to the account running this host's deployment
service (`durpdeploy` for the server or `durpdeploy-agent` for a remote agent)
so it can create/remove per-deployment sub-cgroups without root:

```bash
sudo mkdir -p /sys/fs/cgroup/durpdeploy
sudo chown -R durpdeploy:durpdeploy /sys/fs/cgroup/durpdeploy
# Let durpdeploy write the controllers it needs in sub-cgroups it creates.
echo '+cpu +memory +pids' | sudo tee /sys/fs/cgroup/durpdeploy/cgroup.subtree_control
```

This directory does not survive a reboot (cgroupfs is virtual) — re-run the
above after every reboot, or add a small `systemd-tmpfiles` rule / oneshot
unit if you want it automated. For a remote agent, use the same commands with
`durpdeploy-agent:durpdeploy-agent`; Compose bind-mounts only this delegated
subtree. Missing, read-only, or incorrectly delegated cgroups fail the
deployment rather than disabling its resource limits.

---

## Step 6 — Install the systemd unit

```bash
sudo install -m 0644 ./systemd/durpdeploy.service /etc/systemd/system/durpdeploy.service
sudo systemctl daemon-reload
sudo systemctl enable --now durpdeploy
sudo systemctl status durpdeploy --no-pager
```

You must see `active (running)`. If it fails, check the logs:

```bash
sudo journalctl -u durpdeploy -n 50 --no-pager
```

The service sets `DURPDEPLOY_DB=/var/lib/durpdeploy/durpdeploy.db`, so the
database is created in the right place on first start (migrations auto-run).

---

## Step 7 — Install the Caddyfile

Edit `./Caddyfile` on your workstation and replace
`durpdeploy.example.com` with your real hostname, then:

```bash
rsync -avz ./Caddyfile user@<vm-host>:/tmp/Caddyfile
# on the VM
sudo install -m 0644 /tmp/Caddyfile /etc/caddy/Caddyfile
sudo systemctl reload caddy
sudo systemctl status caddy --no-pager
```

Caddy will obtain a Let's Encrypt certificate on the first request — watch the
logs if TLS setup fails (DNS not propagated, port 443 blocked, etc.):

```bash
sudo journalctl -u caddy -n 50 --no-pager
```

> **Rate limiting note:** the `rate_limit` directive requires a Caddy build
> with the `caddy-ratelimit` module (built-in from Caddy v2.8+). If your
> Caddy does not have it, rebuild with
> `xcaddy build --with github.com/mholt/caddy-ratelimit` or remove the
> `@login` / `rate_limit` block — argon2id's cost is the primary brute-force
> mitigation. Rate limiting is defense in depth.

---

## Step 8 — Create the first admin user

Run the CLI as the `durpdeploy` user so the DB file permissions stay correct:

```bash
# Generate a strong random password first:
openssl rand -base64 24
# Then create the admin (single-quote the password so the shell cannot
# interpret special characters — `$`, `!`, backticks, etc.):
sudo -u durpdeploy DURPDEPLOY_DB=/var/lib/durpdeploy/durpdeploy.db \
    /usr/local/bin/durpdeploy admin create \
        --email admin@example.com \
        --password '<paste-the-generated-password-here>'
```

Expected output:

```
Created admin user: admin@example.com
```

Keep the password somewhere safe (a password manager). There is no password
reset without DB access — see Troubleshooting below.

---

## Step 9 — Configure notifications (optional)

DurpDeploy supports event-driven Slack, Email, Gotify, and Discord notifications.

See [**`docs/notifications.md`**](notifications.md) for the full configuration guide, environment variables, and per-project setup steps.

---

## Step 10 — Verify

From your workstation:

```bash
curl -I https://durpdeploy.example.com
# Expect: HTTP/2 303 with Location: /login  (unauthenticated users are
# redirected to the login page)
```

Then in a browser:

1. Open `https://durpdeploy.example.com` → must redirect to `/login`.
2. Log in with the admin email + password you just created.
3. The dashboard renders.

If anything fails, check the audit log + server logs:

```bash
sudo journalctl -u durpdeploy -n 100 --no-pager
# The audit log is in the SQLite DB:
sudo -u durpdeploy sqlite3 /var/lib/durpdeploy/durpdeploy.db \
    'SELECT created_at, user_id, action FROM audit_log ORDER BY id DESC LIMIT 20;'
```

---

## Maintenance

### Audit Log Pruning

The `audit_log` table can grow large on busy instances. Use the CLI to prune logs older than a specified number of days:

```bash
# Prune logs older than 90 days
sudo -u durpdeploy /usr/local/bin/durpdeploy audit prune --days 90
```

You can automate this with a daily cron job:

```bash
# /etc/cron.d/durpdeploy-prune
0 4 * * * durpdeploy /usr/local/bin/durpdeploy audit prune --days 90
```

### Backup Health Monitoring (Litestream)

If you use Litestream, you can enable an automatic health check. The check sends
a notification if replication stops or has a delay. It supports Slack,
Discord, Gotify, and email.

1. Configure the check command and interval via environment variables in your systemd unit or environment file:

   ```ini
   # This command should exit with code 0 if healthy.
   # The example below checks if 'litestream ltx' returns any replication files.
   Environment=DURPDEPLOY_LITESTREAM_CHECK_COMMAND="litestream ltx -config /etc/litestream.yml /var/lib/durpdeploy/durpdeploy.db | grep -q ."
   Environment=DURPDEPLOY_LITESTREAM_CHECK_INTERVAL=1h
   ```

2. Reload and restart:

   ```bash
   sudo systemctl daemon-reload
   sudo systemctl restart durpdeploy
   ```

When the check command fails, a `backup_unhealthy` event is published to all projects that have notifications configured. A `backup_healthy` event is published once the command succeeds again. All health check events are also visible to admins at `/admin/notifications`.

---

## Backup

SQLite WAL mode permits a live `sqlite3 .backup` operation. The operation makes
a consistent snapshot without a server stop. Refer to
[`docs/backup-restore.md`](backup-restore.md) for setup, verification, and
restore instructions. It also describes the systemd templates and the
`scripts/test-backup-restore.sh` acceptance
test. The short version:

### Option A — daily cron with `sqlite3 .backup` (simplest)

```bash
sudo install -d -o durpdeploy -g durpdeploy -m 0750 /var/backups/durpdeploy
# as root:
echo '0 3 * * * durpdeploy sqlite3 /var/lib/durpdeploy/durpdeploy.db ".backup /var/backups/durpdeploy/durpdeploy-$(date +\%F).db"' \
    | sudo tee /etc/cron.d/durpdeploy-backup
```

Then rsync `/var/backups/durpdeploy/` offsite (to S3, a NAS, another VM).
Keep at least 7 days of retention.

### Option B — litestream (continuous, point-in-time restore) — recommended

Litestream streams the SQLite WAL to S3-compatible storage continuously, so
you lose at most a few seconds of data on a crash. This is the recommended
path for a team deployment — follow the "Litestream" section of
[`docs/backup-restore.md`](backup-restore.md) for install, configuration,
verification (`litestream ltx`), and restore (`litestream restore`)
steps.

---

## Troubleshooting

### Caddy cannot read the Caddyfile

```
Error: loading config: open /etc/caddy/Caddyfile: permission denied
```

AppArmor or SELinux is blocking it. On Debian, AppArmor is the usual suspect.
Check with `sudo aa-status`. If Caddy's AppArmor profile is in enforce mode,
either set it to complain mode while you debug:

```bash
sudo aa-complain /usr/bin/caddy
sudo systemctl restart caddy
```

or adjust the profile to allow reading `/etc/caddy/Caddyfile`.

### `durpdeploy` user cannot write to its DB

```
migration failed: attempt to write a readonly database
```

The `WorkingDirectory=/var/lib/durpdeploy` does not exist or is not owned by
the `durpdeploy` user. Re-run Step 2 — specifically the `install -d` line
that creates `/var/lib/durpdeploy` with the right ownership.

### Let's Encrypt rate limit

```
rate limited: too many certificates already issued for exact set of domains
```

Let's Encrypt allows 5 duplicate certificates per week. If you have been
reprovisioning the VM repeatedly, either:

- Use the staging endpoint during tests. Add `acme_ca https://acme-staging-v02.api.letsencrypt.org/directory` to the global options in the Caddyfile.
- Wait a week, or
- Reuse the certificate from a previous VM (copy `/var/lib/caddy/.local/share/caddy/certificates/`).

### Forgot the admin password

There is no email-reset flow. Reset via the CLI on the VM:

```bash
sudo -u durpdeploy sqlite3 /var/lib/durpdeploy/durpdeploy.db \
    "DELETE FROM users WHERE email='admin@example.com';"
sudo -u durpdeploy /usr/local/bin/durpdeploy admin create \
    --email admin@example.com --password '<new-password>'
```

(Deleting the user invalidates their sessions via the `sessions.user_id` foreign
key `ON DELETE CASCADE`.)

### The dashboard loads but deploys fail

Check the runner logs — the deploy runs `bash` steps via `os/exec`, inheriting
the `durpdeploy` user's environment. If a step needs a tool not in the
`durpdeploy` user's `PATH`, install it system-wide or set the variable in the
project's variables. Steps run in their own process group and are fully
reaped on timeout, cancel, or server shutdown (P1-3). Operating-system sandboxing
(chroot/namespaces/cgroups) is enabled by default if provisioned per Step 5.
