# Backup, restore, and maintenance

The database contains projects, releases, deployments, credentials, MFA state,
and the audit log. Back it up with its matching server encryption key and the
server agent-listener identity directory. A database without its matching key
cannot recover encrypted state. Preserve the identity when agents must retain
their existing server trust pin. Keep keys separately with restricted access,
including old keys needed by pre-rotation backups. See the
[key rotation procedure](security.md#key-rotation-runbook).

In Compose, these are the `durpdeploy-data` and `durpdeploy-agent-identity`
volumes and `secrets/durpdeploy_key`. Remote agent state is separate: back up
its private state directory when you need to preserve that enrolled identity.
Never delete a volume as a recovery shortcut.

This guide covers SQLite backup procedures and automated recovery checks for all supported databases.
PostgreSQL and SQL Server use native backup tools alongside the same key and identity backups.

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

## Automated recovery drill

Run the isolated recovery drill on Linux amd64:

```bash
DURPDEPLOY_CONTAINER_RUNTIME=docker make backup-restore-test
```

The default database is SQLite.
Select PostgreSQL or SQL Server with `DURPDEPLOY_RESTORE_BACKEND`:

```bash
DURPDEPLOY_CONTAINER_RUNTIME=docker DURPDEPLOY_RESTORE_BACKEND=postgres make backup-restore-test
DURPDEPLOY_CONTAINER_RUNTIME=docker DURPDEPLOY_RESTORE_BACKEND=mssql make backup-restore-test
```

The accepted values are `sqlite`, `postgres`, and `mssql`.
An unsupported value fails before resource creation.

Start the selected Docker or Podman socket before the drill.
If you use Podman, set `DURPDEPLOY_CONTAINER_RUNTIME=podman`.
Always select the runtime explicitly.
If you use Podman, select its socket with `DURPDEPLOY_CONTAINER_URL` or `DOCKER_HOST`.

For a rootless Podman socket, run:

```bash
export DURPDEPLOY_CONTAINER_URL="unix://${XDG_RUNTIME_DIR}/podman/podman.sock"
DURPDEPLOY_CONTAINER_RUNTIME=podman make backup-restore-test
```

Use a test host without `/etc/durpdeploy/key`.
The drill refuses that installed key to prevent access to operator secrets.
The drill requires Go, npm, and a working container runtime.
Missing tools or runtime access fail the check.
Other architectures fail before the drill creates resources.

If `DURPDEPLOY_CONTAINER_URL` is set, the drill uses that socket for application execution, backup containers, and cleanup.
Otherwise, the cleanup checks use the Testcontainers provider selected through `DOCKER_HOST` or default discovery.

The drill builds the server and uses disposable database data.
SQLite uses a digest-pinned Litestream 0.3.13 container with a local file replica.
The SQLite backup container has no network access.
PostgreSQL 16 and SQL Server 2022 use separate, digest-pinned source and target containers.
Published database ports bind to loopback.
Both server listeners and the SMTP receiver bind to loopback.
The drill uses generated test credentials and excludes operator application settings.
Archive files and matching keys stay inside the runner-owned private directory.

SQLite interruption checks allow four minutes for setup and use a five-minute global test timeout.
Native database interruption checks allow nine minutes for setup and use a ten-minute global test timeout.
The active workload has a thirty-minute timeout.
They check application namespace labels and Testcontainers session labels after interruption.
They also check removal of owned anonymous database volumes.
Each cleanup operation has an independent one-minute deadline.
Cleanup errors fail the check.

The drill checks these recovery results:

- Backup data includes the final committed API write.
- Recovery succeeds after removal of the source database and its storage.
- The restored database passes its integrity check.
- The restored password permits a new browser login.
- The saved API token permits application reads and writes.
- Project variables, release snapshots, and deployment logs remain available.
- A restored encrypted variable permits successful container execution.
- A local SMTP receiver receives successful deployment notifications.
- API and web notification history show the delivery result.
- A restored schedule creates a deployment and advances its next occurrence.
- The schedule remains disabled after the check.
- Missing and truncated backups cause restore failure on disposable destinations.
- A valid but mismatched application key prevents an encrypted-data API read.
- The matching key restores successful application operation.
- Restored sequences or identity counters allow new application writes.
- Existing and new Unicode data remain readable.
- Test timeout and interruption remove owned processes, containers, volumes, and temporary data.
- Cleanup preserves an unrelated container.

PR CI runs the same command in three independent jobs.
Their names are `Backup restore`, `Backup restore (PostgreSQL)`, and `Backup restore (SQL Server)`.
The drill removes its temporary data, processes, and containers after completion.
It does not use production backup storage or external notification services.
Continue the monthly isolated drill to verify actual backup access and deployment settings.
The interruption checkpoint retains a completed backup, source database, and active application workload.
It verifies interrupted-drill cleanup.
It does not verify crash consistency during every internal backup phase.

### PostgreSQL native recovery

The fixture gives a separate application role ownership of its application database.
It creates a custom-format archive with `pg_dump -Fc` after the final committed API write.
It copies the archive through the container SDK.
It removes the source container before creating any restore destination.

The destination contains an empty database owned by a newly provisioned application role.
The fixture restores as that role with `pg_restore --single-transaction --exit-on-error --no-owner --no-acl`.
It checks migration records, seeded tables, validated foreign keys, and the watermark before application startup.
It does not migrate the empty destination before restore.

This check covers one application database.
Cluster roles, tablespaces, extensions, and production privilege policies require separate procedures.
See the PostgreSQL [backup](https://www.postgresql.org/docs/16/app-pgdump.html) and [restore](https://www.postgresql.org/docs/16/app-pgrestore.html) documentation.

### SQL Server native recovery

The fixture creates a separate application database.
It grants its application user database-scoped `db_owner` membership for migration DDL.
The application login has no instance `sysadmin` membership.
An administrator connection performs provisioning, native backup, native restore, and integrity checks.

The fixture runs `BACKUP DATABASE` with `COPY_ONLY`, `CHECKSUM`, and `STOP_ON_ERROR`.
It copies the archive through the container SDK.
It removes the source container before creating any restore destination.
It gives the destination database process read access to its copied archive.

The fixture reads `RESTORE FILELISTONLY` and maps the expected data and log files with `WITH MOVE`.
It restores with checksum validation and `STOP_ON_ERROR`.
It runs `DBCC CHECKDB` after the actual restore.
It does not perform automatic corruption repair.

The destination login has a new SID and a generated password.
The fixture requires an application connection failure before remapping the restored database user.
It remaps that user with `ALTER USER ... WITH LOGIN`.
It checks restored `db_owner` membership and application reads and writes.

These local fixtures disable database connection encryption inside their isolated loopback setup.
Production database TLS checks and production login privileges require separate validation.
See the Microsoft [container restore](https://learn.microsoft.com/en-us/sql/linux/migrate/tutorial-restore-backup-sql-server-container?view=sql-server-ver16)
and [login mapping](https://learn.microsoft.com/en-us/sql/sql-server/failover-clusters/troubleshoot-orphaned-users-sql-server?view=sql-server-ver16) documentation.

All three checks exclude production backup storage, point-in-time recovery, cross-version upgrades, and restored agent trust.

## Backup health monitoring

DurpDeploy optionally runs `DURPDEPLOY_LITESTREAM_CHECK_COMMAND` through
`/bin/sh -c` as the app account. A zero exit status means healthy. Set
`DURPDEPLOY_LITESTREAM_CHECK_INTERVAL` to a positive Go duration (default `1h`).
An empty command disables checks.
An invalid or nonpositive interval prevents startup when checks are enabled.
Each check has a 30-second timeout.
On Unix systems, cancellation terminates the check's process group.
Failure messages retain only the last 4 KiB of command output.
Shutdown cancellation does not publish a backup failure.

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
