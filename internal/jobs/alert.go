package jobs

import (
	"context"
	"time"

	"github.com/danielmichaels/calendar-digest/internal/store"

	"github.com/riverqueue/river"
)

// alertPeriod is how often one subject may alert. The first failure goes out
// immediately; while it stays broken the rest of that day is silent, because an
// alert that arrives every five minutes is one nobody reads by the second hour.
const alertPeriod = 24 * time.Hour

// alertMaxAttempts bounds the alert's own retry chain. Short: if Telegram is
// unreachable for this long the alert has missed its moment, and the next
// failure raises a fresh one.
const alertMaxAttempts = 5

// AlertSubject is the deduplication key as well as the message heading.
type AlertSubject = string

// AlertCalendarAccess is raised when Google refuses the credential or a
// calendar — the one failure that never fixes itself.
const AlertCalendarAccess AlertSubject = "Calendar access refused"

// AlertCalendarRestored closes the loop the daily throttle would otherwise
// leave open: without it, the only proof the console fight is over is a digest
// arriving up to a day later.
const AlertCalendarRestored AlertSubject = "Calendar access restored"

// flagCalendarAccess is the app_state key tracking whether Google is answering.
// Its transitions are what the restored alert fires on — a flag held in memory
// would announce a recovery on every restart, which is the moment it is least
// likely to be true.
const flagCalendarAccess = "calendar_access"

const (
	flagOK     = "ok"
	flagFailed = "failed"
)

// AlertArgs is one thing a person needs to know about.
//
// Only Subject carries the river:"unique" tag, so every calendar-access failure
// that day collapses into the first one regardless of which recipient hit it or
// what Google said. Without the tag, Detail would enter the hash and each
// distinct error string would alert separately — which is the storm this is
// here to prevent.
type AlertArgs struct {
	Subject AlertSubject `json:"subject" river:"unique"`
	Detail  string       `json:"detail"`
}

func (AlertArgs) Kind() string { return "alert" }

func (AlertArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{
		MaxAttempts: alertMaxAttempts,
		UniqueOpts:  river.UniqueOpts{ByArgs: true, ByPeriod: alertPeriod},
	}
}

// Alerter tells the operator that something needs a person rather than another
// retry.
type Alerter interface {
	Alert(ctx context.Context, subject, detail string) error
}

// AlertWorker delivers one operator alert.
type AlertWorker struct {
	river.WorkerDefaults[AlertArgs]
	*Deps
}

func (w *AlertWorker) Work(ctx context.Context, job *river.Job[AlertArgs]) error {
	// Logged whatever happens next, so the record survives an alerter that is
	// unconfigured, broken, or both.
	w.log().Error("alert raised", "subject", job.Args.Subject, "detail", job.Args.Detail)

	if w.Alerter == nil {
		// Not an error: retrying cannot conjure configuration, and failing here
		// would burn the day's unique window on a job that never had a chance.
		return nil
	}
	if err := w.Alerter.Alert(ctx, job.Args.Subject, job.Args.Detail); err != nil {
		return err
	}
	return nil
}

// recordCalendarAccess stores whether Google is answering and raises the
// restored alert on the way back up.
//
// Only the failed→ok transition alerts. An unwritten flag stays quiet, so a
// fresh database does not greet its first successful check by announcing a
// recovery from nothing.
func (d *Deps) recordCalendarAccess(ctx context.Context, healthy bool) {
	value := flagFailed
	if healthy {
		value = flagOK
	}

	previous, err := store.SetFlag(
		ctx, d.DB, store.New(d.DB), flagCalendarAccess, value, d.now())
	if err != nil {
		// Not fatal to the caller: losing the flag costs a recovery message,
		// not a digest.
		d.log().Error("could not record calendar access state", "error", err)
		return
	}
	if healthy && previous == flagFailed {
		d.raise(ctx, AlertCalendarRestored,
			"Google is answering again and digests are being captured. "+
				"Anything still owed for a date that has not passed will go out "+
				"on the next tick.")
	}
}

// raise queues an operator alert, swallowing nothing: if the enqueue itself
// fails there is no second channel to report it on, so it is logged here.
//
// Fire-and-forget by design. Callers raise alerts while handling a failure of
// their own, and the alert must not become the reason that failure is reported
// differently.
func (d *Deps) raise(ctx context.Context, subject AlertSubject, detail string) {
	if d.Jobs == nil {
		d.log().Error("alert not raised: no enqueuer", "subject", subject, "detail", detail)
		return
	}
	_, err := d.Jobs.InsertMany(ctx, []river.InsertManyParams{
		{Args: AlertArgs{Subject: subject, Detail: detail}},
	})
	if err != nil {
		d.log().Error("alert not raised", "subject", subject, "detail", detail, "error", err)
	}
}
