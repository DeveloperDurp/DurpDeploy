# Issue 186 approved implementation plan

Issue: https://gitlab.durp.info/durfy/apps/durpdeploy/-/work_items/186

Target: main at 385a050e286eeb92de0fb5bf5a5c2cd56fbd8622.
Source: bugfix/token-role-ceiling-186.

Add shared mint-only validation for API, web, and server CLI creation.
Limit global ceilings to the owner's global role.
Check scoped ceilings against every selected project.
Preserve delegated scoped Admin mints for global Maintainers with project Admin membership.
Preserve global administrator membership bypass.
Preserve valid lower and equal ceilings and legacy name-only creation.
Return HTTP 422 before storing rejected tokens or flash secrets.

Keep existing records, token-list rendering, scope edits, and downstream authorization unchanged.
Document existing-token behavior without backfills or flags.
Update role, security, API, shipped skill, and manual-check documentation.
Add Go, API, web, browser, and database parity coverage.

Run required assets, formatting, lint, vet, focused checks, and final applicable suites.
Retain running-server examples and screenshots without credentials.
Complete correctness, security, and QA review before push.
Create an issue-linked MR with the delivery checklist and exact-head CI evidence.
Inspect SonarCloud, approvals, and blocking discussions.
Leave merging to the user.

## Human risk review

Initial issue score: 3/10.
Reviewed implementation score: 4/10.
Project delegation adds compatibility cases across creation paths.
The likely failure is rejecting a legitimate mint.
No migration or stored-token rewrite occurs.
Recover with `git revert`.

The user explicitly selected delegated-admin preservation and documentation-only existing-token handling.
The user then approved this plan through MR creation:

> I reviewed and accept risk 4/10 and approve this plan through MR creation

This approval covers edits, commits, push, and MR creation.
It does not authorize merging.
