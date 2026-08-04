package calendar

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	gcal "google.golang.org/api/calendar/v3"
	"google.golang.org/api/option"
)

var errFakeUnavailable = errors.New("calendar unavailable")

// newTestClient points a real Calendar service at handler, so the request the
// generated client actually builds is the one under test.
func newTestClient(t *testing.T, handler http.HandlerFunc) *GoogleClient {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	svc, err := gcal.NewService(t.Context(),
		option.WithoutAuthentication(),
		option.WithEndpoint(srv.URL),
	)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	return &GoogleClient{svc: svc}
}

func writeEvents(t *testing.T, w http.ResponseWriter, resp gcal.Events) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		t.Errorf("encode response: %v", err)
	}
}

// The query is the contract with Google. singleEvents expands a recurrence
// into its instances, and orderBy=startTime is only accepted alongside it —
// without both, a weekly standup arrives as one master event on the wrong day.
func TestEventsForDayRequestsTheLocalDayWindow(t *testing.T) {
	loc := brisbane(t)
	var got url.Values

	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query()
		writeEvents(t, w, gcal.Events{})
	})

	if _, err := c.EventsForDay(t.Context(), "ada@example.com", "2026-08-05", loc); err != nil {
		t.Fatalf("events for day: %v", err)
	}

	for _, tc := range []struct{ param, want string }{
		{"timeMin", "2026-08-05T00:00:00+10:00"},
		{"timeMax", "2026-08-06T00:00:00+10:00"},
		{"singleEvents", "true"},
		{"orderBy", "startTime"},
	} {
		if have := got.Get(tc.param); have != tc.want {
			t.Errorf("%s = %q, want %q", tc.param, have, tc.want)
		}
	}
}

// The events slice is JSON-encoded straight into the snapshot column, where a
// nil slice stores "null" and an empty one "[]". A reader of the detail page
// should not have to handle both.
func TestEventsForDayReturnsEmptyNotNil(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writeEvents(t, w, gcal.Events{})
	})

	got, err := c.EventsForDay(t.Context(), "ada@example.com", "2026-08-05", brisbane(t))
	if err != nil {
		t.Fatalf("events for day: %v", err)
	}
	if got == nil {
		t.Fatal("got nil, want an empty slice")
	}
	if len(got) != 0 {
		t.Errorf("got %d events, want 0", len(got))
	}

	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != "[]" {
		t.Errorf("encoded as %s, want []", encoded)
	}
}

// A day busy enough to paginate must not silently lose its tail.
func TestEventsForDayFollowsPagination(t *testing.T) {
	loc := brisbane(t)
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("pageToken") == "" {
			writeEvents(t, w, gcal.Events{
				NextPageToken: "page-2",
				Items: []*gcal.Event{{
					Status: "confirmed", Summary: "first",
					Start: &gcal.EventDateTime{DateTime: "2026-08-05T09:00:00+10:00"},
					End:   &gcal.EventDateTime{DateTime: "2026-08-05T09:30:00+10:00"},
				}},
			})
			return
		}
		writeEvents(t, w, gcal.Events{
			Items: []*gcal.Event{{
				Status: "confirmed", Summary: "second",
				Start: &gcal.EventDateTime{DateTime: "2026-08-05T14:00:00+10:00"},
				End:   &gcal.EventDateTime{DateTime: "2026-08-05T14:30:00+10:00"},
			}},
		})
	})

	got, err := c.EventsForDay(t.Context(), "ada@example.com", "2026-08-05", loc)
	if err != nil {
		t.Fatalf("events for day: %v", err)
	}
	if len(got) != 2 || got[0].Summary != "first" || got[1].Summary != "second" {
		t.Errorf("got %d events %+v, want both pages in order", len(got), got)
	}
}

// Google being unavailable has to surface as an error. A DigestJob that
// mistook it for an empty day would write a snapshot saying the day is clear
// and send that, and the snapshot row would then stop any retry.
func TestEventsForDayReportsAnAPIFailure(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":{"code":503,"message":"backend error"}}`, http.StatusServiceUnavailable)
	})

	_, err := c.EventsForDay(t.Context(), "ada@example.com", "2026-08-05", brisbane(t))
	if err == nil {
		t.Fatal("a 503 was reported as a day with no events")
	}
}

func TestEventsForDayRejectsAMalformedDate(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		t.Error("the request should not have been made")
		writeEvents(t, w, gcal.Events{})
	})

	if _, err := c.EventsForDay(t.Context(), "ada@example.com", "tomorrow", brisbane(t)); err == nil {
		t.Error("accepted a date that is not YYYY-MM-DD")
	}
}

func TestNewGoogleClientRejectsAMalformedKey(t *testing.T) {
	if _, err := NewGoogleClient(context.Background(), "not json"); err == nil {
		t.Error("accepted a service account key that is not JSON")
	}
}

// The fake has to answer the same shape as the real client, or Phase 4's tests
// prove nothing about production.
func TestFakeSatisfiesClient(t *testing.T) {
	var c Client = NewFake()

	got, err := c.EventsForDay(t.Context(), "ada@example.com", "2026-08-05", time.UTC)
	if err != nil {
		t.Fatalf("events for day: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Errorf("an unset day returned %+v, want an empty slice", got)
	}
}

func TestFakeRecordsWhatItWasAsked(t *testing.T) {
	loc := brisbane(t)
	f := NewFake()
	f.Set("ada@example.com", "2026-08-05", Event{Summary: "Standup"})

	got, err := f.EventsForDay(t.Context(), "ada@example.com", "2026-08-05", loc)
	if err != nil {
		t.Fatalf("events for day: %v", err)
	}
	if len(got) != 1 || got[0].Summary != "Standup" {
		t.Errorf("got %+v, want the event that was Set", got)
	}

	calls := f.Calls()
	if len(calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(calls))
	}
	want := FakeCall{CalendarID: "ada@example.com", Date: "2026-08-05", Zone: "Australia/Brisbane"}
	if calls[0] != want {
		t.Errorf("call = %+v, want %+v", calls[0], want)
	}
}

func TestFakeReturnsItsError(t *testing.T) {
	f := NewFake()
	f.Err = errFakeUnavailable

	if _, err := f.EventsForDay(t.Context(), "ada@example.com", "2026-08-05", time.UTC); err == nil {
		t.Error("Err was set but the call succeeded")
	}
}

// The whole point of ErrAccess is that the job layer can tell "go and fix the
// Google console" apart from "try again in a minute". Getting a code on the
// wrong side of this line either alerts on a blip or stays silent on a dead
// credential.
func TestEventsForDayClassifiesAccessFailures(t *testing.T) {
	tests := []struct {
		name   string
		status int
		access bool
	}{
		{"401 is a refused credential", http.StatusUnauthorized, true},
		{"403 is a refused credential", http.StatusForbidden, true},
		// Google reports a calendar the service account cannot see as Not
		// Found, so a revoked share arrives as a 404 rather than a 403.
		{"404 is an unshared or mistyped calendar", http.StatusNotFound, true},
		{"429 is worth retrying", http.StatusTooManyRequests, false},
		{"500 is worth retrying", http.StatusInternalServerError, false},
		{"503 is worth retrying", http.StatusServiceUnavailable, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(`{"error":{"message":"nope"}}`))
			})

			_, err := c.EventsForDay(t.Context(), "ada@example.com", "2026-08-05", brisbane(t))
			if err == nil {
				t.Fatal("no error for a failing request")
			}
			if got := errors.Is(err, ErrAccess); got != tc.access {
				t.Errorf("errors.Is(err, ErrAccess) = %v, want %v (err: %v)", got, tc.access, err)
			}
		})
	}
}

func TestVerifyAccessReportsARefusedCalendar(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"message":"forbidden"}}`))
	})

	err := c.VerifyAccess(t.Context(), "ada@example.com")
	if !errors.Is(err, ErrAccess) {
		t.Errorf("err = %v, want it to wrap ErrAccess", err)
	}
}

func TestVerifyAccessAcceptsAReadableCalendar(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writeEvents(t, w, gcal.Events{})
	})

	if err := c.VerifyAccess(t.Context(), "ada@example.com"); err != nil {
		t.Errorf("verify access: %v", err)
	}
}
