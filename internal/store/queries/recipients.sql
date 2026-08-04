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

-- ListEnabledRecipients feeds the due check. Disabled recipients are still
-- listed in the UI, so the filter belongs here rather than in ListRecipients.
-- name: ListEnabledRecipients :many
SELECT *
FROM recipients
WHERE enabled
ORDER BY name;

-- name: UpdateRecipient :one
UPDATE recipients
SET name        = ?,
    calendar_id = ?,
    notify_time = ?,
    tz          = ?,
    enabled     = ?
WHERE id = ?
RETURNING *;

-- name: SetRecipientEnabled :exec
UPDATE recipients
SET enabled = ?
WHERE id = ?;

-- name: DeleteRecipient :exec
DELETE
FROM recipients
WHERE id = ?;
