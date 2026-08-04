package calendar

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	gcal "google.golang.org/api/calendar/v3"
)

func brisbane(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Australia/Brisbane")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	return loc
}

// loadFixture reads a recorded Events.list response.
func loadFixture(t *testing.T, name string) []*gcal.Event {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var resp gcal.Events
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode fixture %s: %v", name, err)
	}
	return resp.Items
}

func TestKeep(t *testing.T) {
	tests := []struct {
		name  string
		event *gcal.Event
		want  bool
	}{
		{
			name:  "a plain confirmed event is busy",
			event: &gcal.Event{Status: "confirmed"},
			want:  true,
		},
		{
			// Empty means opaque: Google omits the default rather than sending
			// it, so treating absence as "free" would drop nearly everything.
			name:  "absent transparency means busy",
			event: &gcal.Event{Status: "confirmed", Transparency: ""},
			want:  true,
		},
		{
			name:  "explicitly opaque is busy",
			event: &gcal.Event{Status: "confirmed", Transparency: "opaque"},
			want:  true,
		},
		{
			name:  "transparent is free and does not belong in the digest",
			event: &gcal.Event{Status: "confirmed", Transparency: "transparent"},
			want:  false,
		},
		{
			name:  "cancelled is dropped",
			event: &gcal.Event{Status: "cancelled"},
			want:  false,
		},
		{
			// Tentative still blocks the time, so it belongs in a digest of
			// what the day looks like. Status survives onto the event so a
			// renderer can mark it.
			name:  "tentative is kept",
			event: &gcal.Event{Status: "tentative"},
			want:  true,
		},
		{
			name:  "a cancelled transparent event is dropped once, not twice",
			event: &gcal.Event{Status: "cancelled", Transparency: "transparent"},
			want:  false,
		},
		{
			name:  "absent status is kept",
			event: &gcal.Event{},
			want:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := keep(tc.event); got != tc.want {
				t.Errorf("keep() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestConvertTimedEvent(t *testing.T) {
	loc := brisbane(t)
	items := loadFixture(t, "timed_event.json")

	got := convertAll(items, loc)
	if len(got) != 1 {
		t.Fatalf("events = %d, want 1", len(got))
	}
	ev := got[0]

	if ev.AllDay {
		t.Error("AllDay = true for an event with a dateTime")
	}
	wantStart := time.Date(2026, 8, 5, 9, 30, 0, 0, loc)
	if !ev.Start.Equal(wantStart) {
		t.Errorf("Start = %v, want %v", ev.Start, wantStart)
	}
	if !ev.End.Equal(time.Date(2026, 8, 5, 10, 0, 0, 0, loc)) {
		t.Errorf("End = %v", ev.End)
	}
	if ev.Summary != "Standup" || ev.Location != "Zoom" {
		t.Errorf("summary/location = %q/%q", ev.Summary, ev.Location)
	}
	if ev.ConferenceURL != "https://meet.google.com/abc-defg-hij" {
		t.Errorf("ConferenceURL = %q", ev.ConferenceURL)
	}
	if len(ev.Attendees) != 2 {
		t.Fatalf("attendees = %d, want 2", len(ev.Attendees))
	}
	if !ev.Attendees[0].Self || ev.Attendees[0].Response != "accepted" {
		t.Errorf("first attendee = %+v, want the calendar owner, accepted", ev.Attendees[0])
	}
	if !ev.Attendees[1].Optional {
		t.Error("second attendee should be optional")
	}
}

// An all-day event carries a date and no time, so its boundaries only exist
// once a zone is chosen. Read in the wrong zone it lands on the wrong day.
func TestConvertAllDayEventResolvesInTheRecipientZone(t *testing.T) {
	loc := brisbane(t)
	items := loadFixture(t, "all_day_event.json")

	got := convertAll(items, loc)
	if len(got) != 1 {
		t.Fatalf("events = %d, want 1", len(got))
	}
	ev := got[0]

	if !ev.AllDay {
		t.Error("AllDay = false for an event with a date and no dateTime")
	}
	if !ev.Start.Equal(time.Date(2026, 8, 5, 0, 0, 0, 0, loc)) {
		t.Errorf("Start = %v, want local midnight in %s", ev.Start, loc)
	}
	// Google's end date is exclusive: a one-day event ends on the 6th.
	if !ev.End.Equal(time.Date(2026, 8, 6, 0, 0, 0, 0, loc)) {
		t.Errorf("End = %v, want the next local midnight", ev.End)
	}
}

func TestConvertAllDropsWhatKeepRejects(t *testing.T) {
	loc := brisbane(t)
	items := loadFixture(t, "mixed_day.json")

	got := convertAll(items, loc)

	var summaries []string
	for _, ev := range got {
		summaries = append(summaries, ev.Summary)
	}
	want := []string{"Dentist", "Public holiday"}
	if len(summaries) != len(want) {
		t.Fatalf("kept %v, want %v", summaries, want)
	}
	for i := range want {
		if summaries[i] != want[i] {
			t.Errorf("kept[%d] = %q, want %q", i, summaries[i], want[i])
		}
	}
}

// A malformed timestamp must lose that one event, not the whole digest. A day
// silently reported as empty is worse than a day missing one entry.
func TestConvertAllSkipsAnUnparseableEvent(t *testing.T) {
	loc := brisbane(t)
	items := []*gcal.Event{
		{Status: "confirmed", Summary: "broken", Start: &gcal.EventDateTime{DateTime: "not a time"}},
		{
			Status:  "confirmed",
			Summary: "fine",
			Start:   &gcal.EventDateTime{DateTime: "2026-08-05T09:30:00+10:00"},
			End:     &gcal.EventDateTime{DateTime: "2026-08-05T10:00:00+10:00"},
		},
	}

	got := convertAll(items, loc)
	if len(got) != 1 || got[0].Summary != "fine" {
		t.Errorf("got %+v, want only the parseable event", got)
	}
}

func TestDayWindowSpansLocalMidnights(t *testing.T) {
	loc := brisbane(t)

	start, end, err := DayWindow("2026-08-05", loc)
	if err != nil {
		t.Fatalf("day window: %v", err)
	}
	if !start.Equal(time.Date(2026, 8, 5, 0, 0, 0, 0, loc)) {
		t.Errorf("start = %v", start)
	}
	if !end.Equal(time.Date(2026, 8, 6, 0, 0, 0, 0, loc)) {
		t.Errorf("end = %v", end)
	}
}

// The day a DST transition lands on is 23 or 25 hours long, so the window has
// to advance the calendar date rather than add a fixed 24 hours.
func TestDayWindowHandlesDSTTransitions(t *testing.T) {
	loc, err := time.LoadLocation("Pacific/Auckland")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}

	tests := []struct {
		name  string
		date  string
		hours float64
	}{
		{"clocks go forward, 23 hours", "2026-09-27", 23},
		{"clocks go back, 25 hours", "2026-04-05", 25},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			start, end, err := DayWindow(tc.date, loc)
			if err != nil {
				t.Fatalf("day window: %v", err)
			}
			if got := end.Sub(start).Hours(); got != tc.hours {
				t.Errorf("%s is %v hours long, want %v", tc.date, got, tc.hours)
			}
		})
	}
}

func TestDayWindowRejectsAMalformedDate(t *testing.T) {
	if _, _, err := DayWindow("5 August 2026", time.UTC); err == nil {
		t.Error("accepted a date that is not YYYY-MM-DD")
	}
}
