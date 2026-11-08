# Keep the SonarCloud main baseline current

The user approved this plan at risk 4/10 on November 8, 2026.
The user then authorized full send for this task only.
The target is main at acf3f8ed8823417915e1635c71c3286a58ea03f4.
The source is improvement/sonarcloud-main-analysis.

Enable coverage, its cache producer, and SonarCloud for normal default-branch pushes in durfy/apps/durpdeploy.
Keep same-project MR analysis, fork restrictions, existing dependencies, and the quality gate.
Use branch parameters for default-branch pushes and PR parameters for MRs.
Separate Go cache keys by branch and protection status, while retaining project and MR isolation.
Keep other MR tests MR-only.
Keep security-only, manual, scheduled, tag, cleanup, and publication rules unchanged.
Add event, dependency, cache, argument, missing-report, and scanner-failure contract tests.
Update AGENTS.md and docs/gitlab-ci.md.

The eight added main jobs used about 86 runner-minutes in pipeline 2532.
Runtime and lint already run on main.
No application, API, agent, dependency, HSTS, or infrastructure changes are included.
MR 152 remains separate and unchanged.

Risk is 4/10 because incorrect rules can omit dependencies or mix analysis contexts.
No credential permissions or scopes change.
Mandatory human risk review does not apply.
Revert through a separate MR if recovery is required.
Stored SonarCloud analysis remains after a revert.

Run CI contracts, cache tests, lint, vet, formatting, and GitLab CI lint before review and push.
Inspect exact-head CI, SonarCloud, approvals, discussions, and mergeability before merging.
Do not bypass a failed gate to bootstrap the baseline.
After merge, verify the main analysis commit before reassessing MR 152.
