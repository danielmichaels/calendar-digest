package jobs

import (
	"context"
	"fmt"
	"time"

	"github.com/danielmichaels/calendar-digest/internal/store"
	"github.com/riverqueue/river"
)

// PurgeSnapshotsArgs drives the daily retention pass.
type PurgeSnapshotsArgs struct{}

func (PurgeSnapshotsArgs) Kind() string { return "purge_snapshots" }

// PurgeSnapshotsWorker removes detail-page snapshots older than the configured
// retention window. The cutoff is a digest date, rather than a capture
// timestamp, because snapshot dates are local dates belonging to recipients.
// The boundary itself is retained: a 90-day setting keeps the date exactly 90
// days before today and removes dates before it.
type PurgeSnapshotsWorker struct {
	river.WorkerDefaults[PurgeSnapshotsArgs]
	*Deps
}

func (w *PurgeSnapshotsWorker) Work(
	ctx context.Context,
	_ *river.Job[PurgeSnapshotsArgs],
) error {
	if w.SnapshotRetentionDays < 1 {
		return fmt.Errorf("jobs: purge snapshots: retention days must be greater than zero")
	}

	cutoff := w.now().UTC().AddDate(0, 0, -w.SnapshotRetentionDays).Format(time.DateOnly)
	purged, err := store.New(w.DB).PurgeSnapshotsBefore(ctx, cutoff)
	if err != nil {
		return fmt.Errorf("jobs: purge snapshots before %s: %w", cutoff, err)
	}
	w.log().Info("purged old digest snapshots", "count", purged, "before", cutoff)
	return nil
}
