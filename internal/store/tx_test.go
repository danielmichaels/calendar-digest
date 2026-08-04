// External test package: the helpers are exercised through the same surface
// service code uses, and it breaks the import cycle with testhelpers, which
// imports store itself.
package store_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/danielmichaels/calendar-digest/internal/store"
	"github.com/danielmichaels/calendar-digest/internal/testhelpers"
)

var errBoom = errors.New("boom")

// countRecipients reads through the pool, so it only ever sees committed rows.
func countRecipients(t *testing.T, q *store.Queries, name string) int {
	t.Helper()
	rows, err := q.ListRecipients(t.Context())
	if err != nil {
		t.Fatalf("list recipients: %v", err)
	}
	var n int
	for _, r := range rows {
		if r.Name == name {
			n++
		}
	}
	return n
}

func TestWithTxCommits(t *testing.T) {
	db := testhelpers.NewDB(t)
	q := store.New(db)

	err := store.WithTx(t.Context(), db, q,
		func(ctx context.Context, _ *sql.Tx, q *store.Queries) error {
			_, err := q.CreateRecipient(ctx, newRecipient("commits"))
			return err
		})
	if err != nil {
		t.Fatalf("WithTx: %v", err)
	}

	if got := countRecipients(t, q, "commits"); got != 1 {
		t.Errorf("committed rows = %d, want 1", got)
	}
}

func TestWithTxRollsBackOnError(t *testing.T) {
	db := testhelpers.NewDB(t)
	q := store.New(db)

	err := store.WithTx(t.Context(), db, q,
		func(ctx context.Context, _ *sql.Tx, q *store.Queries) error {
			if _, err := q.CreateRecipient(ctx, newRecipient("rollback")); err != nil {
				return err
			}
			return errBoom
		})
	// Unwrapped, so callers keep matching their own sentinels through it.
	if !errors.Is(err, errBoom) {
		t.Fatalf("err = %v, want errBoom", err)
	}

	if got := countRecipients(t, q, "rollback"); got != 0 {
		t.Errorf("rows after rollback = %d, want 0", got)
	}
}

// The test that catches a hand-rolled helper with no recover path: without one
// the panic escapes with the transaction still open, and the connection is
// never returned to a pool that only has one.
func TestWithTxRollsBackOnPanicAndRepanics(t *testing.T) {
	db := testhelpers.NewDB(t)
	q := store.New(db)

	func() {
		defer func() {
			if got := recover(); got != "callback exploded" {
				t.Errorf("recovered %v, want \"callback exploded\"", got)
			}
		}()
		_ = store.WithTx(t.Context(), db, q,
			func(ctx context.Context, _ *sql.Tx, q *store.Queries) error {
				if _, err := q.CreateRecipient(ctx, newRecipient("panic")); err != nil {
					return err
				}
				panic("callback exploded")
			})
		t.Error("panic did not propagate out of WithTx")
	}()

	if got := countRecipients(t, q, "panic"); got != 0 {
		t.Errorf("rows after panic = %d, want 0", got)
	}
}

// The test that catches `out = v` assigned before the error check.
func TestInTxReturnsZeroValueOnError(t *testing.T) {
	db := testhelpers.NewDB(t)
	q := store.New(db)

	got, err := store.InTx(t.Context(), db, q,
		func(ctx context.Context, _ *sql.Tx, q *store.Queries) (store.Recipients, error) {
			r, err := q.CreateRecipient(ctx, newRecipient("zero"))
			if err != nil {
				return store.Recipients{}, err
			}
			// A populated value alongside an error must not escape.
			return r, errBoom
		})
	if !errors.Is(err, errBoom) {
		t.Fatalf("err = %v, want errBoom", err)
	}
	if got != (store.Recipients{}) {
		t.Errorf("got %+v, want the zero value", got)
	}
}

func TestInTxCommitsAndReturnsValue(t *testing.T) {
	db := testhelpers.NewDB(t)
	q := store.New(db)

	got, err := store.InTx(t.Context(), db, q,
		func(ctx context.Context, _ *sql.Tx, q *store.Queries) (store.Recipients, error) {
			return q.CreateRecipient(ctx, newRecipient("value"))
		})
	if err != nil {
		t.Fatalf("InTx: %v", err)
	}

	persisted, err := q.GetRecipient(t.Context(), got.ID)
	if err != nil {
		t.Fatalf("get recipient %d: %v", got.ID, err)
	}
	if persisted.Name != "value" {
		t.Errorf("persisted name = %q, want %q", persisted.Name, "value")
	}
}

// Proves EnsureTx joined rather than opened: rolling the caller's transaction
// back must discard the callback's write too. Opening its own would leave the
// row behind.
func TestEnsureTxJoinsCallersTransaction(t *testing.T) {
	db := testhelpers.NewDB(t)
	q := store.New(db)

	outer, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	_, err = store.EnsureTx(t.Context(), db, q.WithTx(outer),
		func(ctx context.Context, _ *sql.Tx, q *store.Queries) (store.Recipients, error) {
			return q.CreateRecipient(ctx, newRecipient("joined"))
		})
	if err != nil {
		t.Fatalf("EnsureTx: %v", err)
	}

	if err := outer.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if got := countRecipients(t, q, "joined"); got != 0 {
		t.Errorf("rows after outer rollback = %d, want 0: EnsureTx opened its own transaction", got)
	}
}

func TestEnsureTxOpensItsOwnTransaction(t *testing.T) {
	db := testhelpers.NewDB(t)
	q := store.New(db)

	_, err := store.EnsureTx(t.Context(), db, q,
		func(ctx context.Context, _ *sql.Tx, q *store.Queries) (store.Recipients, error) {
			return q.CreateRecipient(ctx, newRecipient("own"))
		})
	if err != nil {
		t.Fatalf("EnsureTx: %v", err)
	}
	if got := countRecipients(t, q, "own"); got != 1 {
		t.Errorf("committed rows = %d, want 1", got)
	}

	_, err = store.EnsureTx(t.Context(), db, q,
		func(ctx context.Context, _ *sql.Tx, q *store.Queries) (store.Recipients, error) {
			if _, err := q.CreateRecipient(ctx, newRecipient("own-rollback")); err != nil {
				return store.Recipients{}, err
			}
			return store.Recipients{}, errBoom
		})
	if !errors.Is(err, errBoom) {
		t.Fatalf("err = %v, want errBoom", err)
	}
	if got := countRecipients(t, q, "own-rollback"); got != 0 {
		t.Errorf("rows after rollback = %d, want 0", got)
	}
}

// The one test that catches the named-return trap. With a plain error return
// the deferred `err = tx.Commit()` assigns to a local nobody reads: the commit
// still runs, so every other test here still passes, and only a commit that
// actually fails tells the two apart.
//
// A deferred constraint is what produces that: it is checked at COMMIT rather
// than at INSERT, so the callback succeeds and the commit does not.
func TestWithTxReportsCommitFailure(t *testing.T) {
	db := testhelpers.NewDB(t)
	q := store.New(db)

	_, err := db.ExecContext(t.Context(), `
		CREATE TABLE deferred_child (
			parent_id INTEGER NOT NULL
				REFERENCES recipients (id) DEFERRABLE INITIALLY DEFERRED
		)`)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}

	err = store.WithTx(t.Context(), db, q,
		func(ctx context.Context, tx *sql.Tx, _ *store.Queries) error {
			_, err := tx.ExecContext(ctx,
				`INSERT INTO deferred_child (parent_id) VALUES (404)`)
			return err
		})
	if err == nil {
		t.Fatal("a failed commit was reported as success: beginFunc's error return is not named")
	}
}

// EnsureTx is a correctness requirement, not an ergonomic one, and this is
// why: with the pool capped at a single connection, reaching for the
// pool-bound Queries inside a transaction does not queue behind it, it waits
// for the connection the transaction is holding and never gets it.
//
// The bounded context is what keeps this test from being the hang it
// describes.
func TestPoolAccessInsideTransactionDeadlocks(t *testing.T) {
	db := testhelpers.NewDB(t)
	q := store.New(db)

	err := store.WithTx(t.Context(), db, q,
		func(ctx context.Context, _ *sql.Tx, _ *store.Queries) error {
			ctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
			defer cancel()

			// The pool-bound q from the enclosing scope, deliberately, rather
			// than the transaction-bound one the callback was handed.
			_, err := q.ListRecipients(ctx)
			return err
		})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded: the single connection is no longer exclusive to the transaction", err)
	}
}
