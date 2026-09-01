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
