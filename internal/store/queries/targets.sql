-- name: CreateTarget :one
INSERT INTO notification_targets (recipient_id, kind, config, enabled)
VALUES (?, ?, ?, ?)
RETURNING *;

-- name: GetTarget :one
SELECT *
FROM notification_targets
WHERE id = ?;

-- name: ListTargets :many
SELECT *
FROM notification_targets
WHERE recipient_id = ?
ORDER BY kind;

-- ListEnabledTargets is the fan-out: one SendJob per row it returns.
-- name: ListEnabledTargets :many
SELECT *
FROM notification_targets
WHERE recipient_id = ?
  AND enabled
ORDER BY kind;

-- name: UpdateTargetConfig :one
UPDATE notification_targets
SET config = ?
WHERE id = ?
RETURNING *;

-- name: SetTargetEnabled :exec
UPDATE notification_targets
SET enabled = ?
WHERE id = ?;

-- name: DeleteTarget :exec
DELETE
FROM notification_targets
WHERE id = ?;
