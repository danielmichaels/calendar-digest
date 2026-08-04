-- InsertSnapshotIfAbsent is half of UpsertSnapshot; call that rather than this.
-- DO NOTHING plus a re-read, instead of catching the uniqueness violation,
-- because the row the loser wants is the one the winner wrote.
-- The affected-row count is the only exact answer to "did this call write it":
-- comparing created_at would call a second attempt within the same second a
-- fresh insert.
-- name: InsertSnapshotIfAbsent :execrows
INSERT INTO digest_snapshots (recipient_id, digest_date, token, events, created_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (recipient_id, digest_date) DO NOTHING;

-- name: GetSnapshotForDate :one
SELECT *
FROM digest_snapshots
WHERE recipient_id = ?
  AND digest_date = ?;

-- name: FindSnapshotByToken :one
SELECT *
FROM digest_snapshots
WHERE token = ?;

-- SetNotifiedAt is the raw half of MarkNotified; call that instead. The
-- IS NULL guard is what keeps the timestamp on the first channel that got
-- through: every enabled target calls this, and without it the last one to
-- succeed would overwrite the first.
-- name: SetNotifiedAt :execrows
UPDATE digest_snapshots
SET notified_at = ?
WHERE id = ?
  AND notified_at IS NULL;

-- ListSnapshotKeysSince tells the due check which digests are already done.
-- Keys only: the events column holds a whole day of calendar data per row and
-- the check has no use for it.
-- name: ListSnapshotKeysSince :many
SELECT recipient_id, digest_date
FROM digest_snapshots
WHERE digest_date >= ?
ORDER BY digest_date, recipient_id;

-- name: PurgeSnapshotsBefore :execrows
DELETE
FROM digest_snapshots
WHERE digest_date < ?;
