I am about to search and list relevant project files in your code directory.

Allow running this command?

**Feature Enhancement List for durpdeploy**

Based on a full read of the schema, runner, handlers, and existing plans, here are the most impactful additions, ordered roughly by value vs. effort.

---

**🔴 High value, low effort**

1. **Secret/masked variables** — add a `secret BOOLEAN NOT NULL DEFAULT 0` column to `variables` and `release_variables`. Mask the value in the UI and redact it from deployment logs. The runner already injects vars as env vars; no runner change needed.

2. **Configurable step timeout** — the runner hard-codes `5 * time.Minute` per step. Add a `timeout_seconds INTEGER` column to `steps` (and snapshot it into `steps_json`). Zero = use the current default.

3. **Deployment list N+1 fix** — `ListDeployments` in the handler does 3 extra queries per row (release, project, env). A single JOIN query in `queries/deployments.sql` + a new sqlc type eliminates this.

4. **Deployment notes / reason field** — add a `note TEXT` column to `deployments`. Expose a textarea on the deploy form. Useful for "why was this forced?" audit trail.

5. **Re-deploy button** — on the deployment detail page, a one-click "Re-run" that creates a new deployment with the same release + environment, bypassing the gate (since the prior stage already passed).

---

**🟡 Medium value, medium effort**

6. **Step retry on failure** — add `max_retries INTEGER NOT NULL DEFAULT 0` to `steps` (snapshotted in `steps_json`). The runner loop retries a failed step up to that count before marking the deployment failed.

7. **Deployment webhook / notification** — add a `webhooks` table (`project_id`, `url`, `events TEXT` e.g. `"success,failure"`). After `Run()` finishes, POST a JSON payload. No new dependency — stdlib `net/http`.

8. **Scheduled / cron deployments** — add a `scheduled_deployments` table (`project_id`, `release_id`, `environment_id`, `cron TEXT`, `next_run_at INTEGER`). A background goroutine (ticker every minute) fires due jobs. Covers nightly deploys to dev.

9. **Environment variable inheritance / override chain** — currently a variable with `environment_id = NULL` is a global default and one with an env ID overrides it. Expose this clearly in the UI with a "global / override" label and allow per-environment overrides to be added inline on the variables page.

10. **Deployment approval gate** — add an `approvals` table (`deployment_id`, `approved_by TEXT`, `approved_at INTEGER`). A lifecycle stage can be marked `requires_approval BOOLEAN`. The runner checks for an approval record before executing; the UI shows a pending-approval state with an Approve button.

---

**🟢 Lower priority / polish**

11. **Pagination on deployment list** — the list is unbounded. Add `LIMIT`/`OFFSET` to `ListDeployments` and HTMX-powered "Load more" or page controls.

12. **Search / filter on deployments list** — filter by project, environment, status, or date range via query params. Pure SQL `WHERE` additions, no new deps.

13. **Step template versioning** — `step_templates` currently have no version history. Add a `step_template_versions` table so you can see what a template looked like when a release was snapshotted.

14. **Dashboard / home page metrics** — the index page is sparse. Show: total deployments today, last deploy per project+env, any currently running deployments. One aggregate SQL query each.

15. **Dark/light mode toggle** — DaisyUI supports `data-theme`. An Alpine.js one-liner + `localStorage` persistence. Zero backend change.

16. **Export deployment log** — a `GET /deployments/{id}/logs.txt` endpoint that streams the stored `deployment_logs` rows as plain text. Useful for sharing or archiving.

17. **Health / readiness endpoint** — `GET /healthz` returning `{"status":"ok","db":"ok"}` with a quick `SELECT 1` probe. Required for any container orchestration.

---

**What's deliberately out of scope (YAGNI until asked)**
- Auth/RBAC — the guidelines explicitly note "no auth"; add only when multi-user is a real requirement.
- Parallel step execution — the sequential model is intentional and simpler to reason about.
- External secret stores (Vault, AWS SSM) — the masked-variable feature above covers 90% of the need with zero new deps.
