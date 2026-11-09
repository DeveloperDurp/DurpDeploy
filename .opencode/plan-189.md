# Issue 189 plan and risk review

Issue: https://gitlab.durp.info/durfy/apps/durpdeploy/-/work_items/189

Target: `main`, `fe12e8e827e41574f3b28dabce667df5c1a3d431`.
Source: `bugfix/foreign-key-errors-189`.

## Approved scope

Validate environment references before deletion.
Keep existing deletion constraints and active deployment safeguards.
Serialize environment deletion with deployment admission.
Return a conflict when a reference appears during deletion.
Check environment lock results during deployment admission and reruns.
Validate variable environment existence for projects without lifecycles.
Map supported database foreign key errors at the affected write operations.
Keep infrastructure errors separate from missing parent errors.
Normalize JSON error envelopes before structured server logging.
Cover the API and browser behavior with regression tests.
Update public API documentation and the shipped agent skill.

Migration 032 removed the obsolete environment agent assignment table.
Current agent environment labels already cascade when an environment is deleted.
Count lifecycle stage blockers and test existing label cleanup.
Preserve queue slot cleanup through deployment history deletion.

## Risk and authorization

Original issue risk: 4/10.
Approved implementation risk: 5/10.
Shared admission and deletion transactions can affect deployment history.
The user explicitly approved the plan and accepted 5/10 risk on 2026-11-09.
This approval satisfies the human review requirement for potential data loss.
The approval includes commits, push, and issue-linked MR creation.
Merge authorization remains pending.

## Verification and delivery

Generate sqlc, templ, Swagger UI assets, and the API specification before checks.
Run focused regressions during implementation.
Run final Go, API, browser, database, lint, formatting, and vet checks.
Use the thread stack and a separate retained stack for manual checks.
Keep schedules disabled after testing.
Capture browser screenshots and upload them to the MR.
Run correctness, security, and QA review before the first push.
Check CI, SonarCloud, approvals, and discussions for the final source SHA.

## Recovery and exclusions

Revert the code commit to recover the previous behavior.
No migration or data backfill is required.
Do not remove constraints or add cascades.
Do not change authentication, permissions, or audit policy.
Exclude package repository 502 behavior and issues 190 and 196.
The historical intermittent insert failure has no captured database state.
Test reproducible missing-parent and concurrent deletion paths without claiming historical certainty.
