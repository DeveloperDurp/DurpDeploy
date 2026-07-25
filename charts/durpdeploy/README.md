# durpdeploy Helm chart

Single-binary deploy tool (`/usr/local/bin/durpdeploy`) on Kubernetes with
PostgreSQL as the backing store. Mirrors the layout of the Docker Compose
stack in this repo, but uses `externalPostgres` instead of the
SQLite + Litestream pair.

## TL;DR

```bash
# 1. Create the namespace (or use an existing one).
kubectl create namespace durpdeploy

# 2. Install. This renders the Secret and Deployment but the pod
#    won't start until you set the encryption key.
helm install durpdeploy ./charts/durpdeploy \
  --namespace durpdeploy \
  --set externalPostgres.host=my-rds.example.com \
  --set externalPostgres.port=5432 \
  --set externalPostgres.database=durpdeploy \
  --set externalPostgres.username=durpdeploy

# 3. Set the encryption key (helm NOTES.txt prints the exact command).
openssl rand -base64 32 | kubectl create secret generic \
  durpdeploy-durpdeploy-secret-key \
  --from-literal=secret-key=/dev/stdin \
  --dry-run=client -o yaml | kubectl apply -f -

# 4. Set the Postgres password.
openssl rand -base64 32 | kubectl create secret generic \
  durpdeploy-durpdeploy-postgres \
  --from-literal=password=/dev/stdin \
  --dry-run=client -o yaml | kubectl apply -f -
# ...configure your Postgres server with the same password.

# 5. Create the first admin (the pod must be Running).
ADMIN_PASS='change-me-strong'
kubectl exec deploy/durpdeploy-durpdeploy -n durpdeploy -- \
  durpdeploy admin create --email admin@example.com --password "$ADMIN_PASS"

# 6. Port-forward and log in.
kubectl port-forward -n durpdeploy svc/durpdeploy-durpdeploy 8080:80
open http://localhost:8080
```

## What the chart does (and does not)

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

Default is `1`. The runner executes bash on the pod and the startup
recovery path (`recoverPendingDeployments` in `cmd/server/main.go`) does
a non-atomic claim on `pending` rows. Two replicas would race the
`pending → running` transition. If you scale up, you also need to move
the queue out of the process — the chart won't stop you from setting
`replicaCount: 2`, but expect double-runs on restart.

## Encryption key rotation

```bash
# Generate, then run the rotate subcommand, then patch the Secret.
NEW_KEY=$(openssl rand -base64 32)
kubectl exec deploy/durpdeploy-durpdeploy -- \
  durpdeploy secret-key rotate          # uses the OLD key
kubectl create secret generic durpdeploy-durpdeploy-secret-key \
  --from-literal=secret-key="$NEW_KEY" \
  --dry-run=client -o yaml | kubectl apply -f -
kubectl rollout restart deploy/durpdeploy-durpdeploy
```

`rotate` decrypts every `variables` / `release_variables` row with the
old key and re-encrypts with a freshly-generated one (printed to
stdout). Install the printed value into the Secret *before* restarting
— between the rotate and the restart the binary keeps the old key.

## Backup

Postgres backups are your platform's job, not the chart's. Litestream
(the SQLite backup mechanism) is intentionally absent — see `docs/backup-restore.md`
in the main repo for the S3-compatible approach when using SQLite.

## Values reference

See `values.yaml`. Notable knobs:

- `replicaCount` — keep at 1 unless you've read the comment above.
- `externalPostgres.*` — required.
- `secretKey.existingSecret` / `postgres.existingSecret` — bring your
  own Secrets to integrate with External Secrets / Sealed Secrets / SOPS.
- `ingress.enabled` — front with cert-manager + your IngressController
  for TLS.
- `extraEnv` — pass through `DURPDEPLOY_SMTP_*`, `DURPDEPLOY_DISCORD_*`,
  etc. without forking the chart.

## Uninstalling

```bash
helm uninstall durpdeploy -n durpdeploy
kubectl delete pvc -n durpdeploy -l app.kubernetes.io/instance=durpdeploy
```

Postgres data is not touched — drop the database in your managed
instance or destroy the Postgres server per its own operator docs.
