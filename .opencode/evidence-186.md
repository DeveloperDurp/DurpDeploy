# Issue 186 verification

Use `.opencode/plan-186.md` for the approved scope and human risk review.
The implementation risk remains 4/10.
Revert the change to restore the previous mint validation.
No database recovery is required.

## Manual QA matrix

| Scenario | Action | Expected and observed | Artifact |
| --- | --- | --- | --- |
| Excessive global API ceiling | POST global Admin as a Maintainer | HTTP 422 with the ceiling error | `evidence/186/live-api-rejection.log` |
| Excessive global web ceiling | Submit Admin through the token form | HTTP 422, visible error, no new token | `evidence/186/live.log`, `evidence/186/screenshots/token-mint-rejected-1280.png` |
| Phone layout | Repeat rejection at 375 pixels | Visible error and controls without horizontal overflow | `evidence/186/screenshots/token-mint-rejected-375.png` |
| Equal and lower ceilings | Mint Maintainer, Deployer, and Viewer through API and web | HTTP 201 and 303 | `evidence/186/live.log` |
| Browser correction | Change rejected Admin ceiling to Maintainer | Successful creation and retained metadata | `evidence/186/screenshots/token-mint-allowed.png` |
| Delegated member management | Mint scoped Admin for a project Admin membership | HTTP 201 | `evidence/186/live.log` |
| Delegated token use | Add a project member, then request instance administration | HTTP 201, then HTTP 403 | `evidence/186/mint-regression.log` |
| Existing scope edit | Edit a global Viewer token to Admin, then request instance administration | HTTP 200, then HTTP 403 | `evidence/186/mint-regression.log` |
| Mixed project memberships | Add a project with Viewer membership | HTTP 422 | `evidence/186/live.log` |
| Rejected-record absence | Compare token metadata and flash counts | No token or flash row added | `evidence/186/token-e2e.log` |
| CLI rejection | Mint global Admin for the retained Maintainer | Exit 1 with the ceiling error | `evidence/186/live-cli.log` |

The shared preview reported `available: false`.
The repository's isolated Chromium harness supplied browser actions and screenshots.
The primary agent inspected all three screenshots.
Screenshots contain no plaintext bearer credentials.

## Retained examples

Stack registration: `.local/dev-stack.json`.
The connection record contains the HTTPS URL and scoped CA path.
The T3 project already contains both scripts from `t3.json`.
Keep this thread unsettled for manual review.

Owner: `e2e-mint-186-1794139409@test.local`.
Project Admin example: `/projects/3`.
Project Viewer example: `/projects/4`.
Token metadata: `/settings/tokens`.
Reset the owner's password through administrator settings before manual sign-in.
Follow `docs/manual-e2e.md` for repeat steps.
The token scenario creates no schedules.

## Final checks

Raw command output stays in `evidence/186/`.
The full running-server suite passed on retry.
The full Go command failed because one SQL Server fixture exceeded its startup timeout.
All other packages passed.
The complete migration package retry passed with exit code 0.
Every Go package now has a passing result.
The broader token and role API/web E2E suite passed.
The backend matrix covers SQLite, PostgreSQL, and SQL Server.
Lint, vet, formatting, Swagger generation, and Dockerfile lint checks passed.

The first running-server attempt exceeded the cancel scenario's queue wait.
The deployment later completed.
The retry uses four CPUs and 1 GiB for this thread's app container.
The retry passed that cancellation scenario.
The final database query found zero enabled deployment schedules.

| Check | Command | Exit code |
| --- | --- | --- |
| All Go packages | `bash scripts/go_test.sh -count=1 -timeout=20m ./...` | 1, SQL Server fixture startup timeout |
| Complete migration retry | `bash scripts/go_test.sh -count=1 -timeout=20m ./internal/migrate` | 0 |
| Token and role API/web E2E | `bash scripts/go_test.sh -tags=e2e -count=1 -timeout=20m -run 'Test.*Token.*\|Test.*Role.*' ./internal/handler/api ./internal/handler` | 0 |
| Final mint regression | `bash scripts/go_test.sh -tags=e2e -count=1 -run '^TestTokenMintCeilingAPIWebE2E$' ./internal/handler/api` | 0 |
| Backend matrix | `bash scripts/go_test.sh -v -count=1 -run TestTokenMintBackendParity ./internal/repository` | 0 |
| CLI regression | `bash scripts/go_test.sh -count=1 -run TestRunTokensCreateRoleCeiling ./cmd/server` | 0 |
| Running server and browsers | `make e2e-test`, through `.local/run_suite.sh` | 0 on retry |
| Lint | `make lint` | 0 |
| Vet | `go vet ./...` | 0 |
| Formatting | `make golines-check`, `gofmt -l` | 0, no files listed |
| Swagger | `make swagger-spec` | 0 |
| Dockerfile lint | `hadolint --failure-threshold warning Dockerfile`, through its container | 0 |

The running-server wrapper uses the registered connection record and CA.
It runs CLI commands as the app user inside the owned container.
It runs SQLite queries through the owned volume and rootless user namespace.
The T3 project already has the teardown hook from `t3.json`.
