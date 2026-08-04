package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/danielmichaels/calendar-digest/internal/store"
)

// homeNow is a fixed clock: 4 August 2026, 13:00 UTC — 23:00 in Brisbane. Two
// hours past the fixtures' notify time, so the next run is the following day,
// and two hours past the seeded snapshot's created_at, so an undelivered one
// is old enough to be worth warning about.
var homeNow = time.Date(2026, 8, 4, 13, 0, 0, 0, time.UTC)

func getHome(t *testing.T, h *Handlers) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	withSession(h, "/app", h.Routes()).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/app/", nil))
	return rec
}

func addTarget(t *testing.T, q *store.Queries, recipientID int64, kind, config string, enabled bool) store.NotificationTargets {
	t.Helper()
	target, err := q.CreateTarget(t.Context(), store.CreateTargetParams{
		RecipientID: recipientID,
		Kind:        kind,
		Config:      config,
		Enabled:     enabled,
	})
	if err != nil {
		t.Fatalf("create target: %v", err)
	}
	return target
}

func TestHomeListsRecipientsWithTheirTargetsAndNextRun(t *testing.T) {
	h, q, _ := newHandlers(t)
	h.Now = func() time.Time { return homeNow }

	r, _ := seedSnapshot(t, q, fixtureEvents(t))
	addTarget(t, q, r.ID, "telegram", `{"chat_id":"9911"}`, true)
	addTarget(t, q, r.ID, "email", `{"address":"dan@example.com"}`, false)

	rec := getHome(t, h)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()

	for _, want := range []string{
		"Dan",
		"Australia/Brisbane",
		"21:00",
		"telegram",
		"email",
		// The notify boundary is inclusive, so at exactly 21:00 the next run is
		// tomorrow's rather than the one firing now.
		"5 Aug 2026",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("home page is missing %q", want)
		}
	}
}

// A disabled target still exists and still shows, because "why did this person
// stop getting email" is the question the page has to answer.
func TestHomeDistinguishesADisabledTarget(t *testing.T) {
	h, q, _ := newHandlers(t)
	h.Now = func() time.Time { return homeNow }

	r, _ := seedSnapshot(t, q, fixtureEvents(t))
	addTarget(t, q, r.ID, "email", `{"address":"dan@example.com"}`, false)

	body := getHome(t, h).Body.String()
	if !strings.Contains(body, "off") {
		t.Errorf("a disabled target is not marked as off:\n%s", body)
	}
}

// The whole point of the page. A snapshot with no notified_at means every
// enabled channel failed, and nothing else in the app notices — the row is
// silent, the jobs are discarded, and the recipient simply hears nothing.
func TestHomeWarnsAboutADigestThatReachedNobody(t *testing.T) {
	h, q, _ := newHandlers(t)
	h.Now = func() time.Time { return homeNow }

	r, _ := seedSnapshot(t, q, fixtureEvents(t))
	addTarget(t, q, r.ID, "telegram", `{"chat_id":"9911"}`, true)

	body := getHome(t, h).Body.String()

	if !strings.Contains(body, "reached nobody") {
		t.Errorf("no warning for a snapshot with notified_at NULL:\n%s", body)
	}
	if !strings.Contains(body, "2026-08-05") {
		t.Errorf("warning does not say which digest failed:\n%s", body)
	}
}

// Under an hour old it is still being retried: SendJob backs off for up to
// seventeen hours, so warning immediately would cry wolf on every single run.
func TestHomeStaysQuietAboutARecentUnnotifiedDigest(t *testing.T) {
	h, q, _ := newHandlers(t)
	// Ten minutes after the snapshot was captured.
	h.Now = func() time.Time { return time.Date(2026, 8, 4, 11, 10, 0, 0, time.UTC) }

	r, _ := seedSnapshot(t, q, fixtureEvents(t))
	addTarget(t, q, r.ID, "telegram", `{"chat_id":"9911"}`, true)

	if body := getHome(t, h).Body.String(); strings.Contains(body, "reached nobody") {
		t.Errorf("warned about a digest that is still being retried:\n%s", body)
	}
}

func TestHomeStaysQuietAboutADeliveredDigest(t *testing.T) {
	h, q, _ := newHandlers(t)
	h.Now = func() time.Time { return homeNow }

	r, _ := seedSnapshot(t, q, fixtureEvents(t))
	addTarget(t, q, r.ID, "telegram", `{"chat_id":"9911"}`, true)

	snapshot, err := q.GetSnapshotForDate(t.Context(), store.GetSnapshotForDateParams{
		RecipientID: r.ID,
		DigestDate:  "2026-08-05",
	})
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	if _, err := store.MarkNotified(t.Context(), q, snapshot.ID, homeNow); err != nil {
		t.Fatalf("mark notified: %v", err)
	}

	if body := getHome(t, h).Body.String(); strings.Contains(body, "reached nobody") {
		t.Errorf("warned about a digest that was delivered:\n%s", body)
	}
}

// A recipient whose zone or notify_time no longer parses stops receiving
// digests forever, and that looks exactly like an empty calendar. Due reports
// them as Skipped; the page has to show the same thing.
func TestHomeFlagsARecipientWhoseScheduleCannotBeRead(t *testing.T) {
	h, q, _ := newHandlers(t)
	h.Now = func() time.Time { return homeNow }

	if _, err := q.CreateRecipient(t.Context(), store.CreateRecipientParams{
		Name:       "Broken",
		CalendarID: "broken@example.com",
		NotifyTime: "21:00",
		Tz:         "Mars/Olympus",
		Enabled:    true,
	}); err != nil {
		t.Fatalf("create recipient: %v", err)
	}

	body := getHome(t, h).Body.String()
	if !strings.Contains(body, "Mars/Olympus") || !strings.Contains(body, "schedule") {
		t.Errorf("no warning for an unreadable schedule:\n%s", body)
	}
}

func TestHomeWithNoRecipientsSaysSo(t *testing.T) {
	h, _, _ := newHandlers(t)
	h.Now = func() time.Time { return homeNow }

	rec := getHome(t, h)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "No recipients") {
		t.Errorf("empty state missing:\n%s", rec.Body.String())
	}
}

// The link on the home page has to be the one that was messaged out, or the
// operator cannot see what the recipient saw.
func TestHomeLinksTheLatestDigest(t *testing.T) {
	h, q, _ := newHandlers(t)
	h.Now = func() time.Time { return homeNow }

	_, token := seedSnapshot(t, q, fixtureEvents(t))

	if body := getHome(t, h).Body.String(); !strings.Contains(body, "/d/"+token) {
		t.Errorf("home page does not link the latest digest:\n%s", body)
	}
}
