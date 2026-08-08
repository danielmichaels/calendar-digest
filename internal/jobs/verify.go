package jobs

import (
	"context"
	"errors"
	"fmt"

	"github.com/danielmichaels/calendar-digest/internal/calendar"
	"github.com/danielmichaels/calendar-digest/internal/store"

	"github.com/riverqueue/river"
)

// VerifyCalendarAccessArgs checks that every enabled recipient's calendar can
// still be read.
//
// It runs at boot and daily thereafter. Without it a credential that died
// overnight is discovered at the notify time, which for a 21:00 digest means a
// deploy at 10am buys eleven hours of false confidence.
type VerifyCalendarAccessArgs struct{}

func (VerifyCalendarAccessArgs) Kind() string { return "verify_calendar_access" }

type VerifyCalendarAccessWorker struct {
	river.WorkerDefaults[VerifyCalendarAccessArgs]
	*Deps
}

func (w *VerifyCalendarAccessWorker) Work(
	ctx context.Context,
	_ *river.Job[VerifyCalendarAccessArgs],
) error {
	if w.Calendar == nil {
		w.recordCalendarAccess(ctx, false)
		w.raise(ctx, AlertCalendarAccess,
			"GOOGLE_SERVICE_ACCOUNT_JSON is unset, so no digest can be captured at all.")
		return nil
	}

	q := store.New(w.DB)
	recipients, err := q.ListEnabledRecipients(ctx)
	if err != nil {
		return fmt.Errorf("jobs: verify: list recipients: %w", err)
	}

	refused := false
	for _, r := range recipients {
		for _, calendarID := range calendar.IDs(r.CalendarID) {
			err := w.Calendar.VerifyAccess(ctx, calendarID)
			switch {
			case err == nil:
			case errors.Is(err, calendar.ErrAccess):
				refused = true
				w.raise(ctx, AlertCalendarAccess, fmt.Sprintf(
					"%s's calendar (%s) cannot be read. This will not fix itself — the "+
						"service account key or the calendar share needs attention in the "+
						"Google console.\n\n%s", r.Name, calendarID, err))
			default:
				// Transient. Let River retry the whole check rather than alerting
				// on what may be a blip — and leave the flag alone, since a blip is
				// not evidence either way.
				return fmt.Errorf("jobs: verify: %s: %w", calendarID, err)
			}
		}
	}

	// Recorded after every calendar has been tried, so one refused share does
	// not report the whole credential healthy and cancel its own alert.
	w.recordCalendarAccess(ctx, !refused)
	return nil
}
