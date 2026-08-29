# Agent protocol

`agent/1` is the outbound-only JSON contract between DurpDeploy and a remote
agent. This document freezes the wire vocabulary only. It adds no listener,
database state, runner behavior, or fallback path.

## Endpoints and payloads

All JSON requests are exactly one object and require a present, non-null
`"protocol":"agent/1"`. They reject unknown fields, trailing JSON values,
malformed JSON, and every other protocol value.

| Endpoint | Request contract | Notes |
| --- | --- | --- |
| `POST /agent/v1/poll` | `PollRequest` | Protocol and agent version. A no-work response has no deployment payload. |
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

## Direct assignment

An administrator explicitly assigns each remote environment to one paired
agent. Environments without an assignment run locally. An assigned
environment creates work only for its paired agent. Agents do not select work
or authorize themselves through labels.

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
