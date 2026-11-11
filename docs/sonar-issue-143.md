# Native Ansible static analysis

The analysis for commit `05686837` reported Oracle PL/SQL maintainability rules on plain SQL files.
These files target SQLite, PostgreSQL, and SQL Server through migrations and sqlc.
They do not contain Oracle PL/SQL blocks.

| Rule | Files | Decision |
| --- | --- | --- |
| `plsql:S1192` | `migrations/075_ansible_steps.sql`, `migrations/mssql/060_ansible_steps.sql` | Keep literal defaults and allowed values in each table definition. DDL does not share a procedural constant across these statements. |
| `plsql:S1192` | `queries/step_templates.sql`, `queries/remote_steps.sql` | Keep the same stored-contract literals in independent sqlc statements. A shared procedural declaration is not available here. |
| `plsql:S1138` | `queries/remote_steps.sql` | Keep atomic presence and absence checks for eligibility, capacity, claims, and cleanup. Joins can change row multiplicity. |
| `plsql:S1192` | `queries/deployments.sql`, `queries/dashboard.sql` | Keep fixed stored-contract literals in independent sqlc statements. These statements cannot share an Oracle procedural constant. |
| `plsql:S1138` | `queries/deployments.sql`, `queries/dashboard.sql` | Keep membership checks and legacy check exclusions without changing row multiplicity. |
| `plsql:S1739` | `queries/deployments.sql` | Keep canonical legacy JSON matching with a `deployment_id` equality predicate. SQLite uses the deployment-step key index for this lookup. |
| `plsql:S1192` | `queries/project_groups.sql` | Keep canonical deployment-kind literals in the declarative catalog query. Oracle procedural constants are not available to sqlc. |
| `plsql:S1138` | `queries/project_groups.sql`, `queries/release_availability.sql` | Keep membership and legacy check exclusions without multiplying catalog rows or successful deployments. |
| `plsql:S1739` | `queries/project_groups.sql`, `queries/release_availability.sql` | Keep legacy check detection scoped by `deployment_id` before matching canonical JSON. |

The exceptions name each rule and file explicitly in `sonar-project.properties`.
All other SQL analysis remains enabled.
Database parity tests verify the stored defaults and deployment snapshots on all three supported databases.
Remote execution tests verify eligibility and cleanup admission.
Legacy check tests verify promotion, dashboard, current-version, and rollback eligibility across all three databases.
SQLite query-plan inspection confirms the deployment-step lookup uses its `deployment_id` index.

The project catalog uses one filtered query for rows and the window count.
An empty page repeats that query at offset zero to obtain its total.
This removes the duplicate count query without excluding SQL duplication analysis.
Release availability loads the latest release and its successful environments in one query.
The generated Go code requires no duplicate-literal exception.
API, browser, and database parity checks cover catalog pagination and release availability.

The test image defaults to user `65534:65534`.
The disposable SSH server fixture explicitly selects root for host keys and `sshd` startup.
Deployment workloads retain the runner's enforced non-root user.
The image installs only hash-verified binary wheels.
The image sorts complete version-pinned package arguments for the Docker analyzer.
