package digest

import "github.com/danielmichaels/calendar-digest/internal/calendar"

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
