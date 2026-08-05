package jobs_test

import (
	"testing"
	"time"

	"github.com/danielmichaels/calendar-digest/internal/jobs"
	"github.com/danielmichaels/calendar-digest/internal/store"
	"github.com/danielmichaels/calendar-digest/internal/testhelpers"

	"github.com/riverqueue/river"
)

func TestPurgeSnapshotsKeepsTheRetentionBoundary(t *testing.T) {
	db := testhelpers.NewDB(t)
	q := store.New(db)
	r := createRecipient(t, q, "ada", "Australia/Brisbane", "21:00")

	createSnapshot(t, db, q, r.ID, "2026-05-05")
	boundary := createSnapshot(t, db, q, r.ID, "2026-05-06")
	createSnapshot(t, db, q, r.ID, "2026-08-04")

	w := &jobs.PurgeSnapshotsWorker{Deps: &jobs.Deps{
		DB:                    db,
		Now:                   func() time.Time { return time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC) },
		SnapshotRetentionDays: 90,
	}}
	if err := w.Work(t.Context(), &river.Job[jobs.PurgeSnapshotsArgs]{}); err != nil {
		t.Fatalf("purge: %v", err)
	}

	if _, err := q.GetSnapshotForDate(t.Context(), store.GetSnapshotForDateParams{
		RecipientID: r.ID,
		DigestDate:  "2026-05-05",
	}); err == nil {
		t.Error("snapshot before retention boundary still exists")
	}
	got, err := q.GetSnapshotForDate(t.Context(), store.GetSnapshotForDateParams{
		RecipientID: r.ID,
		DigestDate:  boundary.DigestDate,
	})
	if err != nil {
		t.Fatalf("boundary snapshot: %v", err)
	}
	if got.ID != boundary.ID {
		t.Errorf("boundary snapshot id = %d, want %d", got.ID, boundary.ID)
	}
}

func TestPurgeSnapshotsRejectsAnUnsafeRetentionValue(t *testing.T) {
	db := testhelpers.NewDB(t)
	w := &jobs.PurgeSnapshotsWorker{Deps: &jobs.Deps{DB: db}}

	if err := w.Work(t.Context(), &river.Job[jobs.PurgeSnapshotsArgs]{}); err == nil {
		t.Fatal("purge with zero retention = nil, want error")
	}
}
