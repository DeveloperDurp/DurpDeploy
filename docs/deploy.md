# DurpDeploy — Production Deploy Runbook (Debian 12)

This walks through provisioning a fresh Debian 12 VM to run DurpDeploy behind
Caddy with automatic HTTPS. At the end you will have:

- A `durpdeploy` system user running the Go binary under systemd.
- Caddy in front, terminating TLS and reverse-proxying to `localhost:8080`.
- A first admin user created via the CLI.
- The dashboard reachable at `https://<your-host>/`.

**Time:** ~20 minutes on a fresh VM, assuming DNS is already pointed.

---

## Quick start: Docker (binary image)

The `Dockerfile` builds a static binary in a `scratch` image — there is no
shell, no package manager, and no Caddy in the image. It is a binary
distribution target, not a full AIO container. For local dev there is no
reason to use it (`make build && ./durpdeploy` is faster). For production
the Debian 12 runbook below is the recommended path; that runbook installs
Caddy and the systemd service as separate, well-understood units.

If you do want to run the binary in a container (e.g. behind a separate
Caddy container), the image runs as root by default with the binary at
`/durpdeploy` and CWD `/data` (mount a volume there for the SQLite file):

```bash
docker build -t durpdeploy .
docker run -d --name durpdeploy -p 8080:8080 \
  -v durpdeploy-data:/data \
  -e DURPDEPLOY_DB=/data/durpdeploy.db \
  durpdeploy
```

You still have to bootstrap the first admin with the CLI — there is no
env-var shortcut:

```bash
docker exec -it durpdeploy /durpdeploy admin create \
  --email admin@example.com --password '<strong-password>'
```

The Debian 12 runbook below is the recommended production path.

---

## Prerequisites

- A Debian 12 VM with a **public IP** and root/sudo access.
- A **DNS A record** pointing your hostname (e.g. `durpdeploy.example.com`)
  at that IP. Caddy cannot obtain a Let's Encrypt certificate without it.
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
user owns it; no other account can read it. `/var/log/caddy` is owned by root
because Caddy runs as root initially (it drops privileges to the `caddy` user
for the listener, but writes the log file as configured).

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

## Step 4 — Install the systemd unit

```bash
sudo install -m 0644 ./systemd/durpdeploy.service /etc/systemd/system/durpdeploy.service
sudo systemctl daemon-reload
sudo systemctl enable --now durpdeploy
sudo systemctl status durpdeploy --no-pager
```

You should see `active (running)`. If it fails, check the logs:

```bash
sudo journalctl -u durpdeploy -n 50 --no-pager
```

The service sets `DURPDEPLOY_DB=/var/lib/durpdeploy/durpdeploy.db`, so the
database is created in the right place on first start (migrations auto-run).

---

## Step 5 — Install the Caddyfile

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
> mitigation; rate limiting is defense in depth.

---

## Step 6 — Create the first admin user

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

## Step 7 — Verify

From your workstation:

```bash
curl -I https://durpdeploy.example.com
# Expect: HTTP/2 303 with Location: /login  (unauthenticated users are
# redirected to the login page)
```

Then in a browser:

1. Open `https://durpdeploy.example.com` → should redirect to `/login`.
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

## Backup

SQLite WAL mode makes a live `sqlite3 .backup` safe (it takes a consistent
snapshot without stopping the server). Two options:

### Option A — daily cron with `sqlite3 .backup` (simplest)

```bash
sudo install -d -o durpdeploy -g durpdeploy -m 0750 /var/backups/durpdeploy
# as root:
echo '0 3 * * * durpdeploy sqlite3 /var/lib/durpdeploy/durpdeploy.db ".backup /var/backups/durpdeploy/durpdeploy-$(date +\%F).db"' \
    | sudo tee /etc/cron.d/durpdeploy-backup
```

Then rsync `/var/backups/durpdeploy/` offsite (to S3, a NAS, another VM).
Keep at least 7 days of retention.

### Option B — litestream (continuous, point-in-time restore)

Litestream streams the SQLite WAL to S3-compatible storage continuously, so
you lose at most a few seconds of data on a crash. See
[litestream.io](https://litestream.io/) and `docs/backup-restore.md`
(planned P1-5). This is the recommended path for a team deployment.

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

- Use the staging endpoint while testing (add `acme_ca https://acme-staging-v02.api.letsencrypt.org/directory` to the Caddyfile's global options block), or
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
project's variables. P1-3 will harden the sandbox further.
