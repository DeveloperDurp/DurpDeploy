# durpdeploy Helm chart

Single-binary deploy tool (`/usr/local/bin/durpdeploy`) on Kubernetes with
PostgreSQL as the backing store. Mirrors the layout of the Docker Compose
stack in this repo, but uses `externalPostgres` instead of the
SQLite + Litestream pair.

## TL;DR

```bash
# 1. Create the namespace (or use an existing one).
kubectl create namespace durpdeploy

# 2. Create operator-managed secrets in the release namespace.
KEY=$(openssl rand -base64 32)
PG_PASSWORD=$(openssl rand -hex 32)
kubectl create secret generic durpdeploy-secret-key -n durpdeploy \
  --from-literal=secret-key="$KEY"
kubectl create secret generic durpdeploy-postgres -n durpdeploy \
  --from-literal=password="$PG_PASSWORD"
# Configure your Postgres server with that same password before continuing.
unset KEY PG_PASSWORD

# 3. Install with the existing secrets.
helm install durpdeploy ./charts/durpdeploy \
  --namespace durpdeploy \
  --set secretKey.existingSecret=durpdeploy-secret-key \
  --set postgres.existingSecret=durpdeploy-postgres \
  --set externalPostgres.host=my-rds.example.com \
  --set externalPostgres.port=5432 \
  --set externalPostgres.database=durpdeploy \
  --set externalPostgres.username=durpdeploy

# 4. Create the first admin (the pod must be Running).
ADMIN_PASS='change-me-strong'
kubectl exec deploy/durpdeploy -n durpdeploy -- \
  durpdeploy admin create --email admin@example.com --password "$ADMIN_PASS"

# 5. Port-forward and log in.
kubectl port-forward -n durpdeploy svc/durpdeploy 8080:80
open http://localhost:8080
```

## What the chart does (and does not)

These names assume the release name `durpdeploy` and default name overrides.
Use the names printed by Helm for another release name. Keep the encryption
key in restricted backup storage with the matching database backup.

The Deployment loads the password Secret into `PGPASSWORD` before `DURPDEPLOY_DB`.
The pgx driver reads this password separately from the DSN.
The chart percent-encodes connection fields and does not interpolate the password into the URL.
The default `postgres.enabled: false` does not create a password Secret;
operators must supply one. Manual Secret changes require a Deployment restart;
the checksum annotation changes only when Helm renders changed values.

For additional connection options, supply a complete DSN through `externalPostgres.dsnSecret`.
This Secret replaces the connection fields and the password Secret reference.
Percent-encode reserved characters in each DSN value, including the username and password.
Encode spaces as `%20`.
For example, a synthetic password `p@ss:word` becomes `p%40ss%3Aword`.

```yaml
externalPostgres:
  dsnSecret:
    name: durpdeploy-connection
    key: dsn
```

Create this Secret in the release namespace with a `dsn` key containing the complete PostgreSQL URI.
Keep credentials out of committed values files.
Existing `extraEnv` overrides for `DURPDEPLOY_DB` remain supported.
Use `dsnSecret` for new configurations to avoid duplicate environment entries.

| ✓ Renders                                       | ✗ Does not                                       |
|-------------------------------------------------|--------------------------------------------------|
| Deployment (single replica, non-root, RO root)  | A Postgres server (bring your own)               |
| Service (ClusterIP :80 → :8080)                 | Litestream (Postgres replaces SQLite + Litestream) |
| ServiceAccount (no token automount)             | A Caddy reverse proxy (use an Ingress)           |
| Secret for the encryption key                   | A baked admin user (you create it post-install)  |
| Optional Secret for the Postgres password       | Anything TLS / LetsEncrypt (terminate at Ingress) |
| Ingress, HPA, PDB (opt-in)                      |                                                  |

The chart assumes you'll handle Postgres through whatever you already
use — managed (RDS, Cloud SQL, Aurora, Supabase) or self-hosted
(CloudNative-PG, Zalando, Crunchy). If you want a real subchart for
that, add it as a dependency and remove the `externalPostgres.*` fields
from `values.yaml`; this chart deliberately does not bundle one.

## Replicas

Keep the default `1`. Schedulers, log brokers, and cancellation tracking are
process-local. Multiple schedulers can trigger the same due schedule, and live
SSE delivery is not shared between pods. Deployment admission uses database
transactions and conditional claims, but that does not make the whole server
safe for multiple replicas. Leave autoscaling disabled until these components
support coordinated operation.

## Encryption key rotation

The CLI currently skips encrypted TOTP seeds, stored agent identity ciphertext,
and remote log scrub buffers. Do not rotate instances containing those records;
see [the current limitation](../../docs/security.md#key-rotation-runbook).

For an eligible instance, finish workloads and scale the Deployment to zero.
Run `durpdeploy secret-key rotate` in a separate utility pod or Job with the
same database connection and current encryption-key Secret. It generates the
new key itself. Install the **exact printed key**, then start the Deployment.
Generating another key with OpenSSL cannot decrypt the rotated database.
Use the release namespace on every command and keep the old key with older
backups. For the sample installation, the key Secret is
`durpdeploy-secret-key` and the Deployment is `durpdeploy` in namespace
`durpdeploy`.

## Backup

Postgres backups are your platform's job, not the chart's. Litestream
(the SQLite backup mechanism) is intentionally absent — see [the backup runbook](../../docs/backup-restore.md)
in the main repo for the S3-compatible approach when using SQLite.

## Values reference

See `values.yaml`. Notable knobs:

- `replicaCount` — keep at 1 unless you've read the comment above.
- `externalPostgres.*` — connection fields, or `externalPostgres.dsnSecret` for a complete DSN.
- `secretKey.existingSecret` / `postgres.existingSecret` — bring your
  own Secrets to integrate with External Secrets / Sealed Secrets / SOPS.
- `ingress.enabled` — front with cert-manager + your IngressController
  for TLS.
- `extraEnv` — pass through `DURPDEPLOY_SMTP_*` or a complete database DSN.
  Discord webhook URLs are configured through the app, not server environment variables.
- `embeddedAgent.enabled` — keep disabled until native Kubernetes Job
  execution is available in
  [issue #98](https://github.com/DeveloperDurp/DurpDeploy/issues/98).
  Standalone agents remain available for executable steps.

## Uninstalling

```bash
helm uninstall durpdeploy -n durpdeploy
kubectl delete pvc -n durpdeploy -l app.kubernetes.io/instance=durpdeploy
```

Postgres data is not touched — drop the database in your managed
instance or destroy the Postgres server per its own operator docs.
