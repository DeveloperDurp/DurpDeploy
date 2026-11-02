# Bearer script warnings

Send caller text to `POST /api/v1/lint` with a valid bearer token.
Use `application/json` with no charset or the UTF-8 charset.

```json
{"profile":"builtin-shell-warnings-v1","script":"#!/bin/sh\necho \"$name\""}
```

Include only the required `profile` and `script` string fields.
Do not send a project ID, path, URL selector, or query parameter.
The server rejects missing, duplicate, unknown, null, and non-string fields.
It rejects trailing JSON, invalid UTF-8, lone Unicode surrogates, and NUL.
It does not trim or normalize script text.

Every valid effective role can analyze its own text.
Global and scoped PATs can use this route without selecting a project.
Normal owner, grant, expiry, revocation, and role-ceiling checks still apply.
Cookie-only requests fail with JSON `401` and no login redirect.
This operation grants no stored project or script access.
Other writes retain their existing permission checks.

## Warning profile

The fixed profile uses the existing browser checker's regex rules.
A first-line `#!` prefix satisfies its shebang rule.
Without that prefix, code `1000` appears first on line 1.
Each match of `\$[A-Za-z_][A-Za-z0-9_]*` produces code `2086`.
Warnings use one-based LF lines and left-to-right match order.
CRLF keeps its CR character, and a bare CR does not start another line.

The checker examines comments, quotes, and escaped references without parsing shell syntax.
Quoted variables can produce warnings.
Invalid shell syntax can produce no warnings.
An empty diagnostic list does not establish valid syntax or safe execution.
The profile has no external linter or interpreter dependency.

Bearer messages contain no matched identifier or source excerpt.
The browser route keeps its existing messages and behavior.

```json
{"profile":"builtin-shell-warnings-v1","diagnostics":[{"line":2,"level":"warning","code":2086,"message":"Variable reference matched the built-in warning rule"}],"truncated":false}
```

## Limits and completion

| Boundary | Maximum |
| --- | --- |
| Encoded request body, including escaping and whitespace | 524,288 bytes |
| Decoded script | 65,536 UTF-8 bytes |
| Returned complete diagnostics | 100 |
| Fixed diagnostic message | 128 UTF-8 bytes |
| Encoded success response | 32,768 bytes |
| Analysis after decoding | 2 seconds |
| Concurrent requests per router | 4 |

A smaller configured JSON request ceiling still applies.
Known-length and chunked requests have the same ceiling.
The configured listener deadlines bound reading and response writing.
Default read and write deadlines are 30 and 60 seconds.
Admission fails immediately when all four slots are occupied.
The server closes a busy HTTP/1 connection without consuming its unfinished body.
A reverse proxy can independently delay that response while reading the upload.
Configure the proxy for early responses before testing this HTTP/1 wire behavior.
For Caddy, [`enable_full_duplex`](https://caddyserver.com/docs/caddyfile/options#enable-full-duplex)
permits concurrent HTTP/1 request reads and responses.
This Caddy option is experimental and requires compatible clients.
HTTP/2 and later protocols already permit concurrent reads and responses.
The application cannot enforce an external proxy's body-reading policy.
A slot remains held through reading, analysis, and response delivery.
The server creates no analysis queue or detached worker.
It releases the slot on every return path.

Exactly 100 findings return `truncated: false`.
More findings return the first 100 and `truncated: true`.
The server detects one additional finding without storing an unbounded list.
It serializes the complete bounded response before writing success.
Discard incomplete, interrupted, or partially delivered responses.

| Condition | Status |
| --- | --- |
| Complete warning analysis | `200` |
| Invalid request, profile, or text | `400` |
| Invalid or absent bearer credential | `401` |
| Unsupported effective role | `403` |
| Request cancellation or read timeout, when writable | `408` |
| Encoded, decoded, or configured ceiling exceeded | `413` |
| Unsupported media type or charset | `415` |
| Internal response failure | `500` |
| Admission capacity exhausted | `503` |
| Analysis deadline exceeded | `504` |

The normal generic error scrubber applies to `5xx` responses.
A disconnected caller cannot receive a promised response.
Older servers can reject this POST with `403` before reaching a router `404`.
Preserve `403` as an authorization failure.
Classify only an actual `404` as an unsupported API.

## Privacy and execution boundary

Application-controlled responses use `Cache-Control: no-store`.
The analyzer uses strings, regex, context checks, and JSON.
It does not expand source, execute processes, read files, or make callbacks.
It does not fetch resources or create execution jobs.
It retains no source or results in application storage, logs, or audit bodies.
Normal token last-use telemetry remains enabled.
External proxy logging and string zeroization are outside this guarantee.
Protect real bodies in clients and external logging systems.
Use only synthetic scripts in retained evidence.
The current CLI compatibility pin remains a separate delivery boundary.
