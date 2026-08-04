package store_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/danielmichaels/calendar-digest/internal/store"
	"github.com/danielmichaels/calendar-digest/internal/testhelpers"
)

var testNow = time.Date(2026, 8, 4, 21, 0, 0, 0, time.UTC)

func upsert(
	t *testing.T,
	db *sql.DB,
	q *store.Queries,
	recipientID int64,
	date, events string,
) (store.DigestSnapshots, bool) {
	t.Helper()
	snap, created, err := store.UpsertSnapshot(t.Context(), db, q, store.UpsertSnapshotParams{
		RecipientID: recipientID,
		DigestDate:  date,
		Events:      events,
		CreatedAt:   testNow,
	})
	if err != nil {
		t.Fatalf("upsert snapshot %s: %v", date, err)
	}
	return snap, created
}

func TestUpsertSnapshotCreatesWhenAbsent(t *testing.T) {
	db := testhelpers.NewDB(t)
	q := store.New(db)
	r := mustCreateRecipient(t, q, "ada")

	snap, created := upsert(t, db, q, r.ID, "2026-08-05", `[{"summary":"standup"}]`)

	if !created {
		t.Error("created = false, want true for a date with no snapshot")
	}
	if snap.Token == "" {
		t.Error("token is empty; the detail page would be unreachable")
	}
	if snap.NotifiedAt.Valid {
		t.Error("notified_at is set on a snapshot nobody has been sent yet")
	}
}

// The idempotency guard the whole design rests on. A second attempt for the
// same date must not replace the events already captured, or the link in a
// message already sent would start showing different content.
func TestUpsertSnapshotReturnsTheExistingRowWithoutOverwriting(t *testing.T) {
	db := testhelpers.NewDB(t)
	q := store.New(db)
	r := mustCreateRecipient(t, q, "ada")

	first, _ := upsert(t, db, q, r.ID, "2026-08-05", `[{"summary":"original"}]`)
	second, created := upsert(t, db, q, r.ID, "2026-08-05", `[{"summary":"replacement"}]`)

	if created {
		t.Error("created = true on a date that already had a snapshot")
	}
	if second.ID != first.ID {
		t.Errorf("id = %d, want the existing %d", second.ID, first.ID)
	}
	if second.Events != first.Events {
		t.Errorf("events = %q, want the original %q", second.Events, first.Events)
	}
	if second.Token != first.Token {
		t.Errorf("token changed from %q to %q; links already sent would break", first.Token, second.Token)
	}
}

// Two recipients are independent: one having today's digest must not stop the
// other from getting theirs.
func TestUpsertSnapshotIsPerRecipient(t *testing.T) {
	db := testhelpers.NewDB(t)
	q := store.New(db)
	ada := mustCreateRecipient(t, q, "ada")
	grace := mustCreateRecipient(t, q, "grace")

	if _, created := upsert(t, db, q, ada.ID, "2026-08-05", "[]"); !created {
		t.Fatal("first recipient not created")
	}
	if _, created := upsert(t, db, q, grace.ID, "2026-08-05", "[]"); !created {
		t.Error("second recipient's snapshot was treated as a duplicate of the first")
	}
}

// UpsertSnapshot has to join the fan-out's transaction: if the SendJob
// enqueues fail, the snapshot must not survive to block the retry.
func TestUpsertSnapshotJoinsAnOuterTransaction(t *testing.T) {
	db := testhelpers.NewDB(t)
	q := store.New(db)
	r := mustCreateRecipient(t, q, "ada")

	err := store.WithTx(t.Context(), db, q,
		func(ctx context.Context, _ *sql.Tx, q *store.Queries) error {
			_, _, err := store.UpsertSnapshot(ctx, db, q, store.UpsertSnapshotParams{
				RecipientID: r.ID, DigestDate: "2026-08-05", Events: "[]", CreatedAt: testNow,
			})
			if err != nil {
				return err
			}
			return errBoom
		})
	if !errors.Is(err, errBoom) {
		t.Fatalf("err = %v, want errBoom", err)
	}

	_, err = q.GetSnapshotForDate(t.Context(), store.GetSnapshotForDateParams{
		RecipientID: r.ID, DigestDate: "2026-08-05",
	})
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("err = %v, want sql.ErrNoRows: the snapshot outlived the rolled-back fan-out", err)
	}
}

func TestMarkNotifiedSetsTheTimestamp(t *testing.T) {
	db := testhelpers.NewDB(t)
	q := store.New(db)
	r := mustCreateRecipient(t, q, "ada")
	snap, _ := upsert(t, db, q, r.ID, "2026-08-05", "[]")

	wasFirst, err := store.MarkNotified(t.Context(), q, snap.ID, testNow)
	if err != nil {
		t.Fatalf("mark notified: %v", err)
	}
	if !wasFirst {
		t.Error("wasFirst = false on a snapshot nobody had been sent yet")
	}

	got, err := q.FindSnapshotByToken(t.Context(), snap.Token)
	if err != nil {
		t.Fatalf("find by token: %v", err)
	}
	if !got.NotifiedAt.Valid {
		t.Fatal("notified_at is still NULL after a successful send")
	}
}

// notified_at means "the first channel that got through", so a second channel
// succeeding must not move it. Every enabled target calls this, and only one
// of them is first.
func TestMarkNotifiedKeepsTheFirstTimestamp(t *testing.T) {
	db := testhelpers.NewDB(t)
	q := store.New(db)
	r := mustCreateRecipient(t, q, "ada")
	snap, _ := upsert(t, db, q, r.ID, "2026-08-05", "[]")

	if _, err := store.MarkNotified(t.Context(), q, snap.ID, testNow); err != nil {
		t.Fatalf("first mark: %v", err)
	}

	later := testNow.Add(time.Minute)
	wasFirst, err := store.MarkNotified(t.Context(), q, snap.ID, later)
	if err != nil {
		t.Fatalf("second mark: %v", err)
	}
	if wasFirst {
		t.Error("wasFirst = true for the second channel to succeed")
	}

	got, err := q.FindSnapshotByToken(t.Context(), snap.Token)
	if err != nil {
		t.Fatal(err)
	}
	if got.NotifiedAt.String != store.FormatTime(testNow) {
		t.Errorf("notified_at = %q, want the first send at %q", got.NotifiedAt.String, store.FormatTime(testNow))
	}
}

func TestFindSnapshotByTokenReportsUnknownAsErrNoRows(t *testing.T) {
	q := testhelpers.NewQueries(t)

	_, err := q.FindSnapshotByToken(t.Context(), "NOTAREALTOKEN")
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("err = %v, want sql.ErrNoRows", err)
	}
}

// What the due check reads to know which digests are already done. Dates
// before the floor are past recovering, so loading them would only add
// candidates the check has to discard.
func TestListSnapshotKeysSinceHonoursTheFloor(t *testing.T) {
	db := testhelpers.NewDB(t)
	q := store.New(db)
	r := mustCreateRecipient(t, q, "ada")

	for _, date := range []string{"2026-08-03", "2026-08-04", "2026-08-05"} {
		upsert(t, db, q, r.ID, date, "[]")
	}

	got, err := q.ListSnapshotKeysSince(t.Context(), "2026-08-04")
	if err != nil {
		t.Fatalf("list keys: %v", err)
	}

	var dates []string
	for _, k := range got {
		dates = append(dates, k.DigestDate)
	}
	// The floor date itself is still recoverable, so the comparison is >=.
	if len(dates) != 2 || dates[0] != "2026-08-04" || dates[1] != "2026-08-05" {
		t.Errorf("dates = %v, want [2026-08-04 2026-08-05]", dates)
	}
}

func TestListSnapshotKeysSinceSpansRecipients(t *testing.T) {
	db := testhelpers.NewDB(t)
	q := store.New(db)
	ada := mustCreateRecipient(t, q, "ada")
	grace := mustCreateRecipient(t, q, "grace")
	upsert(t, db, q, ada.ID, "2026-08-05", "[]")
	upsert(t, db, q, grace.ID, "2026-08-05", "[]")

	got, err := q.ListSnapshotKeysSince(t.Context(), "2026-08-05")
	if err != nil {
		t.Fatalf("list keys: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("keys = %d, want one per recipient", len(got))
	}
}

// The purge is a privacy control, not housekeeping: these rows hold event
// descriptions and attendees, and they replicate to R2.
func TestPurgeSnapshotsBeforeKeepsTheBoundaryDate(t *testing.T) {
	db := testhelpers.NewDB(t)
	q := store.New(db)
	r := mustCreateRecipient(t, q, "ada")

	for _, date := range []string{"2026-05-05", "2026-08-04", "2026-08-05"} {
		upsert(t, db, q, r.ID, date, "[]")
	}

	purged, err := q.PurgeSnapshotsBefore(t.Context(), "2026-08-04")
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if purged != 1 {
		t.Errorf("purged = %d, want 1", purged)
	}

	remaining, err := q.ListSnapshotKeysSince(t.Context(), "0000-00-00")
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 2 {
		t.Errorf("remaining = %d, want 2: the boundary date must survive", len(remaining))
	}
}

func TestFormatTimeRoundTrips(t *testing.T) {
	// Local, not UTC: the stored form has to normalise or text comparison
	// between two timestamps written in different zones is meaningless.
	local := time.Date(2026, 8, 5, 7, 0, 0, 0, time.FixedZone("AEST", 10*3600))

	got, err := store.ParseTime(store.FormatTime(local))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !got.Equal(local) {
		t.Errorf("round trip = %v, want %v", got, local)
	}
	if store.FormatTime(local) != "2026-08-04T21:00:00Z" {
		t.Errorf("stored form = %q, want UTC", store.FormatTime(local))
	}
}
