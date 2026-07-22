-- name: GetGlobalNotifications :one
SELECT * FROM global_notifications WHERE id = 1;

-- name: UpdateGlobalNotifications :one
UPDATE global_notifications
SET
    slack_webhook_url = ?,
    notify_emails = ?,
    gotify_url = ?,
    gotify_token = ?,
    discord_webhook_url = ?,
    updated_at = unixepoch()
WHERE id = 1
RETURNING *;
