# Notifications

Deployment start, success, and failure events appear at `/admin/notifications`.
External delivery uses project settings or administrator-managed global channels:

- Project events: **Project menu → Settings → Notifications**.
- System events, including backup and agent health: `/admin/notifications/settings`.
- SMTP transport: server environment variables, shared by email delivery.

## Delivery channels

Create the provider credential, enter the settings below, and save the changes.

| Channel | Settings |
| --- | --- |
| Email | Comma-separated recipient addresses; SMTP must be configured on the server |
| Slack | Incoming Webhook URL |
| Discord | Channel webhook URL from **Integrations → Webhooks** |
| Gotify | Server URL and application token |

## SMTP

| Variable | Purpose |
| --- | --- |
| `DURPDEPLOY_SMTP_HOST` | Hostname; required to enable email |
| `DURPDEPLOY_SMTP_PORT` | Port; required with the hostname, no default |
| `DURPDEPLOY_SMTP_FROM` | Sender address |
| `DURPDEPLOY_SMTP_USER` | Username, when authentication is required |
| `DURPDEPLOY_SMTP_PASS` | Password, when authentication is required |

The Go SMTP client attempts STARTTLS when the server advertises it.
Keep passwords, tokens, and webhook URLs out of source control.

## Delivery history

Administrators inspect delivery status at `/admin/notifications`:

- **Success:** the provider accepted the message.
- **Failed:** delivery returned an error; inspect the row details.
- **Skipped:** the channel or required SMTP configuration is absent.
