package calendar

import (
	"context"
	"fmt"
	"time"

	gcal "google.golang.org/api/calendar/v3"
	"google.golang.org/api/option"
)

// pageSize is Google's maximum. A household calendar never fills one page, so
// asking for the largest allowed makes pagination the rare path rather than
// the normal one.
const pageSize = 2500

// GoogleClient reads calendars through a service account.
//
// The account holds standing read access to every calendar shared with it,
// with no per-recipient grant to revoke. That is the accepted trade for not
// running an OAuth consent flow for a two-person household.
type GoogleClient struct {
	svc *gcal.Service
}

// NewGoogleClient builds a read-only client from a service account key.
//
// The key is the whole credential and never goes in the database: SQLite
// replicates to R2, and a key in a backup is a key in an object store.
//
// The credential type is pinned to ServiceAccount rather than inferred. A
// credentials JSON can also describe an externally-sourced credential that
// fetches its token from a URL or a local command, so accepting whatever type
// the blob claims turns a tampered environment variable into arbitrary
// outbound requests. Pinning makes anything but a service account key fail to
// load.
func NewGoogleClient(ctx context.Context, serviceAccountJSON string) (*GoogleClient, error) {
	svc, err := gcal.NewService(ctx,
		option.WithAuthCredentialsJSON(option.ServiceAccount, []byte(serviceAccountJSON)),
		option.WithScopes(gcal.CalendarReadonlyScope),
	)
	if err != nil {
		return nil, fmt.Errorf("calendar: build service: %w", err)
	}
	return &GoogleClient{svc: svc}, nil
}

func (c *GoogleClient) EventsForDay(
	ctx context.Context,
	calendarID, date string,
	loc *time.Location,
) ([]Event, error) {
	start, end, err := DayWindow(date, loc)
	if err != nil {
		return nil, fmt.Errorf("calendar: %w", err)
	}

	// Never nil: this is JSON-encoded into the snapshot, where a nil slice
	// would store "null".
	out := []Event{}

	// SingleEvents expands a recurrence into the instances that actually fall
	// in the window, and OrderBy is only accepted alongside it. Without both, a
	// weekly standup arrives as one master event dated whenever the series
	// began.
	call := c.svc.Events.List(calendarID).
		TimeMin(start.Format(time.RFC3339)).
		TimeMax(end.Format(time.RFC3339)).
		SingleEvents(true).
		OrderBy("startTime").
		MaxResults(pageSize)

	err = call.Pages(ctx, func(page *gcal.Events) error {
		out = append(out, convertAll(page.Items, loc)...)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("calendar: list events for %s on %s: %w", calendarID, date, err)
	}
	return out, nil
}
