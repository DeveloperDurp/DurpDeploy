-- name: GetGlobalNotifications :one
SELECT * FROM global_notifications WHERE id = 1;

-- name: UpdateGlobalNotifications :one
UPDATE global_notifications
SET
    slack_webhook_url = CASE WHEN CAST(sqlc.arg(clear_slack_webhook_url) AS INTEGER) = 1 THEN NULL ELSE COALESCE(NULLIF(CAST(sqlc.narg(slack_webhook_url) AS TEXT), ''), slack_webhook_url) END,
    notify_emails = sqlc.narg(notify_emails),
    gotify_url = sqlc.narg(gotify_url),
    gotify_token = CASE WHEN CAST(sqlc.arg(clear_gotify_token) AS INTEGER) = 1 THEN NULL ELSE COALESCE(NULLIF(CAST(sqlc.narg(gotify_token) AS TEXT), ''), gotify_token) END,
    discord_webhook_url = CASE WHEN CAST(sqlc.arg(clear_discord_webhook_url) AS INTEGER) = 1 THEN NULL ELSE COALESCE(NULLIF(CAST(sqlc.narg(discord_webhook_url) AS TEXT), ''), discord_webhook_url) END,
    updated_at = unixepoch()
WHERE id = 1
RETURNING *;
