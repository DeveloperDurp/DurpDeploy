# Notifications

DurpDeploy supports event-driven notifications for deployment start, success, and failure. Every event is recorded in the internal notification history (visible to admins at `/admin/notifications`), and can optionally be delivered to external services.

Notifications are configured in two parts:
1. **Server-wide settings**: Configured via environment variables (primarily for Email/SMTP).
2. **Project-specific settings**: Configured in the UI under **Project** → **Notifications**.

---

## Email (SMTP)

Email notifications use a server-wide SMTP configuration. Individual projects then define which email addresses should receive notifications.

### 1. Server-wide Configuration

Set the following environment variables for the DurpDeploy process (typically in your systemd unit or Docker environment):

| Variable | Description | Example |
|---|---|---|
| `DURPDEPLOY_SMTP_HOST` | **Required to enable email.** The SMTP server hostname. | `smtp.gmail.com` |
| `DURPDEPLOY_SMTP_PORT` | The SMTP server port. Defaults to `25` if omitted. | `587` |
| `DURPDEPLOY_SMTP_FROM` | The "From" address for notification emails. | `deploy@example.com` |
| `DURPDEPLOY_SMTP_USER` | The SMTP username (if authentication is required). | `deploy@example.com` |
| `DURPDEPLOY_SMTP_PASS` | The SMTP password (if authentication is required). | `app-specific-password` |

**Note on Security:** `smtp.SendMail` (used by DurpDeploy) will attempt to use `STARTTLS` if the server advertises it.

### 2. Project Configuration

Once the server-wide SMTP host is configured:

1. Navigate to your project in the DurpDeploy UI.
2. Click the **Notifications** button in the header.
3. In the **Email** card, enter one or more email addresses separated by commas.
4. Click **Save Changes**.

---

## Slack

Slack notifications use Incoming Webhooks. Configuration is per-project only.

1. Create an **Incoming Webhook** in your Slack workspace.
2. Navigate to your project in DurpDeploy → **Notifications**.
3. In the **Slack** card, paste the Webhook URL.
4. Click **Save Changes**.

---

## Discord

Discord notifications use Webhooks and are per-project.

1. In your Discord channel settings, go to **Integrations** → **Webhooks** and create a new webhook.
2. Copy the **Webhook URL**.
3. Navigate to your project in DurpDeploy → **Notifications**.
4. In the **Discord** card, paste the Webhook URL.
5. Click **Save Changes**.

---

## Gotify

Gotify notifications are per-project, allowing each project to point to a different Gotify server or application.

1. Create an application in your Gotify instance to get an **App Token**.
2. Navigate to your project in DurpDeploy → **Notifications**.
3. In the **Gotify** card, enter your Gotify server URL (e.g., `https://gotify.example.com`) and the App Token.
4. Click **Save Changes**.

---

## Observability

Admins can view the delivery status of all notification events at `/admin/notifications`.

- **Success (Green)**: The provider accepted the message.
- **Failed (Red)**: The provider returned an error (e.g., 404, invalid credentials, SMTP timeout). Hover or click the row to see the error details.
- **Skipped (Gray)**: The provider was not configured for this project or (in the case of Email) the server-wide SMTP host is not set.
