package ui

import (
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/danielmichaels/calendar-digest/internal/calendar"
	"github.com/danielmichaels/calendar-digest/internal/config"
	"github.com/danielmichaels/calendar-digest/internal/store"
	"github.com/danielmichaels/calendar-digest/internal/testhelpers"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"
)

func newHandlers(t *testing.T) (*Handlers, *store.Queries, *sql.DB) {
	t.Helper()
	db := testhelpers.NewDB(t)
	q := store.New(db)
	return New(Deps{
		Conf: &config.Conf{},
		Log:  slog.New(slog.DiscardHandler),
		Db:   q,
		// scs panics when a handler reads a session the middleware never
		// loaded, so anything mounting Routes has to apply it — see withSession.
		Sessions: scs.New(),
	}), q, db
}

// withSession wraps a handler the way internal/server does. The /app pages read
// flash messages, and scs reads them out of a context value that only
// LoadAndSave puts there.
func withSession(h *Handlers, mount string, handler http.Handler) http.Handler {
	router := chi.NewRouter()
	router.Group(func(r chi.Router) {
		r.Use(h.Sessions.LoadAndSave)
		r.Mount(mount, handler)
	})
	return router
}

// seedSnapshot captures one day for one recipient and returns its token.
func seedSnapshot(t *testing.T, q *store.Queries, events []calendar.Event) (store.Recipients, string) {
	t.Helper()
	r, err := q.CreateRecipient(t.Context(), store.CreateRecipientParams{
		Name:       "Dan",
		CalendarID: "dan@example.com",
		NotifyTime: "21:00",
		Tz:         "Australia/Brisbane",
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("create recipient: %v", err)
	}

	raw, err := json.Marshal(events)
	if err != nil {
		t.Fatalf("encode events: %v", err)
	}
	inserted, err := q.InsertSnapshotIfAbsent(t.Context(), store.InsertSnapshotIfAbsentParams{
		RecipientID: r.ID,
		DigestDate:  "2026-08-05",
		Token:       "xK3fQ9mTn2vB7cLpR4wZ",
		Events:      string(raw),
		CreatedAt:   store.FormatTime(time.Date(2026, 8, 4, 11, 0, 0, 0, time.UTC)),
	})
	if err != nil || inserted != 1 {
		t.Fatalf("seed snapshot: %v (inserted %d)", err, inserted)
	}
	return r, "xK3fQ9mTn2vB7cLpR4wZ"
}

func brisbane(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Australia/Brisbane")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	return loc
}

func fixtureEvents(t *testing.T) []calendar.Event {
	t.Helper()
	loc := brisbane(t)
	return []calendar.Event{
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
			Description:   "Sprint board first,\nthen blockers.",
			Start:         time.Date(2026, 8, 5, 9, 0, 0, 0, loc),
			End:           time.Date(2026, 8, 5, 9, 15, 0, 0, loc),
			Status:        "confirmed",
			ConferenceURL: "https://meet.google.com/abc-defg-hij",
			HTMLLink:      "https://calendar.google.com/event?eid=ev1",
			Organizer:     "dan@example.com",
			Recurring:     true,
			Attendees: []calendar.Attendee{
				{Email: "dan@example.com", DisplayName: "Dan", Self: true, Response: "accepted"},
				{Email: "sam@example.com", DisplayName: "Sam", Response: "needsAction"},
				{Email: "kim@example.com", Response: "declined", Optional: true},
			},
		},
		{
			ID:       "ev4",
			Summary:  "Book club",
			Location: "12 Smith St, Brisbane",
			Start:    time.Date(2026, 8, 5, 19, 30, 0, 0, loc),
			End:      time.Date(2026, 8, 5, 21, 0, 0, 0, loc),
			Status:   "tentative",
		},
	}
}

// getDetail goes through a router mounting DigestRoutes where production
// mounts it, so the test covers the mount path rather than assuming it.
func getDetail(t *testing.T, h *Handlers, token string) *httptest.ResponseRecorder {
	t.Helper()
	router := chi.NewRouter()
	router.Mount("/d", h.DigestRoutes())

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/d/"+token, nil))
	return rec
}

func TestDetailPageShowsEveryUsefulField(t *testing.T) {
	h, q, _ := newHandlers(t)
	_, token := seedSnapshot(t, q, fixtureEvents(t))

	rec := getDetail(t, h, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()

	// Grill Q15: the page is where everything the terse channels dropped lives.
	for _, want := range []string{
		"Dan",
		"Wednesday, 5 August 2026",
		"Public holiday",
		"All day",
		"09:00–09:15",
		"Standup",
		"then blockers.",
		"https://meet.google.com/abc-defg-hij",
		"Sam",
		"kim@example.com",
		"dan@example.com",
		"12 Smith St, Brisbane",
		"Book club",
		"19:30–21:00",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("detail page is missing %q", want)
		}
	}
}

// The link is unguessable, not private: it leaves the network by design, so an
// unknown or purged token must look exactly like a page that never existed.
func TestDetailPage404sOnAnUnknownToken(t *testing.T) {
	h, q, _ := newHandlers(t)
	seedSnapshot(t, q, fixtureEvents(t))

	rec := getDetail(t, h, "notatokenatall")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

// A purged snapshot is the same answer as an unknown one: the row is gone and
// the link in an old message must stop working rather than error.
func TestDetailPage404sAfterAPurge(t *testing.T) {
	h, q, _ := newHandlers(t)
	_, token := seedSnapshot(t, q, fixtureEvents(t))

	if _, err := q.PurgeSnapshotsBefore(t.Context(), "2026-09-01"); err != nil {
		t.Fatalf("purge: %v", err)
	}

	if rec := getDetail(t, h, token); rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 after the snapshot was purged", rec.Code)
	}
}

// The same rule as every renderer: an all-day event's Start is a midnight
// boundary, so a clock here would be wrong and invisible.
func TestDetailPageGivesAnAllDayEventNoClockTime(t *testing.T) {
	h, q, _ := newHandlers(t)
	_, token := seedSnapshot(t, q, fixtureEvents(t))

	body := getDetail(t, h, token).Body.String()

	if strings.Contains(body, "00:00") {
		t.Errorf("all-day event rendered a clock time:\n%s", body)
	}
}

// These URLs travel through Telegram, email and SMS, so they end up in
// people's message history and browser history. Nothing should index them.
func TestDetailPageAsksNotToBeIndexed(t *testing.T) {
	h, q, _ := newHandlers(t)
	_, token := seedSnapshot(t, q, fixtureEvents(t))

	body := getDetail(t, h, token).Body.String()

	if !strings.Contains(body, "noindex") {
		t.Errorf("no robots noindex on a page whose URL is the only thing protecting it")
	}
}

// The events are read back out of the snapshot, not from Google, so the page
// shows what the message said even after the calendar has moved on.
func TestDetailPageShowsAnEmptyDayRatherThanFailing(t *testing.T) {
	h, q, _ := newHandlers(t)
	_, token := seedSnapshot(t, q, nil)

	rec := getDetail(t, h, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for a day with no events", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Nothing on") {
		t.Errorf("empty day did not say so:\n%s", rec.Body.String())
	}
}
