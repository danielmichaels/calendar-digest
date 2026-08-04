package deliver

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/danielmichaels/calendar-digest/internal/calendar"
	"github.com/danielmichaels/calendar-digest/internal/digest"
)

var update = flag.Bool("update", false, "rewrite the golden files from the current output")

// golden compares got against testdata/name, or rewrites it under -update.
//
// The file is the specification: a diff here is a change to what a person
// receives, and is meant to be read rather than regenerated reflexively.
func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)

	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatalf("create testdata: %v", err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run with -update to create it): %v", err)
	}
	if got != string(want) {
		t.Errorf("output does not match %s\n--- got ---\n%s\n--- want ---\n%s", path, got, want)
	}
}

const testBaseURL = "https://calendar.int.lookout.wiki"

func brisbane(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Australia/Brisbane")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	return loc
}

// throughJSON round-trips a digest's events the way delivery really receives
// them: written into digest_snapshots.events by one job and read back by
// another.
//
// Renderers are never handed a *time.Location, so this is what proves they do
// not need one — RFC3339 carries the recipient's offset, and a time decoded
// from it formats to the same wall clock the recipient keeps.
func throughJSON(t *testing.T, d digest.Digest) digest.Digest {
	t.Helper()
	raw, err := json.Marshal(d.Events)
	if err != nil {
		t.Fatalf("encode events: %v", err)
	}
	var events []calendar.Event
	if err := json.Unmarshal(raw, &events); err != nil {
		t.Fatalf("decode events: %v", err)
	}
	d.Events = events
	return d
}

// fullDay is the fixture every renderer is measured against: a timed event
// with a conference link, a timed event with a location and an organiser, an
// all-day event, and a tentative one.
func fullDay(t *testing.T) digest.Digest {
	t.Helper()
	loc := brisbane(t)
	return throughJSON(t, digest.Digest{
		RecipientName: "Dan",
		Date:          "2026-08-05",
		Token:         "xK3fQ9mTn2vB7cLpR4wZ",
		// Ordered as Google returns them: the client asks for orderBy=startTime
		// and an all-day event starts at midnight, so it leads the day.
		Events: []calendar.Event{
			{
				ID:      "ev3",
				Summary: "Public holiday",
				Start:   time.Date(2026, 8, 5, 0, 0, 0, 0, loc),
				End:     time.Date(2026, 8, 6, 0, 0, 0, 0, loc),
				AllDay:  true,
				Status:  "confirmed",
			},
			{
				ID:            "ev1",
				Summary:       "Standup",
				Start:         time.Date(2026, 8, 5, 9, 0, 0, 0, loc),
				End:           time.Date(2026, 8, 5, 9, 15, 0, 0, loc),
				Status:        "confirmed",
				ConferenceURL: "https://meet.google.com/abc-defg-hij",
				Organizer:     "dan@example.com",
				Recurring:     true,
				Attendees: []calendar.Attendee{
					{Email: "dan@example.com", DisplayName: "Dan", Self: true, Response: "accepted"},
					{Email: "sam@example.com", DisplayName: "Sam", Response: "accepted"},
				},
			},
			{
				ID:          "ev2",
				Summary:     "Dentist",
				Description: "Bring the referral letter.",
				Location:    "12 Smith St, Brisbane",
				Start:       time.Date(2026, 8, 5, 14, 0, 0, 0, loc),
				End:         time.Date(2026, 8, 5, 15, 0, 0, 0, loc),
				Status:      "confirmed",
				Organizer:   "reception@dental.example",
				HTMLLink:    "https://calendar.google.com/event?eid=ev2",
			},
			{
				ID:      "ev4",
				Summary: "Book club",
				Start:   time.Date(2026, 8, 5, 19, 30, 0, 0, loc),
				End:     time.Date(2026, 8, 5, 21, 0, 0, 0, loc),
				Status:  "tentative",
			},
		},
	})
}

// emptyDay is grill Q5: an empty calendar still sends, so a working run is
// distinguishable from a missing one.
func emptyDay(t *testing.T) digest.Digest {
	t.Helper()
	return throughJSON(t, digest.Digest{
		RecipientName: "Dan",
		Date:          "2026-08-05",
		Token:         "xK3fQ9mTn2vB7cLpR4wZ",
	})
}
