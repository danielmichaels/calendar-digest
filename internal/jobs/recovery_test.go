package jobs_test

import (
	"database/sql"
	"fmt"
	"testing"

	"github.com/danielmichaels/calendar-digest/internal/calendar"
	"github.com/danielmichaels/calendar-digest/internal/jobs"
	"github.com/danielmichaels/calendar-digest/internal/store"
	"github.com/danielmichaels/calendar-digest/internal/testhelpers"
)

func restoredAlerts(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	for _, a := range argsOfKind[jobs.AlertArgs](t, db) {
		if a.Subject == jobs.AlertCalendarRestored {
			n++
		}
	}
	return n
}

// The recovery message closes the loop the daily throttle leaves open: without
// it the only proof the console fight is over is a digest turning up a day
// later.
func TestVerifyAlertsOnceAccessComesBack(t *testing.T) {
	db := testhelpers.NewDB(t)
	q := store.New(db)
	rc := newEnqueuer(t, db)

	createRecipient(t, q, "ada", "Australia/Brisbane", "21:00")

	fake := calendar.NewFake()
	fake.Err = fmt.Errorf("calendar: verify: %w", calendar.ErrAccess)
	w := verifyWorker(db, rc, fake)

	if err := runVerify(t, w); err != nil {
		t.Fatalf("verify while broken: %v", err)
	}

	fake.Err = nil
	if err := runVerify(t, w); err != nil {
		t.Fatalf("verify after recovery: %v", err)
	}

	alerts := argsOfKind[jobs.AlertArgs](t, db)
	if len(alerts) != 2 {
		t.Fatalf("raised %d alerts, want the failure and the recovery: %v", len(alerts), alerts)
	}
	if alerts[0].Subject != jobs.AlertCalendarAccess {
		t.Errorf("first alert = %q, want the failure", alerts[0].Subject)
	}
	if alerts[1].Subject != jobs.AlertCalendarRestored {
		t.Errorf("second alert = %q, want the recovery", alerts[1].Subject)
	}
}

// A first ever run that succeeds has recovered from nothing. Treating an
// unwritten flag as a transition greets every fresh database with good news
// nobody was waiting for.
func TestVerifyDoesNotAnnounceRecoveryOnAHealthyFirstRun(t *testing.T) {
	db := testhelpers.NewDB(t)
	q := store.New(db)
	rc := newEnqueuer(t, db)

	createRecipient(t, q, "ada", "Australia/Brisbane", "21:00")
	w := verifyWorker(db, rc, calendar.NewFake())

	if err := runVerify(t, w); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if err := runVerify(t, w); err != nil {
		t.Fatalf("second verify: %v", err)
	}

	if alerts := argsOfKind[jobs.AlertArgs](t, db); len(alerts) != 0 {
		t.Errorf("raised %v when nothing was ever broken", alerts)
	}
}

// One refused calendar among several must not record the credential healthy.
// If it does, the next run reads as "already fine" and the recovery message
// never arrives — the failure is announced and then silently forgotten.
func TestVerifyDoesNotReportHealthyWhileOneCalendarIsRefused(t *testing.T) {
	db := testhelpers.NewDB(t)
	q := store.New(db)
	rc := newEnqueuer(t, db)

	createRecipient(t, q, "ada", "Australia/Brisbane", "21:00")
	createRecipient(t, q, "bob", "Australia/Brisbane", "06:30")

	// Only bob's calendar answers, so the run is a partial failure.
	fake := calendar.NewFake()
	fake.Err = fmt.Errorf("calendar: verify: %w", calendar.ErrAccess)
	w := verifyWorker(db, rc, fake)

	if err := runVerify(t, w); err != nil {
		t.Fatalf("verify while broken: %v", err)
	}
	if got := restoredAlerts(t, db); got != 0 {
		t.Fatalf("announced recovery %d times while a calendar was still refused", got)
	}

	fake.Err = nil
	if err := runVerify(t, w); err != nil {
		t.Fatalf("verify after recovery: %v", err)
	}
	if got := restoredAlerts(t, db); got != 1 {
		t.Errorf("raised %d recovery alerts after access came back, want 1", got)
	}
}

// A successful digest capture is proof access works, and it arrives sooner than
// the next daily verification would.
func TestDigestAnnouncesRecoveryAfterAFailedRun(t *testing.T) {
	db := testhelpers.NewDB(t)
	q := store.New(db)
	rc := newEnqueuer(t, db)

	r := createRecipient(t, q, "ada", "Australia/Brisbane", "21:00")

	fake := calendar.NewFake()
	fake.Err = fmt.Errorf("calendar: list events: %w", calendar.ErrAccess)
	w := digestWorker(db, rc, fake)
	args := jobs.DigestArgs{RecipientID: r.ID, DigestDate: "2026-08-05"}

	_ = runDigest(t, w, args)

	fake.Err = nil
	if err := runDigest(t, w, args); err != nil {
		t.Fatalf("digest after recovery: %v", err)
	}

	if got := restoredAlerts(t, db); got != 1 {
		t.Errorf("raised %d recovery alerts, want 1", got)
	}
}
