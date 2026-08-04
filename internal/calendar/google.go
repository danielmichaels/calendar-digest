package calendar

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"golang.org/x/oauth2"
	gcal "google.golang.org/api/calendar/v3"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

// ErrAccess means Google refused the credential or the calendar behind it.
//
// Separated from every other failure because the two need opposite handling: a
// timeout or a 5xx is worth retrying, while a revoked key, a lapsed
// domain-wide delegation or an unshared calendar is not — it stays broken until
// somebody opens the Google console. Retrying that quietly is how a household
// stops receiving digests without anyone noticing.
var ErrAccess = errors.New("calendar: access refused")

// classify marks the failures that need a human rather than another attempt.
//
// 404 is included with the two obvious auth codes on purpose: Google reports a
// calendar the service account cannot see as Not Found, so a revoked share and
// a mistyped calendar id both arrive this way, and both need the same person to
// go and fix the same panel.
func classify(err error) error {
	var apiErr *googleapi.Error
	if errors.As(err, &apiErr) {
		switch apiErr.Code {
		case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound:
			return fmt.Errorf("%w: %s (HTTP %d): %w",
				ErrAccess, apiErr.Message, apiErr.Code, err)
		}
	}
	// The token exchange failing at all — an expired, deleted or disabled
	// service account — never reaches an API status code.
	var tokenErr *oauth2.RetrieveError
	if errors.As(err, &tokenErr) {
		return fmt.Errorf("%w: token exchange rejected (%s): %w",
			ErrAccess, tokenErr.ErrorCode, err)
	}
	return err
}

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
		return nil, fmt.Errorf("calendar: list events for %s on %s: %w",
			calendarID, date, classify(err))
	}
	return out, nil
}

// VerifyAccess makes the smallest real request that proves this calendar can
// still be read, returning an error wrapping ErrAccess when it cannot.
//
// It exercises the credential and the sharing grant together, which is the
// point: those are two separate things to lose and both look identical from
// here — nothing arrives.
func (c *GoogleClient) VerifyAccess(ctx context.Context, calendarID string) error {
	if _, err := c.svc.Events.List(calendarID).MaxResults(1).Do(); err != nil {
		return fmt.Errorf("calendar: verify access to %s: %w", calendarID, classify(err))
	}
	return nil
}
