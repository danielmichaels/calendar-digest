package jobs_test

import (
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/danielmichaels/calendar-digest/internal/calendar"
	"github.com/danielmichaels/calendar-digest/internal/jobs"
	"github.com/danielmichaels/calendar-digest/internal/store"
	"github.com/danielmichaels/calendar-digest/internal/testhelpers"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

func createTarget(t *testing.T, q *store.Queries, recipientID int64, kind, config string) store.NotificationTargets {
	t.Helper()
	target, err := q.CreateTarget(t.Context(), store.CreateTargetParams{
		RecipientID: recipientID,
		Kind:        kind,
		Config:      config,
		Enabled:     true,
	})
	if err != nil {
		t.Fatalf("create target: %v", err)
	}
	return target
}

func digestWorker(db *sql.DB, rc *river.Client[*sql.Tx], cal calendar.Client) *jobs.DigestWorker {
	return &jobs.DigestWorker{Deps: &jobs.Deps{
		DB:       db,
		Jobs:     rc,
		Calendar: cal,
		Now:      func() time.Time { return time.Date(2026, 8, 4, 21, 0, 0, 0, time.UTC) },
	}}
}

func runDigest(t *testing.T, w *jobs.DigestWorker, args jobs.DigestArgs) error {
	t.Helper()
	return w.Work(t.Context(), &river.Job[jobs.DigestArgs]{Args: args})
}

func TestDigestCapturesASnapshotAndFansOutOnePerEnabledTarget(t *testing.T) {
	db := testhelpers.NewDB(t)
	q := store.New(db)
	rc := newEnqueuer(t, db)

	r := createRecipient(t, q, "ada", "Australia/Brisbane", "21:00")
	telegram := createTarget(t, q, r.ID, "telegram", `{"chat_id":"1"}`)
	email := createTarget(t, q, r.ID, "email", `{"address":"ada@example.com"}`)

	fake := calendar.NewFake()
	fake.Set(r.CalendarID, "2026-08-05", calendar.Event{ID: "e1", Summary: "Dentist"})

	if err := runDigest(t, digestWorker(db, rc, fake), jobs.DigestArgs{
		RecipientID: r.ID, DigestDate: "2026-08-05",
	}); err != nil {
		t.Fatalf("digest: %v", err)
	}

	snapshot, err := q.GetSnapshotForDate(t.Context(), store.GetSnapshotForDateParams{
		RecipientID: r.ID, DigestDate: "2026-08-05",
	})
	if err != nil {
		t.Fatalf("snapshot not written: %v", err)
	}
	if snapshot.Token == "" {
		t.Error("snapshot has no token, so its page has no address")
	}

	sends := argsOfKind[jobs.SendArgs](t, db)
	if len(sends) != 2 {
		t.Fatalf("enqueued %d sends, want one per enabled target: %v", len(sends), sends)
	}
	for _, want := range []int64{telegram.ID, email.ID} {
		found := false
		for _, s := range sends {
			if s.TargetID == want {
				found = true
			}
		}
		if !found {
			t.Errorf("no send enqueued for target %d", want)
		}
	}
}

// A disabled target is still a row, and the fan-out reads the enabled ones.
func TestDigestSkipsADisabledTarget(t *testing.T) {
	db := testhelpers.NewDB(t)
	q := store.New(db)
	rc := newEnqueuer(t, db)

	r := createRecipient(t, q, "ada", "Australia/Brisbane", "21:00")
	createTarget(t, q, r.ID, "telegram", `{"chat_id":"1"}`)
	off := createTarget(t, q, r.ID, "email", `{"address":"ada@example.com"}`)
	if err := q.SetTargetEnabled(t.Context(), store.SetTargetEnabledParams{
		ID: off.ID, Enabled: false,
	}); err != nil {
		t.Fatalf("disable target: %v", err)
	}

	fake := calendar.NewFake()
	if err := runDigest(t, digestWorker(db, rc, fake), jobs.DigestArgs{
		RecipientID: r.ID, DigestDate: "2026-08-05",
	}); err != nil {
		t.Fatalf("digest: %v", err)
	}

	sends := argsOfKind[jobs.SendArgs](t, db)
	if len(sends) != 1 || sends[0].TargetID == off.ID {
		t.Errorf("enqueued %v, want only the enabled target", sends)
	}
}

// A retry after a partial failure must not fan out again, or every channel that
// worked the first time sends a second copy.
func TestDigestRerunDoesNotFanOutTwice(t *testing.T) {
	db := testhelpers.NewDB(t)
	q := store.New(db)
	rc := newEnqueuer(t, db)

	r := createRecipient(t, q, "ada", "Australia/Brisbane", "21:00")
	createTarget(t, q, r.ID, "telegram", `{"chat_id":"1"}`)

	fake := calendar.NewFake()
	w := digestWorker(db, rc, fake)
	args := jobs.DigestArgs{RecipientID: r.ID, DigestDate: "2026-08-05"}

	if err := runDigest(t, w, args); err != nil {
		t.Fatalf("first digest: %v", err)
	}
	if err := runDigest(t, w, args); err != nil {
		t.Fatalf("second digest: %v", err)
	}

	if sends := argsOfKind[jobs.SendArgs](t, db); len(sends) != 1 {
		t.Errorf("enqueued %d sends over two runs, want 1: %v", len(sends), sends)
	}
}

func TestForcedDigestRerunFansOutAgain(t *testing.T) {
	db := testhelpers.NewDB(t)
	q := store.New(db)
	rc := newEnqueuer(t, db)

	r := createRecipient(t, q, "ada", "Australia/Brisbane", "21:00")
	createTarget(t, q, r.ID, "telegram", `{"chat_id":"1"}`)

	fake := calendar.NewFake()
	w := digestWorker(db, rc, fake)
	args := jobs.DigestArgs{RecipientID: r.ID, DigestDate: "2026-08-05"}

	if err := runDigest(t, w, args); err != nil {
		t.Fatalf("first digest: %v", err)
	}
	args.Force = true
	if err := runDigest(t, w, args); err != nil {
		t.Fatalf("forced digest: %v", err)
	}

	if sends := argsOfKind[jobs.SendArgs](t, db); len(sends) != 2 {
		t.Errorf("enqueued %d sends after forced rerun, want 2: %v", len(sends), sends)
	}
}

// The events a recipient was told about must be the ones the page shows, so a
// rerun leaves the stored day alone even when the calendar has moved on.
func TestDigestRerunDoesNotRewriteTheStoredDay(t *testing.T) {
	db := testhelpers.NewDB(t)
	q := store.New(db)
	rc := newEnqueuer(t, db)

	r := createRecipient(t, q, "ada", "Australia/Brisbane", "21:00")
	fake := calendar.NewFake()
	fake.Set(r.CalendarID, "2026-08-05", calendar.Event{ID: "e1", Summary: "Dentist"})

	w := digestWorker(db, rc, fake)
	args := jobs.DigestArgs{RecipientID: r.ID, DigestDate: "2026-08-05"}
	if err := runDigest(t, w, args); err != nil {
		t.Fatalf("first digest: %v", err)
	}

	fake.Set(r.CalendarID, "2026-08-05", calendar.Event{ID: "e2", Summary: "Cancelled and replaced"})
	if err := runDigest(t, w, args); err != nil {
		t.Fatalf("second digest: %v", err)
	}

	snapshot, err := q.GetSnapshotForDate(t.Context(), store.GetSnapshotForDateParams{
		RecipientID: r.ID, DigestDate: "2026-08-05",
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := "Dentist"; !contains(snapshot.Events, want) {
		t.Errorf("stored events = %s, want the day captured at send time (%q)", snapshot.Events, want)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle ||
		len(needle) == 0 || indexOf(haystack, needle) >= 0)
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

// A refused credential is the failure that never fixes itself. Retrying it
// eight times only delays the alert, so the job stops immediately.
func TestDigestCancelsAndAlertsWhenTheCalendarRefusesAccess(t *testing.T) {
	db := testhelpers.NewDB(t)
	q := store.New(db)
	rc := newEnqueuer(t, db)

	r := createRecipient(t, q, "ada", "Australia/Brisbane", "21:00")
	createTarget(t, q, r.ID, "telegram", `{"chat_id":"1"}`)

	fake := calendar.NewFake()
	fake.Err = fmt.Errorf("calendar: list events: %w", calendar.ErrAccess)

	err := runDigest(t, digestWorker(db, rc, fake), jobs.DigestArgs{
		RecipientID: r.ID, DigestDate: "2026-08-05",
	})

	var cancel *rivertype.JobCancelError
	if !errors.As(err, &cancel) {
		t.Fatalf("err = %v, want a JobCancelError so River stops retrying", err)
	}

	alerts := argsOfKind[jobs.AlertArgs](t, db)
	if len(alerts) != 1 {
		t.Fatalf("raised %d alerts, want 1: %v", len(alerts), alerts)
	}
	if alerts[0].Subject != jobs.AlertCalendarAccess {
		t.Errorf("alert subject = %q, want %q", alerts[0].Subject, jobs.AlertCalendarAccess)
	}
	if !contains(alerts[0].Detail, r.CalendarID) {
		t.Errorf("alert detail = %q, want it to name the calendar that failed", alerts[0].Detail)
	}

	if _, err := q.GetSnapshotForDate(t.Context(), store.GetSnapshotForDateParams{
		RecipientID: r.ID, DigestDate: "2026-08-05",
	}); !errors.Is(err, sql.ErrNoRows) {
		t.Error("a snapshot was written for a day that could not be read")
	}
}

// The due check offers the digest again every five minutes while access stays
// broken. Without the daily unique window that is 288 Telegram messages a day,
// which is the same as none.
func TestDigestAlertsOnceADayWhileAccessStaysBroken(t *testing.T) {
	db := testhelpers.NewDB(t)
	q := store.New(db)
	rc := newEnqueuer(t, db)

	ada := createRecipient(t, q, "ada", "Australia/Brisbane", "21:00")
	bob := createRecipient(t, q, "bob", "Australia/Brisbane", "21:00")

	fake := calendar.NewFake()
	fake.Err = fmt.Errorf("calendar: list events: %w", calendar.ErrAccess)
	w := digestWorker(db, rc, fake)

	// Two recipients, several ticks: every one of these raises the same alert.
	for range 3 {
		_ = runDigest(t, w, jobs.DigestArgs{RecipientID: ada.ID, DigestDate: "2026-08-05"})
		_ = runDigest(t, w, jobs.DigestArgs{RecipientID: bob.ID, DigestDate: "2026-08-05"})
	}

	if alerts := argsOfKind[jobs.AlertArgs](t, db); len(alerts) != 1 {
		t.Errorf("raised %d alerts, want 1 for the day: %v", len(alerts), alerts)
	}
}

// Losing the credential entirely is a configuration failure rather than a
// Google one, and it is even more urgent: nothing can be captured at all.
func TestDigestAlertsWhenNoCalendarIsConfigured(t *testing.T) {
	db := testhelpers.NewDB(t)
	q := store.New(db)
	rc := newEnqueuer(t, db)

	r := createRecipient(t, q, "ada", "Australia/Brisbane", "21:00")

	err := runDigest(t, digestWorker(db, rc, nil), jobs.DigestArgs{
		RecipientID: r.ID, DigestDate: "2026-08-05",
	})

	var cancel *rivertype.JobCancelError
	if !errors.As(err, &cancel) {
		t.Fatalf("err = %v, want a JobCancelError", err)
	}
	if alerts := argsOfKind[jobs.AlertArgs](t, db); len(alerts) != 1 {
		t.Errorf("raised %d alerts, want 1: %v", len(alerts), alerts)
	}
}

// A blip is not an alert. Retrying is River's job and this must stay on the
// retryable path, or a rate limit becomes a false alarm.
func TestDigestRetriesATransientCalendarFailure(t *testing.T) {
	db := testhelpers.NewDB(t)
	q := store.New(db)
	rc := newEnqueuer(t, db)

	r := createRecipient(t, q, "ada", "Australia/Brisbane", "21:00")

	fake := calendar.NewFake()
	fake.Err = errors.New("calendar: HTTP 503")

	err := runDigest(t, digestWorker(db, rc, fake), jobs.DigestArgs{
		RecipientID: r.ID, DigestDate: "2026-08-05",
	})
	if err == nil {
		t.Fatal("no error for a failing fetch")
	}

	var cancel *rivertype.JobCancelError
	if errors.As(err, &cancel) {
		t.Error("a transient failure cancelled the job instead of retrying")
	}
	if alerts := argsOfKind[jobs.AlertArgs](t, db); len(alerts) != 0 {
		t.Errorf("raised %v for a transient failure", alerts)
	}
}
