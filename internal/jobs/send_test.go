package jobs_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/danielmichaels/calendar-digest/internal/calendar"
	"github.com/danielmichaels/calendar-digest/internal/digest"
	"github.com/danielmichaels/calendar-digest/internal/jobs"
	"github.com/danielmichaels/calendar-digest/internal/store"
	"github.com/danielmichaels/calendar-digest/internal/testhelpers"

	"github.com/riverqueue/river"
)

var errChannelDown = errors.New("channel down")

type fakeNotifier struct {
	kind string
	err  error
	sent []digest.Digest
	// target records the config the notifier was handed, which is the only
	// thing that says where a message actually went.
	target []json.RawMessage
}

func (f *fakeNotifier) Kind() string { return f.kind }

func (f *fakeNotifier) Send(
	_ context.Context,
	target json.RawMessage,
	d digest.Digest,
) (string, error) {
	f.sent = append(f.sent, d)
	f.target = append(f.target, target)
	if f.err != nil {
		return "", f.err
	}
	return "rendered body", nil
}

var sentAt = time.Date(2026, 8, 4, 21, 0, 30, 0, time.UTC)

func sendWorker(db *sql.DB, notifiers map[string]jobs.Notifier) *jobs.SendWorker {
	return &jobs.SendWorker{Deps: &jobs.Deps{
		DB:        db,
		Notifiers: notifiers,
		Now:       func() time.Time { return sentAt },
	}}
}

// seedDigest gives a recipient one target and one captured day to send.
func seedDigest(t *testing.T, db *sql.DB, q *store.Queries, kind string) (store.Recipients, store.NotificationTargets, store.DigestSnapshots) {
	t.Helper()
	r := createRecipient(t, q, "ada", "Australia/Brisbane", "21:00")
	target := createTarget(t, q, r.ID, kind, `{"chat_id":"1"}`)

	events, err := json.Marshal([]calendar.Event{{ID: "e1", Summary: "Dentist"}})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, _, err := store.UpsertSnapshot(t.Context(), db, q, store.UpsertSnapshotParams{
		RecipientID: r.ID,
		DigestDate:  "2026-08-05",
		Events:      string(events),
		CreatedAt:   time.Now(),
	})
	if err != nil {
		t.Fatalf("seed snapshot: %v", err)
	}
	return r, target, snapshot
}

func runSend(t *testing.T, w *jobs.SendWorker, args jobs.SendArgs) error {
	t.Helper()
	return w.Work(t.Context(), &river.Job[jobs.SendArgs]{Args: args})
}

func TestSendDeliversTheSnapshottedDayAndMarksItNotified(t *testing.T) {
	db := testhelpers.NewDB(t)
	q := store.New(db)
	r, target, snapshot := seedDigest(t, db, q, "telegram")

	notifier := &fakeNotifier{kind: "telegram"}
	w := sendWorker(db, map[string]jobs.Notifier{"telegram": notifier})

	if err := runSend(t, w, jobs.SendArgs{
		RecipientID: r.ID, DigestDate: "2026-08-05", TargetID: target.ID,
	}); err != nil {
		t.Fatalf("send: %v", err)
	}

	if len(notifier.sent) != 1 {
		t.Fatalf("sent %d digests, want 1", len(notifier.sent))
	}
	got := notifier.sent[0]
	if got.RecipientName != r.Name || got.Date != "2026-08-05" {
		t.Errorf("digest = %+v, want ada's 2026-08-05", got)
	}
	if got.Token != snapshot.Token {
		t.Errorf("token = %q, want the snapshot's %q: the link must reach this page",
			got.Token, snapshot.Token)
	}
	if len(got.Events) != 1 || got.Events[0].Summary != "Dentist" {
		t.Errorf("events = %+v, want the day captured at send time", got.Events)
	}
	if string(notifier.target[0]) != target.Config {
		t.Errorf("target config = %s, want %s", notifier.target[0], target.Config)
	}

	after, err := q.GetSnapshotForDate(t.Context(), store.GetSnapshotForDateParams{
		RecipientID: r.ID, DigestDate: "2026-08-05",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !after.NotifiedAt.Valid {
		t.Error("notified_at is still null after a successful send")
	}
}

// notified_at has to mean somebody was actually told. Setting it on a failed
// send turns the one signal that a digest reached nobody into a lie.
func TestSendLeavesNotifiedAtNullWhenDeliveryFails(t *testing.T) {
	db := testhelpers.NewDB(t)
	q := store.New(db)
	r, target, _ := seedDigest(t, db, q, "telegram")

	notifier := &fakeNotifier{kind: "telegram", err: errChannelDown}
	w := sendWorker(db, map[string]jobs.Notifier{"telegram": notifier})

	err := runSend(t, w, jobs.SendArgs{
		RecipientID: r.ID, DigestDate: "2026-08-05", TargetID: target.ID,
	})
	if !errors.Is(err, errChannelDown) {
		t.Fatalf("err = %v, want the channel's own error so River retries it", err)
	}

	after, err := q.GetSnapshotForDate(t.Context(), store.GetSnapshotForDateParams{
		RecipientID: r.ID, DigestDate: "2026-08-05",
	})
	if err != nil {
		t.Fatal(err)
	}
	if after.NotifiedAt.Valid {
		t.Error("notified_at was set by a send that failed")
	}
}

// Two channels, one digest: the first through the door owns the timestamp, so
// notified_at answers "when was anyone told" rather than "when was the last".
func TestSendKeepsTheFirstDeliveryTimestamp(t *testing.T) {
	db := testhelpers.NewDB(t)
	q := store.New(db)
	r, telegram, _ := seedDigest(t, db, q, "telegram")
	email := createTarget(t, q, r.ID, "email", `{"address":"ada@example.com"}`)

	w := sendWorker(db, map[string]jobs.Notifier{
		"telegram": &fakeNotifier{kind: "telegram"},
		"email":    &fakeNotifier{kind: "email"},
	})

	for _, id := range []int64{telegram.ID, email.ID} {
		if err := runSend(t, w, jobs.SendArgs{
			RecipientID: r.ID, DigestDate: "2026-08-05", TargetID: id,
		}); err != nil {
			t.Fatalf("send via target %d: %v", id, err)
		}
	}

	after, err := q.GetSnapshotForDate(t.Context(), store.GetSnapshotForDateParams{
		RecipientID: r.ID, DigestDate: "2026-08-05",
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := store.FormatTime(sentAt); after.NotifiedAt.String != want {
		t.Errorf("notified_at = %q, want %q", after.NotifiedAt.String, want)
	}
}

// A kind with nothing wired must fail loudly. Returning nil here would set
// notified_at for a message that was never built, let alone sent.
func TestSendFailsWhenTheTargetKindHasNoNotifier(t *testing.T) {
	db := testhelpers.NewDB(t)
	q := store.New(db)
	r, target, _ := seedDigest(t, db, q, "sms")

	w := sendWorker(db, map[string]jobs.Notifier{"telegram": &fakeNotifier{kind: "telegram"}})

	err := runSend(t, w, jobs.SendArgs{
		RecipientID: r.ID, DigestDate: "2026-08-05", TargetID: target.ID,
	})
	if !errors.Is(err, jobs.ErrNoNotifier) {
		t.Fatalf("err = %v, want ErrNoNotifier", err)
	}

	after, err := q.GetSnapshotForDate(t.Context(), store.GetSnapshotForDateParams{
		RecipientID: r.ID, DigestDate: "2026-08-05",
	})
	if err != nil {
		t.Fatal(err)
	}
	if after.NotifiedAt.Valid {
		t.Error("notified_at was set for a kind with no notifier")
	}
}
