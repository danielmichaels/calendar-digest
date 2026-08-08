package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/danielmichaels/calendar-digest/internal/calendar"
	"github.com/danielmichaels/calendar-digest/internal/store"

	"github.com/riverqueue/river"
)

// errNoCalendar is the cancellation reason when the process has no credential
// at all, which is a configuration failure rather than a Google one.
var errNoCalendar = errors.New("jobs: digest: no calendar client configured")

// digestMaxAttempts bounds one DigestJob's retry chain. River backs off by
// ATTEMPT^4 seconds, so eight attempts is a little over an hour before the job
// is discarded — at which point the due check picks it up again on the next
// tick, because discarded is not one of the default unique states. The due
// check is the real backstop here, so this only has to be long enough to ride
// out a brief Google outage.
const digestMaxAttempts = 8

// DigestArgs identifies one recipient's digest for one local date.
//
// Both fields are the uniqueness key. A tick every five minutes offers the same
// digest repeatedly until its snapshot exists, and ByArgs is what turns that
// into one attempt chain instead of a stack of them.
type DigestArgs struct {
	RecipientID int64  `json:"recipient_id"`
	DigestDate  string `json:"digest_date"`
	// Force refreshes and fans out the digest even when this day was already
	// captured. It is only for an operator's explicit "send now" request;
	// scheduled jobs leave it false so their five-minute sweep remains
	// idempotent.
	Force bool `json:"force"`
}

func (DigestArgs) Kind() string { return "digest" }

func (DigestArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{
		MaxAttempts: digestMaxAttempts,
		// Default ByState, which includes retryable: a job already backing off
		// blocks a duplicate rather than stacking a second chain. Discarded is
		// not in the set, so once River gives up the due check may start again.
		UniqueOpts: river.UniqueOpts{ByArgs: true},
	}
}

// DigestWorker captures one day of calendar and fans out the sends.
type DigestWorker struct {
	river.WorkerDefaults[DigestArgs]
	*Deps
}

func (w *DigestWorker) Work(ctx context.Context, job *river.Job[DigestArgs]) error {
	q := store.New(w.DB)

	recipient, err := q.GetRecipient(ctx, job.Args.RecipientID)
	if err != nil {
		return fmt.Errorf("jobs: digest: recipient %d: %w", job.Args.RecipientID, err)
	}
	loc, err := time.LoadLocation(recipient.Tz)
	if err != nil {
		return fmt.Errorf("jobs: digest: recipient %d: tz: %w", recipient.ID, err)
	}

	if w.Calendar == nil {
		w.recordCalendarAccess(ctx, false)
		w.raise(ctx, AlertCalendarAccess,
			"GOOGLE_SERVICE_ACCOUNT_JSON is unset, so no digest can be captured at all.")
		return river.JobCancel(errNoCalendar)
	}

	// Strictly outside the transaction below. The pool holds a single
	// connection, so a transaction pinned across this call would block every
	// other query in the process for the length of an HTTP round trip.
	var events []calendar.Event
	for _, calendarID := range calendar.IDs(recipient.CalendarID) {
		calendarEvents, err := w.Calendar.EventsForDay(ctx, calendarID, job.Args.DigestDate, loc)
		if err != nil {
			if errors.Is(err, calendar.ErrAccess) {
				w.recordCalendarAccess(ctx, false)
				w.raise(ctx, AlertCalendarAccess, fmt.Sprintf(
					"%s's digest for %s could not be captured: calendar (%s) cannot "+
						"be read. This will not fix itself — the service account key or the "+
						"calendar share needs attention in the Google console.\n\n%s",
					recipient.Name, job.Args.DigestDate, calendarID, err))
				return river.JobCancel(err)
			}
			return fmt.Errorf("jobs: digest: fetch %s from %s: %w", job.Args.DigestDate, calendarID, err)
		}
		events = append(events, calendarEvents...)
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].Start.Before(events[j].Start) })
	// A day that came back is proof access works, and it arrives sooner than
	// the daily verification would.
	w.recordCalendarAccess(ctx, true)

	encoded, err := json.Marshal(events)
	if err != nil {
		return fmt.Errorf("jobs: digest: encode events: %w", err)
	}

	return store.WithTx(ctx, w.DB, q,
		func(ctx context.Context, tx *sql.Tx, q *store.Queries) error {
			_, created, err := store.UpsertSnapshot(ctx, w.DB, q, store.UpsertSnapshotParams{
				RecipientID: recipient.ID,
				DigestDate:  job.Args.DigestDate,
				Events:      string(encoded),
				CreatedAt:   w.now(),
			})
			if err != nil {
				return fmt.Errorf("jobs: digest: upsert snapshot: %w", err)
			}
			if job.Args.Force && !created {
				if _, err := q.ReplaceSnapshotEvents(ctx, store.ReplaceSnapshotEventsParams{
					RecipientID: recipient.ID,
					DigestDate:  job.Args.DigestDate,
					Events:      string(encoded),
					CreatedAt:   store.FormatTime(w.now()),
				}); err != nil {
					return fmt.Errorf("jobs: digest: refresh snapshot: %w", err)
				}
			}
			// The scheduled sweep must not resend an already captured day. An
			// operator's explicit force request is the exception: it refreshes the
			// saved snapshot, then sends it through every enabled channel again.
			if !created && !job.Args.Force {
				return nil
			}

			targets, err := q.ListEnabledTargets(ctx, recipient.ID)
			if err != nil {
				return fmt.Errorf("jobs: digest: list targets: %w", err)
			}
			if len(targets) == 0 {
				w.log().Warn("digest captured with no enabled target to send it to",
					"recipient_id", recipient.ID, "digest_date", job.Args.DigestDate)
				return nil
			}

			params := make([]river.InsertManyParams, 0, len(targets))
			for _, target := range targets {
				params = append(params, river.InsertManyParams{
					Args: SendArgs{
						RecipientID: recipient.ID,
						DigestDate:  job.Args.DigestDate,
						TargetID:    target.ID,
					},
				})
			}
			// InsertManyTx on the same tx: a SendJob that outlived a rolled-back
			// snapshot would try to send a digest that does not exist.
			if _, err := w.Jobs.InsertManyTx(ctx, tx, params); err != nil {
				return fmt.Errorf("jobs: digest: enqueue sends: %w", err)
			}
			return nil
		})
}
