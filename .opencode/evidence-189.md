# Issue 189 verification

Use `.opencode/plan-189.md` for the approved scope and human risk review.
The implementation risk remains 5/10.
Revert the MR commits in reverse order to recover the previous behavior.
No schema or data recovery is required.

## Manual QA matrix

| Scenario | Action | Expected and observed | Artifact |
| --- | --- | --- | --- |
| Referenced environment | Confirm deletion in the environment form | Visible lifecycle stage count, unchanged form, environment remains | `.local/issue189-final-evidence/environment-conflict.png`, `manual-qa.json` |
| Stale variable scope | Submit a removed environment selection | Visible validation toast, retained draft, API 422 | `.local/issue189-final-evidence/variable-validation.png`, `manual-qa.json` |
| Stale deployment selection | Submit a removed environment selection | Visible validation toast and retained form | `.local/issue189-final-evidence/deployment-validation.png` |
| Delete after reference removal | Remove a lifecycle through its API, then delete its environment | API 204 | `/tmp/issue189-running-final-build.log` |
| Parent lost during admission | Inject a lost release lock in an isolated HTTP fixture | API and web 409, zero deployments, valid retry succeeds | `/tmp/issue189-admission-network-e2e.log` |
| Database parity and races | Run the complete repository suite | SQLite, PostgreSQL, and SQL Server checks pass | `/tmp/issue189-go-final-relevant.log` |
| Infrastructure errors | Run existing creation failure cases and classifier checks | Non-FK errors retain 500 behavior | `/tmp/issue189-final-handlers.log`, `/tmp/issue189-go-final-relevant.log` |
| Logs and schedules | Inspect the final retained stack | Zero internal request errors and zero enabled schedules | `.local/issue189-final-evidence/server-internal-errors.log`, `schedule-state.json` |

The shared preview failed twice with `net::ERR_CERT_AUTHORITY_INVALID`.
The primary used a task-owned Chromium container with scoped certificate trust.
The primary inspected the running application screenshots.
The primary closed its Chromium container after capture.

## Final checks

| Check | Result | Raw output |
| --- | --- | --- |
| Complete Go package coverage | Every package has a passing result after recorded retries | `/tmp/issue189-go-final.log`, `/tmp/issue189-go-serial.log`, `/tmp/issue189-migrate-final-retry.log` |
| Final repository suite | Repository package passed in 330.906 seconds | `/tmp/issue189-go-final-relevant.log` |
| Final handler and API suites | Exit 0, 115.706 and 53.359 seconds | `/tmp/issue189-final-handlers.log` |
| Complete running-server E2E on the final build | Exit 0 | `/tmp/issue189-running-final-build.log` |
| Final admission race HTTP E2E | Exit 0 | `/tmp/issue189-admission-network-e2e.log` |
| Latest-build thread stack API and browser regressions | Exit 0 | `/tmp/issue189-thread-final-suite.log` |
| Latest-build retained API and browser regressions | Exit 0 | `/tmp/issue189-final-live.log` |
| Lint and golines | Exit 0 | `/tmp/issue189-release-lint.log` |
| Vet | Exit 0 | `/tmp/issue189-release-vet.log` |
| Changed shell script checks | Exit 0 | `/tmp/issue189-shellcheck-db.log`, `/tmp/issue189-shellcheck-features.log`, `/tmp/issue189-shellcheck-shards.log` |
| Dockerfile lint | Exit 0 | `/tmp/issue189-hadolint.log` |

The first full Go run encountered SQL Server startup failures under concurrent test load.
Crash logs reported inotify exhaustion and exit code 137.
Serial retries resolved those failures without host configuration changes.
The final three migration retries passed in 51.234 seconds.
The updated release admission contract required changes to existing race test expectations.
The combined repository and handler command exited 1 because handler expectations still required 404.
The repository package passed in that command.
Repository source and tests did not change after that passing result.
The separate full handler and API rerun passed after those expectation updates.

Use `/tmp/issue189-package-results.txt` for the complete per-package result index.
The index references the original raw log lines.
Mixed command exits remain failures in those original logs.

| Resolved startup failure | Passing package result |
| --- | --- |
| Agent server | `/tmp/issue189-go-serial.log:11`, 325.300 seconds |
| Dispatch | `/tmp/issue189-go-serial.log:12`, 66.779 seconds |
| SQL Server driver | `/tmp/issue189-go-serial.log:5615`, 15.229 seconds |
| Migration cases | `/tmp/issue189-migrate-final-retry.log:11`, 51.234 seconds |

The first running-server regression exposed missing retained-environment accounting.
The harness now records the exact created environment IDs.
The complete suite passed after this change.
The complete suite also passed against the latest production build.

## Retained examples

Final retained stack: `/home/user/.local/state/durpdeploy/issue189-final-retained-20261109`.
URL: `https://durpdeploy-dev-d9dc9da69d9edbe4df585552.localhost:33205`.
Use its `connection.json` file for the scoped CA path and resource names.
The stack remains outside automatic thread teardown.
Use `make dev-stack ACTION=down STATE=/home/user/.local/state/durpdeploy/issue189-final-retained-20261109` to stop this stack.

Use the printed foreign key lifecycle and project links in the complete suite log.
Follow `docs/manual-e2e.md` for repeat steps.
The foreign key regressions create no schedules.
Both retained schedule tables contain zero enabled schedules.

The separate thread stack remains registered in `.local/dev-stack.json`.
The T3 project already contains both scripts from `t3.json`.
Keep this thread unsettled during manual review.

The historical intermittent insert failure has no captured database snapshot.
The patch addresses reproducible missing-parent paths and concurrent deletion.
It does not claim to prove the historical database state.

## CI follow-up

Pipeline 2593 found an outdated rollout command inventory after the new admission E2E selector was added.
The inventory now includes `ForeignKeyAdmissionRace` with the existing command flags and runtime.
The complete `internal/cicontract` package passed in 3.115 seconds after this update.
Package lint, vet, JSONL parsing, and whitespace checks passed.
Raw output: `/tmp/issue189-ci-contract-fix.log`, `/tmp/issue189-ci-contract-lint.log`, and `/tmp/issue189-ci-contract-vet.log`.

The navigation browser job failed while pulling Ryuk because Harbor refused a connection.
The next test in that job successfully pulled and started Ryuk.
The new pipeline repeats browser verification without changing runtime or cleanup policy.
Pipeline 2596 passed all 42 jobs for commit `7393f1b8`.
SonarCloud reported an OK quality gate, 85.2 percent new coverage, and zero new duplication.

## SonarCloud follow-up

SonarCloud identified three new cognitive complexity findings despite the passing quality gate.
The follow-up extracts deployment selection, creation error handling, and Ansible check-mode validation.
The database parity test now uses a named test helper.
These extractions preserve validation order, response codes, transaction boundaries, and test assertions.
The implementation risk remains 5/10.

The remaining `UpdateVariable` complexity finding predates this issue.
Its method body is unchanged from target commit `fe12e8e8`.
That existing finding remains outside this change.

| Final source check | Result | Raw output |
| --- | --- | --- |
| Focused deployment, foreign key, and Ansible regressions | Exit 0 | `/tmp/issue189-sonar-focused.log` |
| Complete repository suite | Passed, 351.239 seconds | `/tmp/issue189-sonar-final-go.log` |
| Complete API suite | Passed, 54.739 seconds | `/tmp/issue189-sonar-final-go.log` |
| Complete handler retry | Exit 0, 94.603 seconds | `/tmp/issue189-sonar-handler-retry.log` |
| Lint and golines | Exit 0 | `/tmp/issue189-sonar-lint.log` |
| Vet | Exit 0 | `/tmp/issue189-sonar-vet.log` |
| Admission race HTTP E2E | Exit 0 | `/tmp/issue189-sonar-admission-e2e.log` |
| Refreshed thread stack API and browser regressions | Exit 0 | `/tmp/issue189-sonar-thread-suite.log` |
| Current retained stack API and browser regressions | Exit 0 | `/tmp/issue189-sonar-live.log` |
| Complete running-server E2E | Exit 0 | `/tmp/issue189-sonar-running-final.log` |

The combined final Go command exited 1 after a SQL Server startup failure in the handler package.
The crash reported inotify exhaustion and exit code 137.
The complete handler package passed when retried alone.
The repository and API package results in the failed command passed.

An extra live regression overlapped the first complete running-server suite and changed its environment baseline.
All functional scenarios passed, but the final leftover fixture check failed.
The primary reran the complete suite alone without application changes.
The standalone complete suite passed its final fixture checks.

Current manual artifacts use `.local/issue189-sonar-evidence/`.
The primary inspected all three updated screenshots from the current build.
The current retained stack has zero enabled schedules and zero internal request error entries.
Use `schedule-state.json` and `server-internal-errors.log` in that artifact directory for the final audit.

Current retained stack: `/home/user/.local/state/durpdeploy/issue189-sonar-retained-20261109`.
URL: `https://durpdeploy-dev-e8ab410402847d7592511fd6.localhost:43761`.
Use its `connection.json` file for the scoped CA path and resource names.
Use `make dev-stack ACTION=down STATE=/home/user/.local/state/durpdeploy/issue189-sonar-retained-20261109` to stop this stack.
Earlier retained stacks remain available for their recorded examples.
