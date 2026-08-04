// Package templates holds the templ components and the view models they
// render.
//
// View models live in this plain Go file, not in a .templ file: only the
// generated _templ.go is compiled, so a type declared in a .templ would not
// exist until `task templ` had run.
package templates

type HomeView struct {
	Title string
	// Flash is a one-shot message surviving exactly one render.
	Flash      string
	Recipients []RecipientRow
	// Undelivered are digests that were captured and then reached nobody. They
	// lead the page because nothing else in the app notices them: the jobs are
	// discarded into riverui and the recipient just hears silence.
	Undelivered []UndeliveredRow
	// BaseURL is shown because every notification embeds it, so a wrong one is
	// invisible until somebody taps a link that goes nowhere.
	BaseURL string
}

type RecipientRow struct {
	ID   int64
	Name string
	// Schedule is the notify time and zone as configured, e.g. "21:00
	// Australia/Brisbane".
	Schedule string
	Enabled  bool
	// NextRun is when the next digest is owed, empty when ScheduleProblem is
	// not.
	NextRun string
	// ScheduleProblem is set when the zone or notify time cannot be read. Such
	// a recipient silently stops receiving anything, which is identical from
	// the outside to a calendar with nothing on it.
	ScheduleProblem string
	Targets         []TargetRow
	// LastDigest is the most recent captured day, empty if there is none yet.
	LastDigest    string
	LastDigestURL string
	// Delivered says whether that last digest reached anybody at all — not
	// whether it reached everybody, which notified_at cannot answer.
	Delivered bool
}

type TargetRow struct {
	ID   int64
	Kind string
	// Address is the human-readable part of the target's config: a chat id, an
	// email address, a phone number.
	Address string
	Enabled bool
}

type UndeliveredRow struct {
	RecipientName string
	DigestDate    string
	// Age is how long it has been failing, e.g. "3 hours".
	Age string
	URL string
}

// DigestView is one captured day as the detail page shows it.
//
// Grill Q15 puts everything here that the terse channels dropped, so the
// hierarchy is what a person opening the link at nine in the evening needs
// first: which day, then when each thing is, then how to get to it, then who
// else is coming.
type DigestView struct {
	Title         string
	RecipientName string
	// Zone names the timezone the times below are in, because a forwarded link
	// can easily be read by somebody in another one.
	Zone string
	// Date is the long form, with the year: this page outlives the message it
	// came from.
	Date   string
	Events []EventView
	// CapturedAt says when the calendar was read. It is the page's honesty: the
	// events are a snapshot, and the real calendar may have moved on since.
	CapturedAt string
}

type EventView struct {
	// Time is digest.TimeRange, so an all-day event never shows a clock.
	Time      string
	Summary   string
	Tentative bool
	Recurring bool
	Location  string
	// Description is rendered as text, never as markup. Google allows HTML in
	// it, and this is somebody else's calendar.
	Description   string
	ConferenceURL string
	// CalendarURL opens the event in Google Calendar, for changing it.
	CalendarURL string
	Organizer   string
	Attendees   []AttendeeView
}

type AttendeeView struct {
	Name string
	// Response is the RSVP in words: "Accepted", "Maybe", "No reply".
	Response string
	Optional bool
}
