package store_test

import (
	"database/sql"
	"testing"
	"time"

	"github.com/danielmichaels/calendar-digest/internal/testhelpers"
)

// dbPath asks SQLite where the handle's main database lives, so a contender
// can open the same file without the test threading the path around.
func dbPath(t *testing.T, db *sql.DB) string {
	t.Helper()
	var path string
	err := db.QueryRowContext(t.Context(),
		`SELECT file FROM pragma_database_list WHERE name = 'main'`).Scan(&path)
	if err != nil {
		t.Fatalf("read database path: %v", err)
	}
	return path
}

func openWith(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open %s: %v", dsn, err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// Pragmas are per-connection, so this is the assertion that a handle opened
// through DSN is actually configured rather than merely intended to be.
func TestDSNAppliesPragmas(t *testing.T) {
	db := testhelpers.NewDB(t)

	for _, tc := range []struct{ pragma, want string }{
		{"foreign_keys", "1"},
		{"busy_timeout", "10000"},
		{"journal_mode", "wal"},
	} {
		var got string
		if err := db.QueryRowContext(t.Context(), `PRAGMA `+tc.pragma).Scan(&got); err != nil {
			t.Errorf("read %s: %v", tc.pragma, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s = %q, want %q", tc.pragma, got, tc.want)
		}
	}
}

// _txlock=immediate must take the write lock when BEGIN runs rather than at
// the first write. The transaction below writes nothing, so under the default
// deferred locking it would hold no lock and the contender would succeed —
// and a transaction that read first and wrote later would then fail on the
// lock upgrade, which busy_timeout does not cover and so cannot be waited out.
func TestDSNTakesTheWriteLockAtBegin(t *testing.T) {
	db := testhelpers.NewDB(t)
	path := dbPath(t, db)

	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback() //nolint:errcheck

	// A short busy_timeout so the assertion is a fast failure rather than the
	// ten-second wait a production handle would make.
	contender := openWith(t, path+"?_pragma=busy_timeout(100)")
	_, err = contender.ExecContext(t.Context(),
		`INSERT INTO recipients (name, calendar_id, notify_time, tz) VALUES ('c', 'c', '21:00', 'UTC')`)
	if err == nil {
		t.Fatal("a second handle wrote while a transaction was open: BEGIN is not taking the write lock")
	}
}

// The concrete defect behind the shared handle. As generated, the store's
// handle carried no pragmas at all, so when River's handle held the write lock
// it did not wait — it failed instantly with SQLITE_BUSY.
//
// The mechanism is what is asserted, with a short timeout standing in for the
// production ten seconds; TestDSNAppliesPragmas covers the value itself. The
// substitution is not only for speed: SQLite's busy handler sleeps without
// consulting the Go context, so a ten-second wait here would be ten seconds of
// test no ctx deadline could shorten.
func TestBusyTimeoutIsWhatMakesAHandleWait(t *testing.T) {
	db := testhelpers.NewDB(t)
	path := dbPath(t, db)

	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback() //nolint:errcheck

	const insert = `INSERT INTO recipients (name, calendar_id, notify_time, tz) VALUES ('c', 'c', '21:00', 'UTC')`
	const wait = 400 * time.Millisecond

	elapsed := func(t *testing.T, dsn string) time.Duration {
		t.Helper()
		start := time.Now()
		if _, err := openWith(t, dsn).ExecContext(t.Context(), insert); err == nil {
			t.Fatal("contender acquired a lock the open transaction is holding")
		}
		return time.Since(start)
	}

	if got := elapsed(t, path); got >= wait {
		t.Errorf("handle with no pragmas waited %v; it has no busy timeout and should fail at once", got)
	}
	if got := elapsed(t, path+"?_pragma=busy_timeout(400)"); got < wait {
		t.Errorf("handle with busy_timeout(400) gave up after %v, want at least %v", got, wait)
	}
}
