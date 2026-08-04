// Package testhelpers provides the database tests run against: a fresh SQLite
// file per test, carrying both the application and River schemas, removed when
// the test ends.
package testhelpers

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/danielmichaels/calendar-digest/internal/store"

	"github.com/riverqueue/river/riverdriver/riversqlite"
	"github.com/riverqueue/river/rivermigrate"
	_ "modernc.org/sqlite"
)

// NewDB returns a migrated, empty database belonging to t alone.
//
// A fresh file per test rather than one shared file cleared between them.
// Migrating costs about 10ms, which buys away both the truncate list nobody
// remembers to extend and the leaking of River's own tables — those cannot be
// cleared mid-suite without racing River's maintenance services, so a shared
// database would show every job insert to every later test.
//
// The handle is configured exactly as production's is, capped at one
// connection included, so a test that would deadlock in production deadlocks
// here rather than passing and deferring the discovery.
func NewDB(t *testing.T) *sql.DB {
	t.Helper()

	// Under t.TempDir so removal is registered before the close below.
	// Cleanups run last-registered-first, which is the order that matters: the
	// WAL and shared-memory sidecars are only tidied up by a clean close.
	path := filepath.Join(t.TempDir(), "test.db")
	if err := store.MigrateUp(t.Context(), path, nil); err != nil {
		t.Fatalf("testhelpers: apply application migrations: %v", err)
	}

	db, err := sql.Open("sqlite", store.DSN(path))
	if err != nil {
		t.Fatalf("testhelpers: open %s: %v", path, err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("testhelpers: close database: %v", err)
		}
	})
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	migrator, err := rivermigrate.New(riversqlite.New(db), nil)
	if err != nil {
		t.Fatalf("testhelpers: build river migrator: %v", err)
	}
	if _, err := migrator.Migrate(t.Context(), rivermigrate.DirectionUp, nil); err != nil {
		t.Fatalf("testhelpers: apply river migrations: %v", err)
	}
	return db
}

// NewQueries is NewDB bound to the generated queries, for tests with no use
// for the handle itself.
func NewQueries(t *testing.T) *store.Queries {
	t.Helper()
	return store.New(NewDB(t))
}
