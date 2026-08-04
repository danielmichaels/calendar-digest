package calendar

import (
	"time"

	gcal "google.golang.org/api/calendar/v3"
)

// keep reports whether a Google event belongs in a digest of what the day
// looks like.
//
// Two rules, both about the absent value. Google omits transparency when the
// event is opaque, so absence means busy and only the explicit "transparent"
// is free — reading it the other way round drops almost every event. Status is
// likewise omitted on a plain event, so only an explicit "cancelled" is
// dropped. Tentative events stay: the time is still blocked, and Status
// carries through so a renderer can say so.
func keep(ev *gcal.Event) bool {
	return ev.Status != "cancelled" && ev.Transparency != "transparent"
}

// convertAll filters and converts one page of the Events.list response,
// preserving Google's ordering.
//
// An event whose timestamps will not parse is dropped rather than failing the
// page. A digest missing one entry is a smaller lie than a day reported as
// empty because a single malformed event poisoned it.
func convertAll(items []*gcal.Event, loc *time.Location) []Event {
	out := make([]Event, 0, len(items))
	for _, item := range items {
		if !keep(item) {
			continue
		}
		ev, err := convert(item, loc)
		if err != nil {
			continue
		}
		out = append(out, ev)
	}
	return out
}

func convert(item *gcal.Event, loc *time.Location) (Event, error) {
	start, allDay, err := resolve(item.Start, loc)
	if err != nil {
		return Event{}, err
	}
	end, _, err := resolve(item.End, loc)
	if err != nil {
		return Event{}, err
	}

	ev := Event{
		ID:            item.Id,
		Summary:       item.Summary,
		Description:   item.Description,
		Location:      item.Location,
		Start:         start,
		End:           end,
		AllDay:        allDay,
		Status:        item.Status,
		ConferenceURL: item.HangoutLink,
		HTMLLink:      item.HtmlLink,
		Recurring:     item.RecurringEventId != "",
	}
	if item.Organizer != nil {
		ev.Organizer = item.Organizer.Email
	}
	for _, a := range item.Attendees {
		ev.Attendees = append(ev.Attendees, Attendee{
			Email:       a.Email,
			DisplayName: a.DisplayName,
			Response:    a.ResponseStatus,
			Optional:    a.Optional,
			Self:        a.Self,
		})
	}
	return ev, nil
}

// resolve turns one endpoint into an instant, reporting whether it was an
// all-day date.
//
// The two forms are mutually exclusive: a timed event carries dateTime with an
// offset, an all-day one carries a bare date that is not an instant at all
// until a zone is applied. Parsing that date in loc is what puts the event on
// the right day for this recipient — read in UTC it would land on the previous
// day for anyone east of it.
func resolve(dt *gcal.EventDateTime, loc *time.Location) (time.Time, bool, error) {
	if dt == nil {
		return time.Time{}, false, errMissingEndpoint
	}
	if dt.Date != "" {
		t, err := time.ParseInLocation(time.DateOnly, dt.Date, loc)
		return t, true, err
	}
	t, err := time.Parse(time.RFC3339, dt.DateTime)
	if err != nil {
		return time.Time{}, false, err
	}
	return t.In(loc), false, nil
}
