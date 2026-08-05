package jobs_test

import (
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/danielmichaels/calendar-digest/internal/jobs"
	"github.com/danielmichaels/calendar-digest/internal/store"
	"github.com/danielmichaels/calendar-digest/internal/testhelpers"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riversqlite"
)

// newEnqueuer is a River client that inserts but never works: these tests drive
// Work directly so they can assert what a single run of one worker enqueued,
// without waiting on a running client.
func newEnqueuer(t *testing.T, db *sql.DB) *river.Client[*sql.Tx] {
	t.Helper()
	rc, err := river.NewClient(riversqlite.New(db), &river.Config{
		Workers: jobs.NewWorkers(&jobs.Deps{}),
	})
	if err != nil {
		t.Fatalf("new river client: %v", err)
	}
	return rc
}

// argsOfKind reads back every queued job of one kind, oldest first. River
// stores args as SQLite jsonb, so json() is needed to get text out.
func argsOfKind[T river.JobArgs](t *testing.T, db *sql.DB) []T {
	t.Helper()
	var zero T
	rows, err := db.QueryContext(t.Context(),
		`SELECT json(args) FROM river_job WHERE kind = ? ORDER BY id`, zero.Kind())
	if err != nil {
		t.Fatalf("query %s jobs: %v", zero.Kind(), err)
	}
	defer func() { _ = rows.Close() }()

	var out []T
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			t.Fatalf("scan args: %v", err)
		}
		var v T
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			t.Fatalf("decode args %s: %v", raw, err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate jobs: %v", err)
	}
	return out
}

func createRecipient(t *testing.T, q *store.Queries, name, tz, notifyTime string) store.Recipients {
	t.Helper()
	r, err := q.CreateRecipient(t.Context(), store.CreateRecipientParams{
		Name:       name,
		CalendarID: name + "@example.com",
		NotifyTime: notifyTime,
		Tz:         tz,
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("create recipient %s: %v", name, err)
	}
	return r
}

func createSnapshot(t *testing.T, db *sql.DB, q *store.Queries, recipientID int64, date string) store.DigestSnapshots {
	t.Helper()
	snapshot, _, err := store.UpsertSnapshot(t.Context(), db, q, store.UpsertSnapshotParams{
		RecipientID: recipientID,
		DigestDate:  date,
		Events:      "[]",
		CreatedAt:   time.Now(),
	})
	if err != nil {
		t.Fatalf("create snapshot: %v", err)
	}
	return snapshot
}

func scanWorker(db *sql.DB, rc *river.Client[*sql.Tx], now time.Time) *jobs.ScanDueRecipientsWorker {
	return &jobs.ScanDueRecipientsWorker{Deps: &jobs.Deps{
		DB:   db,
		Jobs: rc,
		Now:  func() time.Time { return now },
	}}
}

func runScan(t *testing.T, w *jobs.ScanDueRecipientsWorker) {
	t.Helper()
	if err := w.Work(t.Context(), &river.Job[jobs.ScanDueRecipientsArgs]{}); err != nil {
		t.Fatalf("scan: %v", err)
	}
}

func brisbane(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Australia/Brisbane")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	return loc
}

func TestScanEnqueuesADigestForARecipientPastTheirNotifyTime(t *testing.T) {
	db := testhelpers.NewDB(t)
	q := store.New(db)
	rc := newEnqueuer(t, db)

	r := createRecipient(t, q, "ada", "Australia/Brisbane", "21:00")
	// Last night's digest, so only tomorrow's is outstanding.
	createSnapshot(t, db, q, r.ID, "2026-08-04")

	runScan(t, scanWorker(db, rc, time.Date(2026, 8, 4, 21, 5, 0, 0, brisbane(t))))

	got := argsOfKind[jobs.DigestArgs](t, db)
	want := []jobs.DigestArgs{{RecipientID: r.ID, DigestDate: "2026-08-05"}}
	if len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("enqueued %v, want %v", got, want)
	}
}

func TestScanEnqueuesNothingBeforeTheNotifyTime(t *testing.T) {
	db := testhelpers.NewDB(t)
	q := store.New(db)
	rc := newEnqueuer(t, db)

	r := createRecipient(t, q, "ada", "Australia/Brisbane", "21:00")
	createSnapshot(t, db, q, r.ID, "2026-08-04")

	runScan(t, scanWorker(db, rc, time.Date(2026, 8, 4, 20, 59, 0, 0, brisbane(t))))

	if got := argsOfKind[jobs.DigestArgs](t, db); len(got) != 0 {
		t.Errorf("enqueued %v, want nothing", got)
	}
}

// The property the whole five-minute tick depends on: a snapshot already
// written is the durable "done", so the next tick must not enqueue it again.
func TestScanEnqueuesNothingForADigestAlreadySnapshotted(t *testing.T) {
	db := testhelpers.NewDB(t)
	q := store.New(db)
	rc := newEnqueuer(t, db)

	r := createRecipient(t, q, "ada", "Australia/Brisbane", "21:00")
	createSnapshot(t, db, q, r.ID, "2026-08-04")
	createSnapshot(t, db, q, r.ID, "2026-08-05")

	runScan(t, scanWorker(db, rc, time.Date(2026, 8, 4, 21, 5, 0, 0, brisbane(t))))

	if got := argsOfKind[jobs.DigestArgs](t, db); len(got) != 0 {
		t.Errorf("enqueued %v, want nothing", got)
	}
}

// Ticking every five minutes means the same digest is offered over and over
// until its snapshot exists. UniqueOpts{ByArgs} is what stops that becoming a
// second attempt chain for work already in flight.
func TestScanRepeatedDoesNotStackDuplicateDigests(t *testing.T) {
	db := testhelpers.NewDB(t)
	q := store.New(db)
	rc := newEnqueuer(t, db)

	r := createRecipient(t, q, "ada", "Australia/Brisbane", "21:00")
	createSnapshot(t, db, q, r.ID, "2026-08-04")

	w := scanWorker(db, rc, time.Date(2026, 8, 4, 21, 5, 0, 0, brisbane(t)))
	runScan(t, w)
	runScan(t, w)
	runScan(t, w)

	got := argsOfKind[jobs.DigestArgs](t, db)
	if len(got) != 1 {
		t.Errorf("enqueued %d digests over three ticks, want 1: %v", len(got), got)
	}
}

// A disabled recipient is still listed for the UI, so the filter has to hold
// here rather than being assumed from the query.
func TestScanIgnoresADisabledRecipient(t *testing.T) {
	db := testhelpers.NewDB(t)
	q := store.New(db)
	rc := newEnqueuer(t, db)

	r := createRecipient(t, q, "ada", "Australia/Brisbane", "21:00")
	if err := q.SetRecipientEnabled(t.Context(), store.SetRecipientEnabledParams{
		ID: r.ID, Enabled: false,
	}); err != nil {
		t.Fatalf("disable recipient: %v", err)
	}

	runScan(t, scanWorker(db, rc, time.Date(2026, 8, 4, 21, 5, 0, 0, brisbane(t))))

	if got := argsOfKind[jobs.DigestArgs](t, db); len(got) != 0 {
		t.Errorf("enqueued %v, want nothing", got)
	}
}

// One unusable row must not cost every other recipient their digest.
func TestScanEnqueuesForHealthyRecipientsDespiteABrokenOne(t *testing.T) {
	db := testhelpers.NewDB(t)
	q := store.New(db)
	rc := newEnqueuer(t, db)

	broken := createRecipient(t, q, "broken", "Australia/Brisbain", "21:00")
	healthy := createRecipient(t, q, "healthy", "Australia/Brisbane", "21:00")
	createSnapshot(t, db, q, broken.ID, "2026-08-04")
	createSnapshot(t, db, q, healthy.ID, "2026-08-04")

	runScan(t, scanWorker(db, rc, time.Date(2026, 8, 4, 21, 5, 0, 0, brisbane(t))))

	got := argsOfKind[jobs.DigestArgs](t, db)
	if len(got) != 1 || got[0].RecipientID != healthy.ID {
		t.Fatalf("enqueued %v, want only the healthy recipient (%d)", got, healthy.ID)
	}
}

// Recovery: 21:00 passed with the process down, and the morning tick still owes
// that date.
func TestScanRecoversADigestMissedOvernight(t *testing.T) {
	db := testhelpers.NewDB(t)
	q := store.New(db)
	rc := newEnqueuer(t, db)

	r := createRecipient(t, q, "ada", "Australia/Brisbane", "21:00")
	createSnapshot(t, db, q, r.ID, "2026-08-04")

	runScan(t, scanWorker(db, rc, time.Date(2026, 8, 5, 6, 0, 0, 0, brisbane(t))))

	got := argsOfKind[jobs.DigestArgs](t, db)
	want := []jobs.DigestArgs{{RecipientID: r.ID, DigestDate: "2026-08-05"}}
	if len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("enqueued %v, want %v", got, want)
	}
}
