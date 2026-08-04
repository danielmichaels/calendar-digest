package jobs_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/danielmichaels/calendar-digest/internal/store"
	"github.com/danielmichaels/calendar-digest/internal/testhelpers"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riversqlite"
)

var errBoom = errors.New("boom")

type probeArgs struct {
	Note string `json:"note"`
}

func (probeArgs) Kind() string { return "probe" }

type probeWorker struct {
	river.WorkerDefaults[probeArgs]
}

func (probeWorker) Work(context.Context, *river.Job[probeArgs]) error { return nil }

// newRiverClient builds a client on db with no queues, so it inserts but never
// works: these tests are about what the transaction spans, not about jobs
// running. The worker is still registered because River rejects an insert
// whose kind is not in the bundle.
func newRiverClient(t *testing.T, db *sql.DB) *river.Client[*sql.Tx] {
	t.Helper()
	workers := river.NewWorkers()
	river.AddWorker(workers, &probeWorker{})

	rc, err := river.NewClient(riversqlite.New(db), &river.Config{Workers: workers})
	if err != nil {
		t.Fatalf("new river client: %v", err)
	}
	return rc
}

func countJobs(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM river_job`).Scan(&n); err != nil {
		t.Fatalf("count jobs: %v", err)
	}
	return n
}

// The property Phase 4's fan-out rests on: the snapshot write and the SendJob
// enqueues are one commit.
//
// Note what this does not prove. InsertTx runs through the *sql.Tx it is
// handed, not through the handle the client was built with, so this passes
// even with River on a separate handle. The shared handle is needed for the
// lock contention and the missing pragmas, not for this.
func TestInsertTxCommitsWithTheRowItDependsOn(t *testing.T) {
	db := testhelpers.NewDB(t)
	q := store.New(db)
	rc := newRiverClient(t, db)

	_, err := store.InTx(t.Context(), db, q,
		func(ctx context.Context, tx *sql.Tx, q *store.Queries) (store.Recipients, error) {
			r, err := q.CreateRecipient(ctx, store.CreateRecipientParams{
				Name: "spans", CalendarID: "c", NotifyTime: "21:00", Tz: "UTC", Enabled: true,
			})
			if err != nil {
				return store.Recipients{}, err
			}
			if _, err := rc.InsertTx(ctx, tx, probeArgs{Note: r.Name}, nil); err != nil {
				return store.Recipients{}, err
			}
			return r, nil
		})
	if err != nil {
		t.Fatalf("InTx: %v", err)
	}

	rows, err := q.ListRecipients(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Errorf("recipients = %d, want 1", len(rows))
	}
	if got := countJobs(t, db); got != 1 {
		t.Errorf("jobs = %d, want 1", got)
	}
}

// The half that matters most: a rollback after the enqueue must take the job
// with it, or a SendJob outlives the snapshot it was going to send.
func TestInsertTxRollsBackWithTheRowItDependsOn(t *testing.T) {
	db := testhelpers.NewDB(t)
	q := store.New(db)
	rc := newRiverClient(t, db)

	err := store.WithTx(t.Context(), db, q,
		func(ctx context.Context, tx *sql.Tx, q *store.Queries) error {
			r, err := q.CreateRecipient(ctx, store.CreateRecipientParams{
				Name: "orphan", CalendarID: "c", NotifyTime: "21:00", Tz: "UTC", Enabled: true,
			})
			if err != nil {
				return err
			}
			if _, err := rc.InsertTx(ctx, tx, probeArgs{Note: r.Name}, nil); err != nil {
				return err
			}
			return errBoom
		})
	if !errors.Is(err, errBoom) {
		t.Fatalf("err = %v, want errBoom", err)
	}

	rows, err := q.ListRecipients(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Errorf("recipients = %d, want 0", len(rows))
	}
	if got := countJobs(t, db); got != 0 {
		t.Errorf("jobs = %d, want 0: the enqueue outlived the write it depends on", got)
	}
}
