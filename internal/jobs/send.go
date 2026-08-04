package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/danielmichaels/calendar-digest/internal/calendar"
	"github.com/danielmichaels/calendar-digest/internal/digest"
	"github.com/danielmichaels/calendar-digest/internal/store"

	"github.com/riverqueue/river"
)

// ErrNoNotifier means a target's kind has no delivery implementation wired in.
var ErrNoNotifier = errors.New("jobs: send: no notifier for this target kind")

// sendMaxAttempts bounds one SendJob's retry chain, and is longer than
// digestMaxAttempts because nothing restarts it. The fan-out runs only when the
// snapshot is first created, so a discarded SendJob is that target's delivery
// gone for good — where a discarded DigestJob is picked up by the next due
// check. Twelve attempts of ATTEMPT^4 backoff is roughly seventeen hours, which
// is about as long as the digest is worth delivering.
const sendMaxAttempts = 12

// SendArgs identifies one delivery: one target's copy of one digest.
//
// The digest is named by (recipient, date) rather than by snapshot id so the
// args stay readable in riverui and mean the same thing as DigestArgs. The
// token is deliberately absent: it is the secret that protects the page, and
// job rows are visible to anyone with the dashboard.
type SendArgs struct {
	RecipientID int64  `json:"recipient_id"`
	DigestDate  string `json:"digest_date"`
	TargetID    int64  `json:"target_id"`
}

func (SendArgs) Kind() string { return "send" }

func (SendArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: sendMaxAttempts}
}

// SendWorker delivers one digest over one channel.
//
// One job per target, so a channel that is down retries on its own schedule
// without resending on the channels that already worked. Delivery is
// at-least-once: a crash between a successful send and River acking the job
// sends that channel a second copy, which grill Q7 accepts.
type SendWorker struct {
	river.WorkerDefaults[SendArgs]
	*Deps
}

func (w *SendWorker) Work(ctx context.Context, job *river.Job[SendArgs]) error {
	q := store.New(w.DB)

	target, err := q.GetTarget(ctx, job.Args.TargetID)
	if err != nil {
		return fmt.Errorf("jobs: send: target %d: %w", job.Args.TargetID, err)
	}
	notifier, ok := w.Notifiers[target.Kind]
	if !ok {
		return fmt.Errorf("%w: %q", ErrNoNotifier, target.Kind)
	}

	recipient, err := q.GetRecipient(ctx, job.Args.RecipientID)
	if err != nil {
		return fmt.Errorf("jobs: send: recipient %d: %w", job.Args.RecipientID, err)
	}
	snapshot, err := q.GetSnapshotForDate(ctx, store.GetSnapshotForDateParams{
		RecipientID: job.Args.RecipientID,
		DigestDate:  job.Args.DigestDate,
	})
	if err != nil {
		return fmt.Errorf("jobs: send: snapshot %d/%s: %w",
			job.Args.RecipientID, job.Args.DigestDate, err)
	}

	var events []calendar.Event
	if err := json.Unmarshal([]byte(snapshot.Events), &events); err != nil {
		return fmt.Errorf("jobs: send: decode snapshot %d: %w", snapshot.ID, err)
	}

	body, err := notifier.Send(ctx, json.RawMessage(target.Config), digest.Digest{
		RecipientName: recipient.Name,
		Date:          snapshot.DigestDate,
		Token:         snapshot.Token,
		Events:        events,
	})
	if err != nil {
		return fmt.Errorf("jobs: send: %s target %d: %w", target.Kind, target.ID, err)
	}

	// Only reached on a nil error, which is what makes notified_at mean
	// "somebody was actually told" rather than "we tried".
	first, err := store.MarkNotified(ctx, q, snapshot.ID, w.now())
	if err != nil {
		return fmt.Errorf("jobs: send: mark notified: %w", err)
	}
	w.log().Info("digest delivered",
		"recipient_id", recipient.ID,
		"digest_date", snapshot.DigestDate,
		"kind", target.Kind,
		"target_id", target.ID,
		"first", first,
		"body", body)
	return nil
}
