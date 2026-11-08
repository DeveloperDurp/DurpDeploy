# Validation evidence

The following final checks passed with exit code 0:

- `make install-hooks templ-generate swagger-ui-copy`
- `bash scripts/go_test.sh -count=1 ./internal/cicontract ./scripts/transportcheck`
- `make lint`
- `go vet ./...`
- `make golines-check`
- `git diff --check`
- `bash scripts/check-ci-contract.sh`
- `bash scripts/check-agent-ci-contract_test.sh`
- `bash scripts/check-mobile-browser-container-contract.sh`
- `python3 scripts/ci_go_cache_test.py`
- `node --test scripts/ci_release.test.mjs scripts/ci_cache_cleanup.test.mjs`

Raw local output is in the task-owned .opencode/sonar-main directory.
The first test run found a test compile error, which the final revision fixes.
No application code changed.
The full application, API, and browser suites remain required in the MR pipeline.

The GitLab project CI Lint API accepted the combined local main configuration without errors or warnings.
Main includes the cache producer, lint, runtime, six coverage jobs, and SonarCloud.
Simulations override event variables in submitted YAML and inline local configuration.
Security-only and cleanup simulations inline only their permitted includes.
Local event tests separately evaluate actual include rules and job dependencies.

MR and fork MR simulations passed with their existing analysis restrictions.
Feature push simulation returned the expected workflow rejection.
Tag, manual, schedule, security-only, and cleanup simulations passed without SonarCloud.
Responses are stored in .opencode/sonar-main/lint-*.json.

Independent correctness review returned MERGE with high confidence and no blockers.
The reviewer independently reran the CI contract package and checked Bash support in the pinned scanner image.
Review pass 1 used agent /root/review_sonar_main.
The review permits initial push and does not approve bypassing pending merge gates.

Supplemental publication verification passed its image and private registry cases.
The host invocation exited 1 before chart checks because Helm 3 is unavailable.
The disposable CI helper attempt exited 126 because it could not read the mounted checkout.
The supplemental publication suite is incomplete and is not recorded as PASS.
No publication implementation or rules changed.
The MR pipeline retains its existing full publication regression checks.

Exact-head pipeline, SonarCloud, and merge checks remain pending.
The stale main baseline can block this MR before the first main analysis.
No gate, finding classification, or exclusion was changed.

## Pipeline 2552 and integration update

Pipeline 2552 passed all 42 jobs on 01210751125f3ff09d0830f235a2cd9eb6e37fba.
The SonarCloud quality gate returned OK.
The pre-commit job passed the complete publication fixture, including Helm package identity.
This CI result supplies the publication coverage unavailable on the local host.

The SonarCloud API reported complexity 18 in the changed MR contract function.
The update extracts its existing per-job checks into a helper without changing assertions.
The other reported complexity finding concerns the unchanged pipeline loader.
No finding was suppressed or classified through a token.

Main advanced to 5aa3b4274b957222210fb11f0939c821793a88b0.
The source branch merges that target because the project requires fast-forward merges.
The target update changes token authorization and does not modify this MR's CI files.
The CI contract suites, lint, vet, formatting, and CI shell contract passed again after integration.
Raw results use the update log files in the task evidence directory.
Fresh review and a new exact-head pipeline remain required before merge.
Risk remains 4/10, with the approved scope unchanged.

Fresh correctness review pass 2 returned MERGE with high confidence and no blockers.
The reviewer used agent /root/review_sonar_main_pass2.
The reviewed diff has SHA256 6c3d9b1c73f1a47977584514176541497845f0bd46a695a5adb936623e295e41.
The new-head pipeline, SonarCloud result, and final merge checks remain pending.
