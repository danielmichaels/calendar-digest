-- name: CreateRecipient :one
INSERT INTO recipients (name, calendar_id, notify_time, tz, enabled)
VALUES (?, ?, ?, ?, ?)
RETURNING *;

-- name: GetRecipient :one
SELECT *
FROM recipients
WHERE id = ?;

-- name: ListRecipients :many
SELECT *
FROM recipients
ORDER BY name;

-- name: DeleteRecipient :exec
DELETE
FROM recipients
WHERE id = ?;
