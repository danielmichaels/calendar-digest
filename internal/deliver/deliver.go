// Package deliver turns a digest into the message each channel sends, and
// sends it.
//
// It sits above internal/notify, which knows how to reach a transport but not
// what a digest is, and internal/digest, which knows what a digest is but not
// where it goes.
package deliver

import (
	"fmt"
	"strings"
	"time"

	"github.com/danielmichaels/calendar-digest/internal/calendar"
	"github.com/danielmichaels/calendar-digest/internal/digest"
)

// detailURL builds the /d/{token} link, or returns empty when no BASE_URL is
// configured.
//
// Empty is a real state: BASE_URL is optional at boot, and a digest carrying no
// link is honest where one carrying "/d/xK3f" is a link nothing can follow.
// Every renderer omits the line rather than sending the second kind.
func detailURL(baseURL, token string) string {
	if baseURL == "" || token == "" {
		return ""
	}
	return strings.TrimSuffix(baseURL, "/") + "/d/" + token
}

// headline names the day the events fall on, spelled out in full.
//
// Never "tomorrow": a digest missed overnight is re-sent the following morning
// for its original date, so the relative word is wrong exactly when the digest
// is late — the one case nobody is watching for.
func headline(date string) string {
	d, err := time.Parse(time.DateOnly, date)
	if err != nil {
		// A digest_date that will not parse cannot be recovered here, and a
		// message headed with the raw value still tells the reader which day.
		return date
	}
	return fmt.Sprintf("%s, %d %s", d.Weekday(), d.Day(), d.Month())
}

// timeRange is the left-hand column of a timeline row.
//
// An all-day event's Start and End are midnight boundaries in the recipient's
// zone rather than instants, so formatting them with a clock would report
// "00:00–00:00" — true of the stored value and meaningless to a reader.
func timeRange(ev calendar.Event) string {
	if ev.AllDay {
		return "All day"
	}
	return ev.Start.Format("15:04") + "–" + ev.End.Format("15:04")
}

// eventCount is the phrase the header uses, so that "nothing on" and "1 event"
// do not have to be special-cased at each call site.
func eventCount(d digest.Digest) string {
	switch len(d.Events) {
	case 0:
		return "nothing on"
	case 1:
		return "1 event"
	default:
		return fmt.Sprintf("%d events", len(d.Events))
	}
}
