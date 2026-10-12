# Notifications

Deployment start, success, and failure events appear at `/admin/notifications`.
External delivery uses project settings or administrator-managed global channels:

- Project events: **Project menu → Settings → Notifications**.
- System events, including backup and agent health: `/admin/notifications/settings`.
- SMTP transport: server environment variables, shared by email delivery.

## Delivery channels

Create the provider credential, enter the settings below, and save the changes.

Slack and Discord webhook URLs and Gotify tokens are write-only, including for administrators.
Project and global settings show whether each credential is configured.
The server retains each credential for notification delivery.

Leave a credential field blank to keep its stored value.
Enter a new value to replace it.
Select **Clear credential** to remove it.
Email recipients and Gotify server URLs remain readable.

API responses return empty credential fields and corresponding `*_configured` Boolean fields.
Blank or omitted credential fields preserve stored values in `PUT` requests.
Set `clear_slack_webhook_url`, `clear_gotify_token`, or `clear_discord_webhook_url` to `true` to clear that credential.
A clear flag takes precedence over a replacement value.
Delivery history shows failure status without credential-bearing provider errors, including retained historical errors.

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
