package jobs_test

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/danielmichaels/calendar-digest/internal/calendar"
	"github.com/danielmichaels/calendar-digest/internal/jobs"
	"github.com/danielmichaels/calendar-digest/internal/store"
	"github.com/danielmichaels/calendar-digest/internal/testhelpers"

	"github.com/riverqueue/river"
)

func verifyWorker(db *sql.DB, rc *river.Client[*sql.Tx], cal calendar.Client) *jobs.VerifyCalendarAccessWorker {
	return &jobs.VerifyCalendarAccessWorker{Deps: &jobs.Deps{
		DB: db, Jobs: rc, Calendar: cal,
	}}
}

func runVerify(t *testing.T, w *jobs.VerifyCalendarAccessWorker) error {
	t.Helper()
	return w.Work(t.Context(), &river.Job[jobs.VerifyCalendarAccessArgs]{})
}

// The reason this job exists: a credential that died overnight is found at
// deploy time instead of at 21:00.
func TestVerifyAlertsOnARefusedCalendar(t *testing.T) {
	db := testhelpers.NewDB(t)
	q := store.New(db)
	rc := newEnqueuer(t, db)

	r := createRecipient(t, q, "ada", "Australia/Brisbane", "21:00")

	fake := calendar.NewFake()
	fake.Err = fmt.Errorf("calendar: verify: %w", calendar.ErrAccess)

	if err := runVerify(t, verifyWorker(db, rc, fake)); err != nil {
		t.Fatalf("verify: %v", err)
	}

	alerts := argsOfKind[jobs.AlertArgs](t, db)
	if len(alerts) != 1 {
		t.Fatalf("raised %d alerts, want 1: %v", len(alerts), alerts)
	}
	if alerts[0].Subject != jobs.AlertCalendarAccess {
		t.Errorf("subject = %q", alerts[0].Subject)
	}
	if !strings.Contains(alerts[0].Detail, r.CalendarID) {
		t.Errorf("detail = %q, want it to name the calendar", alerts[0].Detail)
	}
}

func TestVerifyIsSilentWhenEveryCalendarReads(t *testing.T) {
	db := testhelpers.NewDB(t)
	q := store.New(db)
	rc := newEnqueuer(t, db)

	createRecipient(t, q, "ada", "Australia/Brisbane", "21:00")
	createRecipient(t, q, "bob", "Australia/Brisbane", "06:30")

	if err := runVerify(t, verifyWorker(db, rc, calendar.NewFake())); err != nil {
		t.Fatalf("verify: %v", err)
	}

	if alerts := argsOfKind[jobs.AlertArgs](t, db); len(alerts) != 0 {
		t.Errorf("raised %v against healthy calendars", alerts)
	}
}

// No credential at all is the most urgent version of this: nothing can be
// captured for anyone.
func TestVerifyAlertsWhenNoCalendarIsConfigured(t *testing.T) {
	db := testhelpers.NewDB(t)
	q := store.New(db)
	rc := newEnqueuer(t, db)

	createRecipient(t, q, "ada", "Australia/Brisbane", "21:00")

	if err := runVerify(t, verifyWorker(db, rc, nil)); err != nil {
		t.Fatalf("verify: %v", err)
	}

	if alerts := argsOfKind[jobs.AlertArgs](t, db); len(alerts) != 1 {
		t.Errorf("raised %d alerts, want 1: %v", len(alerts), alerts)
	}
}

// A blip must stay retryable rather than becoming a false alarm about the
// Google console.
func TestVerifyRetriesATransientFailureWithoutAlerting(t *testing.T) {
	db := testhelpers.NewDB(t)
	q := store.New(db)
	rc := newEnqueuer(t, db)

	createRecipient(t, q, "ada", "Australia/Brisbane", "21:00")

	fake := calendar.NewFake()
	fake.Err = errors.New("calendar: HTTP 503")

	if err := runVerify(t, verifyWorker(db, rc, fake)); err == nil {
		t.Fatal("no error, so River would not retry a transient failure")
	}
	if alerts := argsOfKind[jobs.AlertArgs](t, db); len(alerts) != 0 {
		t.Errorf("raised %v for a transient failure", alerts)
	}
}

// One broken calendar must not stop the others being checked, or the first
// failure hides every other.
func TestVerifyChecksEveryRecipientDespiteOneFailing(t *testing.T) {
	db := testhelpers.NewDB(t)
	q := store.New(db)
	rc := newEnqueuer(t, db)

	createRecipient(t, q, "ada", "Australia/Brisbane", "21:00")
	createRecipient(t, q, "bob", "Australia/Brisbane", "06:30")

	fake := calendar.NewFake()
	fake.Err = fmt.Errorf("calendar: verify: %w", calendar.ErrAccess)

	if err := runVerify(t, verifyWorker(db, rc, fake)); err != nil {
		t.Fatalf("verify: %v", err)
	}

	if got := len(fake.Calls()); got != 2 {
		t.Errorf("checked %d calendars, want 2", got)
	}
	// Both failures collapse into the day's single alert.
	if alerts := argsOfKind[jobs.AlertArgs](t, db); len(alerts) != 1 {
		t.Errorf("raised %d alerts, want 1: %v", len(alerts), alerts)
	}
}
