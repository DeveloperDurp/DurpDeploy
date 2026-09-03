# Agent protocol

The canonical protocol specification is maintained with the standalone agent:
[`docs/agent-protocol.md`](https://github.com/DeveloperDurp/durpdeploy-agent/blob/main/docs/agent-protocol.md).

DurpDeploy pins the agent Go module that defines the shared wire types,
transport verification, encrypted payload envelope, and execution behavior.
Breaking wire changes require a new protocol identifier; `agent-v1` remains
backward compatible.
