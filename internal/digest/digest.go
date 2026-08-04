package digest

import (
	"github.com/danielmichaels/calendar-digest/internal/calendar"
)

// TimeRange describes when an event happens, for any surface that shows one.
//
// It lives here rather than in a renderer because every surface needs the same
// rule and only needs to get it wrong once: an all-day event's Start and End
// are midnight boundaries resolved in the recipient's zone, not instants, so
// formatting them with a clock yields "00:00–00:00" — true of the stored value
// and meaningless to a reader.
func TimeRange(ev calendar.Event) string {
	if ev.AllDay {
		return "All day"
	}
	return ev.Start.Format("15:04") + "–" + ev.End.Format("15:04")
}

// Digest is one recipient's day as a renderer and the detail page see it: a
// snapshot's contents decoded, plus what a message needs in order to address
// and link to it.
//
// It is built from the snapshot row rather than from Google, so what a message
// says and what the page shows cannot drift apart — the events here are the
// ones captured at send time, whatever the calendar says later.
type Digest struct {
	RecipientName string
	// Date is the local date the events fall on, "YYYY-MM-DD".
	Date string
	// Token is the /d/{token} segment. Unguessable because these links leave
	// the network.
	Token  string
	Events []calendar.Event
}
