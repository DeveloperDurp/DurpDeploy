# DurpDeploy — Backup & Restore Runbook (P1-6)

DurpDeploy's SQLite database (`durpdeploy.db`, WAL mode) is the sole source of
truth for projects, releases, deployments, variables, and the audit log.
There is no other durable copy unless you set one up. This runbook covers two
options:

1. **Litestream** (recommended) — continuous WAL streaming to S3-compatible
   storage. Point-in-time recovery, seconds of data loss at most.
2. **Cron fallback** — a daily `sqlite3 .backup` + `rsync` if you don't want
   to manage an S3 bucket.

Pick one. Litestream is strictly better if you already have (or can create)
an S3-compatible bucket (AWS S3, MinIO, Backblaze B2, Cloudflare R2, etc).

---

## Option 1 — Litestream (continuous replication)

Litestream tails the SQLite WAL and streams changed pages to object storage
as they're written. It runs as its own systemd service, alongside — not
inside — the `durpdeploy` service.

### Install

```bash
curl -L https://github.com/benbjohnson/litestream/releases/download/v0.5.14/litestream-0.5.14-linux-x86_64.tar.gz \
  | sudo tar -xz -C /usr/local/bin litestream
litestream version
```

Pin an exact version rather than trusting the "latest" redirect in a real
provisioning script — the download URL above will need bumping when you
upgrade. Debian/Ubuntu also has a `.deb` release asset if you prefer
`dpkg -i`; check the
[releases page](https://github.com/benbjohnson/litestream/releases) for
the current version and the right asset for your architecture (`x86_64` vs
`arm64`).

### Configure

Copy the template from this repo and fill in your bucket:

```bash
sudo install -d -m 0750 -o durpdeploy -g durpdeploy /etc/litestream
sudo install -m 0640 -o root -g durpdeploy systemd/litestream.yml /etc/litestream.yml
sudo $EDITOR /etc/litestream.yml   # fill in bucket, endpoint, credentials
```

See `systemd/litestream.yml` in this repo for the full commented template.
At minimum you need to set:

- `dbs[0].path` — defaults to `/var/lib/durpdeploy/durpdeploy.db` (matches
  `systemd/durpdeploy.service`'s `WorkingDirectory` + `DURPDEPLOY_DB`).
- `dbs[0].replicas[0].bucket` / `path` — your S3 (or S3-compatible) bucket
  and key prefix.
- `dbs[0].replicas[0].endpoint` — only needed for non-AWS S3-compatible
  stores (MinIO, R2, B2). Omit for real AWS S3.
- Credentials — via `access-key-id` / `secret-access-key` in the file, or
  (preferred) `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY` env vars in the
  systemd unit so the key never sits in a plaintext YAML file.

### Run as a systemd service

```bash
sudo install -m 0644 systemd/litestream.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now litestream
sudo systemctl status litestream
```

### Verify replication is healthy

```bash
sudo -u durpdeploy litestream ltx -config /etc/litestream.yml /var/lib/durpdeploy/durpdeploy.db
```

This lists the LTX (transaction) files currently held in the replica. A
healthy setup shows entries with a `max_txid` that keeps advancing every
time you re-run the command. An empty list, or a `max_txid` that stops
advancing, means replication is stuck — check `journalctl -u litestream`
first. (Older Litestream releases, pre-v0.4, called this command
`snapshots`; this runbook targets the current `ltx`/LTX-based CLI.)

```bash
journalctl -u litestream -n 50 --no-pager
```

Run the `snapshots` check as a periodic cron/monitoring job (e.g. hourly) so
a stalled replica pages someone instead of being discovered during an actual
outage.

### Restore (disaster recovery)

On a fresh VM (or after wiping a corrupted local DB):

```bash
sudo systemctl stop durpdeploy
sudo -u durpdeploy litestream restore -config /etc/litestream.yml \
  -o /var/lib/durpdeploy/durpdeploy.db \
  /var/lib/durpdeploy/durpdeploy.db
sudo systemctl start durpdeploy
```

To restore to a specific point in time instead of "latest":

```bash
sudo -u durpdeploy litestream restore -config /etc/litestream.yml \
  -timestamp 2026-07-18T12:00:00Z \
  -o /var/lib/durpdeploy/durpdeploy.db \
  /var/lib/durpdeploy/durpdeploy.db
```

`litestream restore` writes to a temp file and renames it into place, so a
restore that's interrupted partway through does not leave a corrupt
`durpdeploy.db` — the original file (or nothing, on a fresh VM) is left
untouched until the restore completes successfully.

**Test this monthly.** A backup you have never restored is a backup you
don't have. Spin up a scratch VM, run the restore steps above against a
copy of your production `litestream.yml` (read-only credentials are enough),
and confirm the resulting `durpdeploy.db` opens and the row counts look
sane (`sqlite3 durpdeploy.db 'select count(*) from deployments;'`).

`scripts/test-backup-restore.sh` in this repo automates an end-to-end
version of this drill against a local replica directory (no S3 bucket
required) — see that script for the exact sequence.

---

## Option 2 — Cron fallback (no S3 required)

If you don't want to run Litestream or manage a bucket, a daily
`sqlite3 .backup` plus offsite copy is a reasonable minimum. This gives you
daily-granularity recovery (worst case: lose up to 24h of data) rather than
Litestream's near-continuous replication.

### Backup script

```bash
sudo tee /usr/local/bin/durpdeploy-backup.sh >/dev/null <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
SRC=/var/lib/durpdeploy/durpdeploy.db
DEST_DIR=/var/backups/durpdeploy
DATE=$(date +%F)

install -d -m 0750 -o durpdeploy -g durpdeploy "$DEST_DIR"
sqlite3 "$SRC" ".backup '$DEST_DIR/durpdeploy-$DATE.db'"

# Keep 14 days locally.
find "$DEST_DIR" -name 'durpdeploy-*.db' -mtime +14 -delete

# Ship offsite. Replace with your own destination (rsync, rclone, S3, etc).
# rsync -av "$DEST_DIR/durpdeploy-$DATE.db" backup-host:/backups/durpdeploy/
EOF
sudo chmod +x /usr/local/bin/durpdeploy-backup.sh
```

`sqlite3 .backup` uses SQLite's online backup API — it is safe to run while
`durpdeploy` is live and writing to the WAL; it does not require stopping
the service.

### Cron entry

```bash
sudo crontab -u durpdeploy -e
# add:
0 3 * * * /usr/local/bin/durpdeploy-backup.sh >> /var/log/durpdeploy-backup.log 2>&1
```

### Restore

```bash
sudo systemctl stop durpdeploy
sudo -u durpdeploy cp /var/backups/durpdeploy/durpdeploy-2026-07-18.db \
  /var/lib/durpdeploy/durpdeploy.db
sudo rm -f /var/lib/durpdeploy/durpdeploy.db-shm /var/lib/durpdeploy/durpdeploy.db-wal
sudo systemctl start durpdeploy
```

Removing the stale `-shm`/`-wal` sidecar files is required — they belong to
the old database and starting the server against a fresh `.db` file with
leftover WAL sidecars from a different point in time can confuse SQLite's
WAL recovery.

---

## Which one should I use?

| | Litestream | Cron fallback |
|---|---|---|
| Data loss on crash | Seconds | Up to 24h |
| Requires S3 bucket | Yes | No |
| Point-in-time restore | Yes (`-timestamp`) | Daily granularity only |
| Setup effort | Moderate (bucket + credentials) | Low |

Litestream is the recommended default for anything you'd call "production."
The cron fallback exists for the case where standing up an S3-compatible
bucket is a bigger lift than accepting daily-granularity backups.

## Edge cases

- **Empty database**: both options work fine on a freshly-migrated,
  empty `durpdeploy.db` — Litestream just replicates an (almost) empty WAL,
  and `sqlite3 .backup` produces a tiny file. No special-casing needed.
- **WAL checkpoints**: durpdeploy opens SQLite with `journal_mode=WAL`
  (see `cmd/server/main.go`'s DSN). Litestream is designed around WAL mode
  and handles checkpoints (including ones triggered by the Go server's own
  connection pool) by shipping WAL frames before they're checkpointed away;
  no configuration is needed on the durpdeploy side.
- **Interrupted restore**: `litestream restore` restores to a temp path and
  renames atomically, so a killed/interrupted restore never leaves a
  half-written `durpdeploy.db` in place. For the cron fallback, `cp` is not
  atomic — if you interrupt a restore mid-`cp`, re-run the `cp` from a known
  good backup file before starting the service.
