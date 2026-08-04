// Package calendar reads a recipient's events for one local day. It knows
// nothing about digests, delivery or storage: it answers "what is on this
// calendar on this date, in this timezone".
package calendar

import (
	"context"
	"errors"
	"time"
)

// errMissingEndpoint means an event arrived with no start or no end. It costs
// that event its place in the digest, not the whole day.
var errMissingEndpoint = errors.New("calendar: event has no start or end")

// Event is one calendar entry, reduced to the fields the digest and the detail
// page use.
//
// These JSON tags are a storage format, not a wire format. The whole slice is
// serialised into digest_snapshots.events and read back weeks later, so
// renaming a field silently changes how every existing snapshot renders.
type Event struct {
	ID          string    `json:"id"`
	Summary     string    `json:"summary"`
	Description string    `json:"description,omitempty"`
	Location    string    `json:"location,omitempty"`
	Start       time.Time `json:"start"`
	End         time.Time `json:"end"`
	// AllDay events have no instant of their own — Start and End are midnight
	// boundaries resolved in the recipient's zone, so they must not be
	// rendered as times.
	AllDay bool `json:"all_day"`
	// Status is "confirmed" or "tentative". Cancelled events never get here.
	Status        string     `json:"status,omitempty"`
	Organizer     string     `json:"organizer,omitempty"`
	Attendees     []Attendee `json:"attendees,omitempty"`
	ConferenceURL string     `json:"conference_url,omitempty"`
	HTMLLink      string     `json:"html_link,omitempty"`
	Recurring     bool       `json:"recurring,omitempty"`
}

type Attendee struct {
	Email       string `json:"email"`
	DisplayName string `json:"display_name,omitempty"`
	// Response is Google's responseStatus: accepted, declined, tentative or
	// needsAction.
	Response string `json:"response,omitempty"`
	Optional bool   `json:"optional,omitempty"`
	// Self marks the attendee whose calendar this is.
	Self bool `json:"self,omitempty"`
}

// Client fetches the events for one recipient's day.
//
// The date and its zone travel together because an all-day event is a date
// with no instant: "2026-08-05" starts at a different moment in Brisbane than
// in Auckland, and the window the digest covers is the recipient's local
// midnight-to-midnight, not UTC's.
type Client interface {
	// EventsForDay returns the busy events falling on date, formatted
	// "YYYY-MM-DD" and read in loc, ordered by start time. A day with nothing
	// on it returns an empty slice and no error — that is a digest too.
	//
	// A refused credential or unshared calendar comes back wrapping ErrAccess,
	// which callers must not retry.
	EventsForDay(ctx context.Context, calendarID, date string, loc *time.Location) ([]Event, error)

	// VerifyAccess reports whether calendarID can be read right now, so a
	// broken grant can be found at boot rather than at the notify time.
	VerifyAccess(ctx context.Context, calendarID string) error
}

// DayWindow returns the half-open interval [start, end) that date covers in
// loc: local midnight to the next local midnight.
//
// Half-open, not inclusive, because the two are not the same length on a DST
// boundary. Adding 24 hours to the start lands an hour early or late twice a
// year, where advancing the calendar day does not.
func DayWindow(date string, loc *time.Location) (start, end time.Time, err error) {
	start, err = time.ParseInLocation(time.DateOnly, date, loc)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	return start, start.AddDate(0, 0, 1), nil
}
