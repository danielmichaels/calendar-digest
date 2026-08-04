package store

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/danielmichaels/calendar-digest/internal/config"

	_ "modernc.org/sqlite"
)

// DSN builds the connection string for the database at path. Every handle
// opened against the database must go through it: pragmas apply per
// connection, not per database file, so a handle opened without them runs with
// foreign keys off and no busy timeout while looking identical from outside.
//
// _txlock=immediate takes the write lock at BEGIN rather than at the first
// write statement. Deferred, a transaction that reads before it writes fails
// with SQLITE_BUSY when it tries to upgrade the lock, and busy_timeout does
// not apply to upgrades — so the failure is instant and cannot be waited out
// from inside the transaction.
func DSN(path string) string {
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	return path + sep + strings.Join([]string{
		"_pragma=busy_timeout(10000)",
		"_pragma=journal_mode(WAL)",
		"_pragma=foreign_keys(1)",
		"_txlock=immediate",
	}, "&")
}

// NewDatabasePool opens the one handle the process shares. River, the HTTP
// handlers and the migrator all use it, so a transaction started here can span
// an application write and a job insert.
//
// It is capped at a single connection deliberately. SQLite serialises writers
// regardless, and with a cap of one, code that reaches for the pool while a
// transaction is open does not queue — it waits for the connection that
// transaction is holding until busy_timeout fires. Anything that might run
// inside a transaction must therefore take the tx-bound *Queries, which is
// what EnsureTx is for.
func NewDatabasePool(ctx context.Context, cfg *config.Conf) (*sql.DB, error) {
	db, err := sql.Open("sqlite", DSN(cfg.Db.DbName))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err = db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}
