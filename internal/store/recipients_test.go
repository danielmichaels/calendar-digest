package store_test

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/danielmichaels/calendar-digest/internal/store"
	"github.com/danielmichaels/calendar-digest/internal/testhelpers"
)

func TestCreateRecipientRoundTrips(t *testing.T) {
	q := testhelpers.NewQueries(t)

	created := mustCreateRecipient(t, q, "ada")

	got, err := q.GetRecipient(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("get recipient: %v", err)
	}
	if got != created {
		t.Errorf("read back %+v, want %+v", got, created)
	}
}

// Handlers match on this to answer 404, so the sentinel matters as much as the
// absence.
func TestGetRecipientReportsMissingAsErrNoRows(t *testing.T) {
	q := testhelpers.NewQueries(t)

	_, err := q.GetRecipient(t.Context(), 404)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("err = %v, want sql.ErrNoRows", err)
	}
}

func TestUpdateRecipientChangesEveryField(t *testing.T) {
	q := testhelpers.NewQueries(t)
	created := mustCreateRecipient(t, q, "ada")

	want := store.UpdateRecipientParams{
		ID:         created.ID,
		Name:       "grace",
		CalendarID: "grace@group.calendar.google.com",
		NotifyTime: "06:30",
		Tz:         "Pacific/Auckland",
		Enabled:    false,
	}
	got, err := q.UpdateRecipient(t.Context(), want)
	if err != nil {
		t.Fatalf("update recipient: %v", err)
	}

	if got.Name != want.Name || got.CalendarID != want.CalendarID ||
		got.NotifyTime != want.NotifyTime || got.Tz != want.Tz || got.Enabled != want.Enabled {
		t.Errorf("updated to %+v, want %+v", got, want)
	}
}

// The due-check runs over enabled recipients only. Disabling one has to stop
// the nightly digest, which is the whole point of the flag.
func TestListEnabledRecipientsExcludesDisabled(t *testing.T) {
	q := testhelpers.NewQueries(t)
	on := mustCreateRecipient(t, q, "enabled")
	off := mustCreateRecipient(t, q, "disabled")

	if err := q.SetRecipientEnabled(t.Context(), store.SetRecipientEnabledParams{
		ID: off.ID, Enabled: false,
	}); err != nil {
		t.Fatalf("disable recipient: %v", err)
	}

	got, err := q.ListEnabledRecipients(t.Context())
	if err != nil {
		t.Fatalf("list enabled: %v", err)
	}
	if len(got) != 1 || got[0].ID != on.ID {
		t.Errorf("enabled = %+v, want only %q", got, on.Name)
	}

	all, err := q.ListRecipients(t.Context())
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("ListRecipients returned %d, want 2: disabling must not hide a recipient from the UI", len(all))
	}
}

func TestDeleteRecipientRemovesIt(t *testing.T) {
	q := testhelpers.NewQueries(t)
	created := mustCreateRecipient(t, q, "ada")

	if err := q.DeleteRecipient(t.Context(), created.ID); err != nil {
		t.Fatalf("delete recipient: %v", err)
	}

	if _, err := q.GetRecipient(t.Context(), created.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("err = %v, want sql.ErrNoRows", err)
	}
}
