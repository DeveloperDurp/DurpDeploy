# Agent protocol

`agent/1` and `agent/2` are the outbound-only JSON contracts between DurpDeploy
and a remote agent. Version 2 adds fixed interpreter capability reporting while
the server continues to accept version 1 as Bash-only.

## Endpoints and payloads

All JSON requests are exactly one object and require a present, non-null
`protocol` of `agent/1` or `agent/2`. They reject unknown fields, trailing JSON
values, malformed JSON, and every other protocol value. Pairing remains
`agent/1` so existing identities can upgrade without re-pairing.

| Endpoint | Request contract | Notes |
| --- | --- | --- |
| `POST /agent/v1/pairings/server-init` | `PairRequest` | Server-side pairing completion over mTLS. The first call uses `completion_ack: false`; after durable confirmation the same request is retried with `completion_ack: true` to perform listener cleanup. |
| `POST /agent/v1/poll` | `PollRequest` | Version 1 sends protocol and agent version and is recorded as Bash-only. Version 2 also requires `supported_interpreters`, containing only `bash`, `pwsh`, or `python3`. A no-work response has no deployment payload. |
| `POST /agent/v1/deployments/{id}/start` | `StartRequest` | Acknowledges that the claimed work started. |
| `POST /agent/v1/deployments/{id}/heartbeat` | `HeartbeatRequest` | Response carries cancellation state and staged server fingerprints. |
| `POST /agent/v1/deployments/{id}/logs` | `LogBatchRequest` | Ordered line events. |
| `POST /agent/v1/deployments/{id}/result` | `ResultRequest` | Result state is only `succeeded` or `failed`. |
| `POST /agent/v1/deployments/{id}/cancelled` | `CancelledRequest` | Cancellation acknowledgement is distinct from a normal result. |

The endpoint route, active mTLS identity, and later persistence checks bind a
claim to one agent. This contract deliberately does not document credential
material, certificate bodies, or secret values.

## Bounds and timing

| Contract | Fixed value |
| --- | --- |
| Any request body | 1 MiB |
| Log batch | 100 events and 256 KiB |
| Log line | 16 KiB UTF-8 bytes |
| Poll interval / maximum long poll | 25 seconds |
| Heartbeat interval | 10 seconds |
| Pre-start claim expiry | 60 seconds |
| Started-work lost threshold | 45 seconds without heartbeat |
| Cancellation acknowledgement deadline | 30 seconds |

These are protocol constants, not configuration knobs. Oversize requests and
batches fail before later agent or persistence work consumes them.

## Bootstrap and pairing flow

Pairing uses two channels:

1. The unpaired local listener prints a short-lived pairing code and its
   certificate fingerprint.
2. The operator enters the agent address, display name, and pairing code in
   the authenticated form. The server discovers the certificate fingerprint,
   and the operator approves it on a separate confirmation page after comparing
   it with the fingerprint printed by the agent.
3. The server submits `POST /agent/v1/pairings/server-init` over mTLS with
   `completion_ack: false`.
4. The callback validates the confirmed identity and persists the server pin
   and endpoint only after validation.
5. The same request is retried with `completion_ack: true` when needed to
   recover a lost `204 No Content` response. This acknowledgement closes the
   temporary callback listener. It is idempotent.

Pairing code disclosure is local-only. Codes and fingerprints are never API
responses.

## Agent labels

Administrators can attach capability labels through the browser or API and
environment labels through the agent details page. For each remote step, the
server selects active, paired agents that have the deployment's environment
label, every capability label required by the step, and the interpreter stored
in the immutable deployment-step snapshot. Label matching is case-insensitive.
A step with no capability labels matches every active, paired agent carrying
the environment label and reporting the required interpreter.

Each poll transactionally replaces the agent's interpreter capabilities. The
claim transaction rechecks the capability, so a capability removed after queue
creation cannot claim incompatible work. The server creates one run per
matching agent, so every match receives the step. The deployment continues only
after all runs succeed. If nothing matches, the step fails without falling back
to local execution. Labels select work; they do not grant authorization or
create a security boundary.

Encrypted step payloads omit the interpreter for Bash, preserving strict
`agent/1` decoding. PowerShell and Python payloads include `pwsh` or `python3`
and are sent only to compatible `agent/2` agents.

## Dispatch state machine

| From | Allowed next state | Meaning |
| --- | --- | --- |
| `waiting` | `claimed` | A matching agent owns the pre-start claim. |
| `claimed` | `waiting` | Only the 60-second pre-start expiry can reclaim it. |
| `claimed` | `started`, `cancel_requested` | The agent starts, or cancellation overlays the claim. |
| `started` | `succeeded`, `failed`, `cancelled`, `lost`, `cancel_requested` | Started work reaches a terminal state, becomes lost after missed heartbeats, or receives cancellation. |
| `cancel_requested` | `cancelled`, `cancel_unconfirmed`, `lost` | The agent acknowledges cancellation, misses the 30-second acknowledgement deadline, or is lost. |

All other edges, including `started` to `waiting`, are invalid. A pre-start
claim may be reclaimed, but started work is never automatically replayed or
requeued and does not fall back to local execution. Recovery is an explicit new
deployment.

## Transport trust

Agents initiate the connection. Both sides use self-signed X.509 identities
and pin the peer's SHA-256 certificate fingerprint directly. This has no
central CA, no trust-on-first-use mode, and no certificate-verification bypass.
Fingerprint rotation is staged over an already authenticated connection.
