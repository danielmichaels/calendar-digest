package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// SetFlag stores value under key and returns what was there before, which is
// the empty string the first time a key is ever written.
//
// The previous value rather than a changed bool, because "absent" and
// "different" are not the same thing to a caller deciding whether to alert: a
// process whose first ever check succeeds has not recovered from anything, and
// treating an unwritten key as a change announces a recovery that never
// happened.
//
// Read and write are one transaction, so two workers reaching the same key
// cannot both see the old value and both report the transition.
func SetFlag(
	ctx context.Context,
	db *sql.DB,
	q *Queries,
	key, value string,
	at time.Time,
) (previous string, err error) {
	return EnsureTx(ctx, db, q,
		func(ctx context.Context, _ *sql.Tx, q *Queries) (string, error) {
			previous, err := q.GetAppState(ctx, key)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return "", err
			}
			if err := q.SetAppState(ctx, SetAppStateParams{
				Key:       key,
				Value:     value,
				UpdatedAt: FormatTime(at),
			}); err != nil {
				return "", err
			}
			return previous, nil
		})
}
