-- name: GetAppState :one
SELECT value
FROM app_state
WHERE key = ?;

-- SetAppState is the raw half of SetFlag; call that instead, since the previous
-- value is the part callers actually need and this does not return it.
-- name: SetAppState :exec
INSERT INTO app_state (key, value, updated_at)
VALUES (?, ?, ?)
ON CONFLICT (key) DO UPDATE SET value      = excluded.value,
                                updated_at = excluded.updated_at;
