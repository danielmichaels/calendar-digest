package store

import (
	"context"
	"database/sql"
)

// TxFunc is the body of a transaction. It receives Queries already bound to
// the transaction, so no call site writes q.WithTx(tx) again, plus the raw tx
// for the APIs that need one directly — River's InsertTx above all.
type TxFunc[T any] func(ctx context.Context, tx *sql.Tx, q *Queries) (T, error)

// InTx runs fn in a transaction, committing when it returns nil and rolling
// back otherwise. A panic rolls back and propagates. fn's error comes back
// unwrapped so callers keep matching their own sentinels through it, and on
// any failure the returned T is the zero value rather than a half-built result
// from a transaction that did not commit.
//
// fn must not perform network I/O. The pool holds a single connection, so a
// transaction pinned across an HTTP call blocks every other query in the
// process for its duration.
func InTx[T any](ctx context.Context, db *sql.DB, q *Queries, fn TxFunc[T]) (T, error) {
	var out T
	err := beginFunc(ctx, db, func(tx *sql.Tx) error {
		v, err := fn(ctx, tx, q.WithTx(tx))
		if err != nil {
			return err
		}
		out = v
		return nil
	})
	if err != nil {
		var zero T
		return zero, err
	}
	return out, nil
}

// WithTx is InTx for a transaction that produces no value.
func WithTx(ctx context.Context, db *sql.DB, q *Queries,
	fn func(ctx context.Context, tx *sql.Tx, q *Queries) error,
) error {
	_, err := InTx(ctx, db, q,
		func(ctx context.Context, tx *sql.Tx, q *Queries) (struct{}, error) {
			return struct{}{}, fn(ctx, tx, q)
		})
	return err
}

// EnsureTx runs fn in q's transaction when q is already bound to one, and in a
// new transaction otherwise. Joining rather than nesting is deliberate: the
// caller's commit or rollback decides fn's fate too, so a helper that may or
// may not be called mid-transaction cannot half-commit its part.
//
// It is what lets a function be called both standalone and as one step of a
// larger write without the caller passing a flag or a tx it cannot supply —
// and with the pool capped at one connection, a function that reached for the
// pool mid-transaction would not merely be slow, it would wait for the
// connection that transaction is holding.
func EnsureTx[T any](ctx context.Context, db *sql.DB, q *Queries, fn TxFunc[T]) (T, error) {
	if tx, ok := q.db.(*sql.Tx); ok {
		return fn(ctx, tx, q)
	}
	return InTx(ctx, db, q, fn)
}

// beginFunc is the database/sql stand-in for pgx.BeginFunc, which has no
// equivalent here: it begins, commits when fn returns nil, and rolls back
// otherwise, with a panic rolling back and propagating.
//
// The named return is the entire reason this is its own function. The commit
// happens in the deferred close, and with a plain error return `err =
// tx.Commit()` would assign to a local nobody reads — a failed commit
// reporting success, with no symptom until someone notices the data is
// missing.
func beginFunc(ctx context.Context, db *sql.DB, fn func(*sql.Tx) error) (err error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
		if err != nil {
			_ = tx.Rollback()
			return
		}
		err = tx.Commit()
	}()
	return fn(tx)
}
