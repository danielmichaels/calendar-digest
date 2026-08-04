package store_test

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/danielmichaels/calendar-digest/internal/store"
	"github.com/danielmichaels/calendar-digest/internal/testhelpers"
)

func TestCreateTargetRoundTrips(t *testing.T) {
	q := testhelpers.NewQueries(t)
	r := mustCreateRecipient(t, q, "ada")

	created := mustCreateTarget(t, q, r.ID, "telegram", `{"chat_id":"12345"}`)

	got, err := q.GetTarget(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("get target: %v", err)
	}
	if got != created {
		t.Errorf("read back %+v, want %+v", got, created)
	}
}

// One recipient, every channel: the fan-out this schema exists to support.
func TestListTargetsReturnsEveryChannelForOneRecipient(t *testing.T) {
	q := testhelpers.NewQueries(t)
	r := mustCreateRecipient(t, q, "ada")

	mustCreateTarget(t, q, r.ID, "telegram", `{"chat_id":"1"}`)
	mustCreateTarget(t, q, r.ID, "email", `{"address":"ada@example.com"}`)
	mustCreateTarget(t, q, r.ID, "sms", `{"phone":"+61400000000"}`)

	got, err := q.ListTargets(t.Context(), r.ID)
	if err != nil {
		t.Fatalf("list targets: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("targets = %d, want 3", len(got))
	}

	kinds := map[string]bool{}
	for _, target := range got {
		kinds[target.Kind] = true
	}
	for _, want := range []string{"telegram", "email", "sms"} {
		if !kinds[want] {
			t.Errorf("kind %q missing from %+v", want, got)
		}
	}
}

func TestListTargetsIsScopedToOneRecipient(t *testing.T) {
	q := testhelpers.NewQueries(t)
	mine := mustCreateRecipient(t, q, "mine")
	theirs := mustCreateRecipient(t, q, "theirs")

	mustCreateTarget(t, q, mine.ID, "telegram", `{"chat_id":"1"}`)
	mustCreateTarget(t, q, theirs.ID, "telegram", `{"chat_id":"2"}`)

	got, err := q.ListTargets(t.Context(), mine.ID)
	if err != nil {
		t.Fatalf("list targets: %v", err)
	}
	if len(got) != 1 || got[0].RecipientID != mine.ID {
		t.Errorf("targets = %+v, want only the one belonging to %d", got, mine.ID)
	}
}

// The fan-out enqueues one SendJob per enabled target, so a disabled channel
// has to disappear here or it still gets sent to.
func TestListEnabledTargetsExcludesDisabled(t *testing.T) {
	q := testhelpers.NewQueries(t)
	r := mustCreateRecipient(t, q, "ada")

	on := mustCreateTarget(t, q, r.ID, "telegram", `{"chat_id":"1"}`)
	off := mustCreateTarget(t, q, r.ID, "email", `{"address":"ada@example.com"}`)

	if err := q.SetTargetEnabled(t.Context(), store.SetTargetEnabledParams{
		ID: off.ID, Enabled: false,
	}); err != nil {
		t.Fatalf("disable target: %v", err)
	}

	got, err := q.ListEnabledTargets(t.Context(), r.ID)
	if err != nil {
		t.Fatalf("list enabled targets: %v", err)
	}
	if len(got) != 1 || got[0].ID != on.ID {
		t.Errorf("enabled = %+v, want only the telegram target", got)
	}
}

func TestUpdateTargetConfigReplacesIt(t *testing.T) {
	q := testhelpers.NewQueries(t)
	r := mustCreateRecipient(t, q, "ada")
	created := mustCreateTarget(t, q, r.ID, "email", `{"address":"old@example.com"}`)

	const want = `{"address":"new@example.com"}`
	got, err := q.UpdateTargetConfig(t.Context(), store.UpdateTargetConfigParams{
		ID: created.ID, Config: want,
	})
	if err != nil {
		t.Fatalf("update target config: %v", err)
	}
	if got.Config != want {
		t.Errorf("config = %q, want %q", got.Config, want)
	}
}

func TestDeleteTargetLeavesTheOthers(t *testing.T) {
	q := testhelpers.NewQueries(t)
	r := mustCreateRecipient(t, q, "ada")
	doomed := mustCreateTarget(t, q, r.ID, "telegram", `{"chat_id":"1"}`)
	kept := mustCreateTarget(t, q, r.ID, "email", `{"address":"ada@example.com"}`)

	if err := q.DeleteTarget(t.Context(), doomed.ID); err != nil {
		t.Fatalf("delete target: %v", err)
	}

	got, err := q.ListTargets(t.Context(), r.ID)
	if err != nil {
		t.Fatalf("list targets: %v", err)
	}
	if len(got) != 1 || got[0].ID != kept.ID {
		t.Errorf("remaining = %+v, want only the email target", got)
	}
}

// The CHECK is the only thing standing between a typo and a target nothing
// will ever deliver, since no notifier would match the kind.
func TestCreateTargetRejectsAnUnknownKind(t *testing.T) {
	q := testhelpers.NewQueries(t)
	r := mustCreateRecipient(t, q, "ada")

	_, err := q.CreateTarget(t.Context(), store.CreateTargetParams{
		RecipientID: r.ID, Kind: "carrier-pigeon", Config: "{}", Enabled: true,
	})
	if err == nil {
		t.Fatal("created a target with an unknown kind; the CHECK is not enforced")
	}
}

// Deleting a recipient must not leave targets pointing at nothing. This only
// holds because the handle sets foreign_keys(1) — without it the cascade is
// silently inert.
func TestDeletingARecipientCascadesToItsTargets(t *testing.T) {
	db := testhelpers.NewDB(t)
	q := store.New(db)
	r := mustCreateRecipient(t, q, "ada")
	target := mustCreateTarget(t, q, r.ID, "telegram", `{"chat_id":"1"}`)

	if err := q.DeleteRecipient(t.Context(), r.ID); err != nil {
		t.Fatalf("delete recipient: %v", err)
	}

	if _, err := q.GetTarget(t.Context(), target.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("err = %v, want sql.ErrNoRows: the target outlived its recipient", err)
	}
}
