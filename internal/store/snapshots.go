package store

import (
	"context"
	"database/sql"
	"time"
)

// timeLayout is how every timestamp column is stored: RFC3339 in UTC. Fixed
// width and normalised to one zone, so SQLite's text comparison orders and
// range-filters timestamps correctly without parsing them.
const timeLayout = time.RFC3339

// FormatTime renders t for storage, converting to UTC first. A timestamp
// written in local time would compare wrongly against every other row.
func FormatTime(t time.Time) string { return t.UTC().Format(timeLayout) }

// ParseTime reads a timestamp back out of a column FormatTime wrote.
func ParseTime(s string) (time.Time, error) { return time.Parse(timeLayout, s) }

// MarkNotified records at as the moment this digest first reached anybody,
// reporting whether this caller was that first one. Later callers are a no-op
// and report false.
//
// Every enabled target calls it after a successful send, so it answers "was
// anyone told", not "was everyone told" — a channel that is permanently broken
// leaves no trace here, only discarded SendJobs in riverui.
func MarkNotified(ctx context.Context, q *Queries, id int64, at time.Time) (bool, error) {
	affected, err := q.SetNotifiedAt(ctx, SetNotifiedAtParams{
		ID:         id,
		NotifiedAt: sql.NullString{String: FormatTime(at), Valid: true},
	})
	if err != nil {
		return false, err
	}
	return affected == 1, nil
}

type UpsertSnapshotParams struct {
	RecipientID int64
	DigestDate  string
	Events      string
	CreatedAt   time.Time
}

// snapshotUpsert is the pair EnsureTx carries back, since it is generic over a
// single value.
type snapshotUpsert struct {
	snapshot DigestSnapshots
	created  bool
}

// UpsertSnapshot returns the snapshot for (recipient, date), writing it first
// if there is none, and reports whether this call is the one that wrote it.
//
// That flag is the fan-out's guard. A DigestJob that finds the snapshot
// already there must not enqueue a second round of SendJobs, or a retry after
// a partial failure sends the digest twice to the channels that worked.
//
// The events of an existing row are left alone. Its token is already in a
// message somebody received, and the page behind that link must not start
// showing a different day's worth of calendar.
//
// It runs through EnsureTx, so called inside the fan-out's transaction the
// snapshot lives or dies with the enqueues, and called on its own it opens a
// transaction of its own.
func UpsertSnapshot(
	ctx context.Context,
	db *sql.DB,
	q *Queries,
	arg UpsertSnapshotParams,
) (DigestSnapshots, bool, error) {
	out, err := EnsureTx(ctx, db, q,
		func(ctx context.Context, _ *sql.Tx, q *Queries) (snapshotUpsert, error) {
			inserted, err := q.InsertSnapshotIfAbsent(ctx, InsertSnapshotIfAbsentParams{
				RecipientID: arg.RecipientID,
				DigestDate:  arg.DigestDate,
				Token:       NewToken(),
				Events:      arg.Events,
				CreatedAt:   FormatTime(arg.CreatedAt),
			})
			if err != nil {
				return snapshotUpsert{}, err
			}

			// Re-read rather than use RETURNING: on conflict the insert
			// returns nothing, and the row the caller needs is the one already
			// there.
			snapshot, err := q.GetSnapshotForDate(ctx, GetSnapshotForDateParams{
				RecipientID: arg.RecipientID,
				DigestDate:  arg.DigestDate,
			})
			if err != nil {
				return snapshotUpsert{}, err
			}
			return snapshotUpsert{snapshot: snapshot, created: inserted == 1}, nil
		})
	return out.snapshot, out.created, err
}
