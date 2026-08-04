package testhelpers_test

import (
	"testing"

	"github.com/danielmichaels/calendar-digest/internal/testhelpers"
)

func TestNewDBAppliesApplicationSchema(t *testing.T) {
	db := testhelpers.NewDB(t)

	for _, table := range []string{"recipients", "notification_targets", "digest_snapshots"} {
		var name string
		err := db.QueryRowContext(t.Context(),
			`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table,
		).Scan(&name)
		if err != nil {
			t.Errorf("table %s missing: %v", table, err)
		}
	}
}

// River's schema has to be on the same handle as the application's, because
// the whole point of the shared handle is a transaction spanning both.
func TestNewDBAppliesRiverSchema(t *testing.T) {
	db := testhelpers.NewDB(t)

	var name string
	err := db.QueryRowContext(t.Context(),
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'river_job'`,
	).Scan(&name)
	if err != nil {
		t.Fatalf("river_job missing: %v", err)
	}
}

// The pragma is per-connection and silently absent when it is missing, so
// without this the ON DELETE CASCADE in the schema would be decoration.
func TestNewDBEnforcesForeignKeys(t *testing.T) {
	db := testhelpers.NewDB(t)

	_, err := db.ExecContext(t.Context(),
		`INSERT INTO notification_targets (recipient_id, kind, config) VALUES (404, 'telegram', '{}')`)
	if err == nil {
		t.Fatal("insert referencing a missing recipient succeeded; foreign keys are off")
	}
}

func TestNewDBIsEmpty(t *testing.T) {
	db := testhelpers.NewDB(t)

	var count int
	if err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM recipients`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("recipients = %d rows, want 0", count)
	}
}

func TestNewDBIsIsolatedPerCall(t *testing.T) {
	first := testhelpers.NewDB(t)
	second := testhelpers.NewDB(t)

	_, err := first.ExecContext(t.Context(),
		`INSERT INTO recipients (name, calendar_id, notify_time, tz) VALUES ('a', 'c', '21:00', 'UTC')`)
	if err != nil {
		t.Fatal(err)
	}

	var count int
	if err := second.QueryRowContext(t.Context(), `SELECT count(*) FROM recipients`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("second database saw %d rows written to the first; they share storage", count)
	}
}

// A test that opens a transaction and then reaches for the pool deadlocks in
// production, because the pool is capped at one connection. It must deadlock
// here too, or the helper is not reproducing the thing EnsureTx exists to
// prevent.
func TestNewDBIsCappedAtOneConnection(t *testing.T) {
	db := testhelpers.NewDB(t)

	if got := db.Stats().MaxOpenConnections; got != 1 {
		t.Errorf("MaxOpenConnections = %d, want 1", got)
	}
}
