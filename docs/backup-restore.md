# Backup, restore, and maintenance

The database contains projects, releases, deployments, credentials, MFA state,
and the audit log. Back it up with its matching server encryption key and the
server agent-listener identity directory. A database without its matching key
cannot recover encrypted state. Preserve the identity when agents must retain
their existing server trust pin. Keep keys separately with restricted access,
including old keys needed by pre-rotation backups. See the
[current rotation limitation](security.md#key-rotation-runbook).

In Compose, these are the `durpdeploy-data` and `durpdeploy-agent-identity`
volumes and `secrets/durpdeploy_key`. Remote agent state is separate: back up
its private state directory when you need to preserve that enrolled identity.
Never delete a volume as a recovery shortcut.

This guide covers SQLite. PostgreSQL and SQL Server need their own native
backup procedures, alongside the same key and identity backups.

## Option 1 — Litestream (continuous replication)

The [Compose stack](deploy.md) runs Litestream as a separate sidecar sharing
`/data/durpdeploy.db` with the app. It uses **Litestream 0.3**. Use that version's
configuration and commands; newer LTX commands do not apply to this image.
Replication is asynchronous: recovery reaches the latest successfully uploaded
data, and an unavailable replica can increase data loss.

### Configure and verify

Edit [`deploy/litestream.example.yml`](../deploy/litestream.example.yml), the
file mounted at `/etc/litestream.yml`. Set the bucket, unique replica prefix,
region, and optional S3-compatible endpoint. Put `AWS_ACCESS_KEY_ID`,
`AWS_SECRET_ACCESS_KEY`, and `LITESTREAM_S3_BUCKET` in `compose.litestream.env`.
Keep that file private. Start the stack using the deployment guide, then check:

```bash
docker compose logs --tail=100 litestream
docker compose run --rm --no-deps litestream snapshots \
  -config /etc/litestream.yml /data/durpdeploy.db
docker compose run --rm --no-deps litestream wal \
  -config /etc/litestream.yml /data/durpdeploy.db
```

The [0.3 snapshots](https://litestream.io/v0.3/reference/snapshots/) and
[WAL commands](https://litestream.io/v0.3/reference/wal/) list replica data.
Check that uploaded WAL entries advance after a known database write; an idle
database need not advance. A successful command or table header alone does
not prove freshness. Alert on replication errors and periodically test a restore.

### Restore

Finish or cancel deployments and wait for confirmed remote cleanup before
stopping the app. Keep the old database and sidecars for investigation.
Restore to a new path first:

```bash
docker compose stop app litestream
docker compose run --rm --no-deps --user 10001:10001 litestream restore \
  -config /etc/litestream.yml -o /data/durpdeploy-restored.db \
  /data/durpdeploy.db
```

The [0.3 restore command](https://litestream.io/v0.3/reference/restore/) refuses
an existing output path. Choose another unused output filename for each retry.
For point-in-time recovery, add `-timestamp <RFC3339-time>` before the database
path; the requested time must be covered by retained replica data.

Before promoting the restored file, use a SQLite tool or an isolated recovery
instance to check integrity and expected records. With all database writers
still stopped, archive the original `durpdeploy.db`, `durpdeploy.db-wal`, and
`durpdeploy.db-shm` together outside the live database path. Move the verified
restored file to `/data/durpdeploy.db`, retaining ownership `10001:10001`.
Restore the matching key and server identity, then recreate the app:

```bash
docker compose up -d --force-recreate app litestream
docker compose logs --tail=100 app litestream
```

Keep the public origin unchanged to preserve passkey validity. Check login,
projects, release snapshots, deployment history, and agent trust before
resuming deployments. A database restore does not recover files created by
scripts on deployment targets.

Test recovery monthly in an isolated environment with read-only replica
credentials. Keep the recovered server disconnected from production agents
and execution runtimes, disable schedules before starting it, and prevent
outbound notifications. Compare expected records before declaring recovery
successful. Never test by deleting production data.

## Option 2 — Scheduled SQLite backup

For a host-managed database, SQLite's online backup command can create a
consistent backup while the app is writing:

```bash
umask 077
sqlite3 /absolute/path/durpdeploy.db \
  ".backup '/absolute/backup/path/durpdeploy-backup.db'"
```

Install the SQLite CLI separately and run this as an account with access to
the database, WAL, and backup directory. In Compose, the database is inside
a named volume and the app image has no SQLite CLI; supply a backup tool
with access to that volume rather than running the command inside the app.
Do not copy only a live database file: committed data may still be in its WAL.

Schedule backups with your host's scheduler, alert on failures, and copy them
offsite. A daily schedule can lose up to a day's writes when backups succeed;
failed backups extend that window. Keep dated backups and their matching keys,
and test restores. For recovery, stop every database writer and replication
process, verify the backup, and follow the same archive-and-promote procedure
above. Never reuse the old WAL or SHM with the restored database.

## Backup health monitoring

DurpDeploy optionally runs `DURPDEPLOY_LITESTREAM_CHECK_COMMAND` through
`/bin/sh -c` as the app account. A zero exit status means healthy. Set
`DURPDEPLOY_LITESTREAM_CHECK_INTERVAL` to a positive Go duration (default `1h`).
An empty command disables checks.

The app image has no Litestream binary and cannot run commands inside the
sidecar by itself. Supply a trusted check executable and its required tools
inside the app's execution environment before enabling this feature. For
Compose, set these variables in `compose.app.env` and recreate the app with
`docker compose up -d app`. The check must return nonzero for missing or stale
replica data; listing output alone is insufficient.

Failed checks publish `backup_unhealthy`; the first successful check after a
failure publishes `backup_healthy`. Configure their global channels at
`/admin/notifications/settings` and inspect delivery at `/admin/notifications`.

## Audit log retention

Audit rows accumulate until you prune them. Run the CLI against the same
database as the app:

```bash
docker compose exec -T app su-exec 10001:10001 \
  /usr/local/bin/durpdeploy audit prune --days 90
```

Pruning preserves rows linked to live deployments or releases. Retention is
resolved from `--days N`, then `DURPDEPLOY_AUDIT_RETENTION_DAYS`, then the
180-day default. The command prints the retention period and cutoff. Schedule
it with your own host scheduler, from the stack's working directory, and
monitor failures. The repository does not install a pruning schedule.
