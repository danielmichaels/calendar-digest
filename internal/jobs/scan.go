package jobs

import (
	"context"
	"fmt"

	"github.com/danielmichaels/calendar-digest/internal/digest"
	"github.com/danielmichaels/calendar-digest/internal/store"

	"github.com/riverqueue/river"
)

// ScanDueRecipientsArgs drives the due check and carries nothing.
//
// The job asks the database what is owed at the moment it runs rather than
// being told, so a tick delayed by a restart still answers about now instead of
// about whenever it was scheduled. This is the piece River deliberately does
// not own: its periodic schedules are in-memory and leader-scoped, so a cron at
// 21:00 with the process down at 21:00 does not fire late — it does not fire.
type ScanDueRecipientsArgs struct{}

func (ScanDueRecipientsArgs) Kind() string { return "scan_due_recipients" }

// ScanDueRecipientsWorker turns "what is owed right now" into DigestJobs.
//
// It carries no UniqueOpts on purpose. The obvious choice, ByState with
// River's default set, includes JobStateCompleted — which would drop every
// tick after the first success until the job cleaner removed the completed row,
// silently stopping the clock. Overlapping scans need no guard anyway: the
// DigestJobs they insert collapse on ByArgs.
type ScanDueRecipientsWorker struct {
	river.WorkerDefaults[ScanDueRecipientsArgs]
	*Deps
}

func (w *ScanDueRecipientsWorker) Work(
	ctx context.Context,
	_ *river.Job[ScanDueRecipientsArgs],
) error {
	q := store.New(w.DB)

	recipients, err := q.ListEnabledRecipients(ctx)
	if err != nil {
		return fmt.Errorf("jobs: scan: list recipients: %w", err)
	}
	if len(recipients) == 0 {
		return nil
	}

	now := w.now()
	snapshots, err := q.ListSnapshotKeysSince(ctx, digest.SnapshotFloor(now, recipients))
	if err != nil {
		return fmt.Errorf("jobs: scan: list snapshot keys: %w", err)
	}

	owed, skipped := digest.Due(now, recipients, snapshots)
	for _, s := range skipped {
		// Every tick rather than once: a recipient with an unusable zone
		// receives nothing at all, and the silence is exactly what this warning
		// exists to break.
		w.log().Warn("recipient excluded from the due check",
			"recipient_id", s.RecipientID, "error", s.Err)
	}
	if len(owed) == 0 {
		return nil
	}

	params := make([]river.InsertManyParams, 0, len(owed))
	for _, d := range owed {
		params = append(params, river.InsertManyParams{
			Args: DigestArgs{RecipientID: d.RecipientID, DigestDate: d.DigestDate},
		})
	}
	// InsertMany, not InsertManyFast: only the former resolves unique conflicts
	// gracefully, and every tick between a digest falling due and its snapshot
	// existing offers the same job again.
	if _, err := w.Jobs.InsertMany(ctx, params); err != nil {
		return fmt.Errorf("jobs: scan: enqueue digests: %w", err)
	}
	return nil
}
