package store_test

import (
	"testing"

	"github.com/danielmichaels/calendar-digest/internal/store"
)

func newRecipient(name string) store.CreateRecipientParams {
	return store.CreateRecipientParams{
		Name:       name,
		CalendarID: name + "@group.calendar.google.com",
		NotifyTime: "21:00",
		Tz:         "Australia/Brisbane",
		Enabled:    true,
	}
}

func mustCreateRecipient(t *testing.T, q *store.Queries, name string) store.Recipients {
	t.Helper()
	r, err := q.CreateRecipient(t.Context(), newRecipient(name))
	if err != nil {
		t.Fatalf("create recipient %q: %v", name, err)
	}
	return r
}

func mustCreateTarget(
	t *testing.T,
	q *store.Queries,
	recipientID int64,
	kind, config string,
) store.NotificationTargets {
	t.Helper()
	target, err := q.CreateTarget(t.Context(), store.CreateTargetParams{
		RecipientID: recipientID,
		Kind:        kind,
		Config:      config,
		Enabled:     true,
	})
	if err != nil {
		t.Fatalf("create %s target: %v", kind, err)
	}
	return target
}
