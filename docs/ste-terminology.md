# Simplified Technical English terminology

This glossary applies to the files in the `docs` directory. It uses
ASD-STE100 Issue 9. Use the terms in this glossary with the meanings that this
document gives.

Code, commands, paths, environment variables, API routes, protocol values,
database identifiers, and text from the user interface are technical nouns.
Do not change these items to make their spelling agree with normal text.

## Technical nouns

| Term | Approved meaning |
| --- | --- |
| agent | A DurpDeploy process that does deployment work on a remote host. |
| API | The DurpDeploy HTTP interface that uses JSON. |
| API token | A bearer credential for the API. |
| Argon2id | The algorithm that makes a password hash. |
| audit log | The database record of successful changes. |
| Authentik | The identity provider in the OIDC examples. |
| Caddy | The reverse proxy in the supported installation. |
| callback | The OIDC HTTP request that returns to DurpDeploy. |
| certificate fingerprint | The SHA-256 identification of a certificate. |
| claim | A temporary assignment of one deployment to one agent. |
| claim token | A temporary credential for a claim. |
| CLI | A command-line interface. |
| container | An isolated operating-system process environment. |
| Compose | The Docker Compose or Podman Compose configuration. |
| CSRF | Cross-site request forgery. |
| database | The DurpDeploy SQLite, PostgreSQL, or SQL Server data store. |
| deployment | One operation that runs the steps in a release. |
| deployment gate | A rule that controls movement through lifecycle stages. |
| deployment log | Text output from a deployment step. |
| DurpDeploy | The application that this repository contains. |
| environment | A DurpDeploy deployment target. |
| environment file | A file that contains process environment variables. |
| heartbeat | A periodic message that shows that an agent operates. |
| HTMX | The browser library that sends partial-page HTTP requests. |
| identity provider | An OIDC service that authenticates a user. |
| lifecycle | An ordered set of deployment environments. |
| Litestream | The SQLite replication program in the backup procedure. |
| long poll | An HTTP poll that the server keeps open for a specified time. |
| MFA | Multi-factor authentication. |
| migration | A versioned change to the database schema. |
| mTLS | Mutual TLS authentication. |
| OIDC | OpenID Connect. |
| pairing | The operation that establishes trust between a server and an agent. |
| pairing code | A temporary credential for agent pairing. |
| passkey | A WebAuthn credential. |
| payload | The encrypted deployment data that the server sends to an agent. |
| project | A DurpDeploy object that contains steps, variables, and releases. |
| recovery code | A one-time MFA credential. |
| release | An immutable snapshot of project steps and variables. |
| reverse proxy | The HTTP service between a client and DurpDeploy. |
| role | The authorization level of a user or project member. |
| runner | The DurpDeploy component that runs deployment steps. |
| sandbox | The operating-system limits for a deployment step. |
| session | A browser authentication record. |
| SQLite | The default DurpDeploy database engine. |
| SSE | Server-sent events. |
| step | A Bash script in a release. |
| TOTP | A time-based one-time password. |
| trust pin | An approved certificate fingerprint. |
| user interface | The DurpDeploy browser interface. |
| variable | A named value that a deployment step receives. |
| WebAuthn | The browser protocol for passkeys. |
| webhook | An HTTP endpoint that receives a notification. |

Plural forms of these nouns are permitted when the meaning does not change.
Common software product names and official protocol names are also technical
nouns.

## Technical verbs

| Term | Approved meaning |
| --- | --- |
| authenticate | To confirm the identity of a user, agent, or server. |
| authorize | To give permission for an operation. |
| back up | To make a copy that can restore data. |
| deploy | To run a release in an environment. |
| dispatch | To assign a deployment to a local runner or remote agent. |
| encrypt | To change plaintext to ciphertext with a key. |
| hash | To calculate a one-way value from data. |
| log in | To start an authenticated browser session. |
| pair | To establish trust between a server and an agent. |
| poll | To ask the server for work. |
| re-pair | To pair an agent identity again. |
| redeploy | To make a new deployment from a completed deployment. |
| replicate | To copy database changes to backup storage. |
| restore | To replace data with data from a backup. |
| revoke | To make a credential or identity invalid. |
| rotate | To replace a key, credential, or trust pin. |
| scrub | To replace secret text in a log. |
| stream | To send data as it becomes available. |

Use these verbs only with the meanings in this table. Use their approved
inflections when they are necessary.

## Writing rules

Use these rules for new and changed documentation:

- Use American English spelling.
- Use the active voice when the actor is important.
- Write one instruction in each sentence.
- Start an instruction with an imperative verb.
- Put a condition before the instruction that it controls.
- Use no more than 20 words in an instruction.
- Use no more than 25 words in a descriptive sentence.
- Use one topic in each paragraph.
- Use no more than six sentences in a paragraph.
- Do not use contractions or semicolons.
- Use the same term for the same item or operation.
- Do not change commands, code, identifiers, or user-interface text.
