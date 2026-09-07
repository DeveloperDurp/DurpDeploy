# DurpDeploy agent

The execution agent is developed and released from the standalone
[`DeveloperDurp/durpdeploy-agent`](https://github.com/DeveloperDurp/durpdeploy-agent)
repository.

Use its [operator runbook](https://github.com/DeveloperDurp/durpdeploy-agent/blob/main/docs/agents.md)
for installation, pairing, upgrades, rollback, state backup, and troubleshooting.
The DurpDeploy control plane retains agent registration, environment assignment,
dispatch, revocation, and audit history.

The optional `agent` service in this repository's Compose files pulls the pinned
standalone image. Override `DURPDEPLOY_AGENT_VERSION` to test another released
agent version.

## Label routing

An administrator can put active, paired agents in an agent label. A label is a
named fixed member list. It is not a selector expression. A disabled, revoked,
or unpaired agent is not eligible. The server does not use heartbeat age to
remove an otherwise eligible agent from label routing.

Set a project execution policy to `local`, or set it to a label with one
strategy. `round_robin` selects one eligible member and advances the label
cursor in the same transaction. `all` creates one root deployment and one
child deployment for every eligible member. The server orders children by the
saved routing order.

Use a local override for one manual deployment. Use a label override only when
the request gives both `agent_label_id` and `agent_strategy`. A request with no
override uses the current project policy.

If a label has no eligible members, a normal deployment fails. It does not run
on the local runner. An approval-gated deployment stays pending until approval.
Approval resolves its saved intent. Approval returns a conflict when the saved
label members are no longer eligible. It does not change the pending root.

## Routing history and operations

DurpDeploy saves the resolved routing policy with each deployment. A saved
label name and ordered agent set remain useful after a label or agent changes.
An `all` root shows aggregate status and ordered child summaries. Standard
deployment lists, counts, and lifecycle gates show roots only. Open each child
for its logs. A fan-out root has no combined log stream or text export.

Retry preserves the exact saved agent IDs and order. Retry does not use a
later label membership change. A retry fails before creation when a required
agent is unavailable. For a pending approval retry, approval checks the saved
agents and keeps the pending root unchanged on failure.

Redeploy creates a new deployment and resolves the current project policy.
Use redeploy when a changed policy or current label membership is required.
You cannot retry or redeploy a child deployment.

## Scheduled deployments

A schedule uses `inherit` by default. Each fire resolves the project's current
policy. Set a schedule to `local` or a label to save an explicit target.
Concurrent schedulers create one occurrence for each due time. A non-approval
schedule with no eligible label members creates one failed root. An
approval-gated schedule can remain pending until eligible members exist.

The routing integration harness uses real agent processes and the remote
protocol. It does not prove host operating-system sandbox enforcement. Keep
the existing sandbox tests and host security checks in the release procedure.

If a remote agent does not acknowledge cancellation before the server deadline,
DurpDeploy records `cancel_unconfirmed`. The fan-out root then settles failed.
Use the child logs to investigate the remote process before you redeploy.

## Legacy environments

Projects without an execution policy keep the existing environment assignment.
An assigned remote agent remains the legacy target. An unassigned environment
uses the local runner. Save a project policy to migrate from this legacy
behavior. This save does not change past deployments.
