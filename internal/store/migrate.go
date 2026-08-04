package store

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/danielmichaels/calendar-digest/assets"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

const (
	// migrationsDir is the path inside assets.EmbeddedFiles, not on disk:
	// migrations ship in the binary so no host needs the goose CLI.
	migrationsDir = "migrations"

	pingAttempts = 30
	pingInterval = time.Second
)

// MigrateUp applies every pending migration. Every replica runs this at boot.
//
// A nil logger discards goose output.
func MigrateUp(ctx context.Context, dsn string, logger *slog.Logger) error {
	return migrate(ctx, dsn, logger, func(db *sql.DB) error {
		return goose.Up(db, migrationsDir)
	})
}

// MigrateUpTo applies migrations up to and including version, leaving later
// ones pending. Tests use it to exercise a specific pre-migration schema.
func MigrateUpTo(ctx context.Context, dsn string, version int64, logger *slog.Logger) error {
	return migrate(ctx, dsn, logger, func(db *sql.DB) error {
		return goose.UpTo(db, migrationsDir, version)
	})
}

func migrate(ctx context.Context, dsn string, logger *slog.Logger, fn func(*sql.DB) error) error {
	db, err := prepareMigrationDB(ctx, dsn, logger)
	if err != nil {
		return err
	}
	defer db.Close()

	goose.SetBaseFS(assets.EmbeddedFiles)
	if err := goose.SetDialect("sqlite3"); err != nil {
		return fmt.Errorf("store: set goose dialect: %w", err)
	}
	if logger == nil {
		goose.SetLogger(goose.NopLogger())
	}

	// No advisory lock: SQLite is single-writer and single-node by nature.
	if err := fn(db); err != nil {
		return fmt.Errorf("store: run migrations: %w", err)
	}
	return nil
}

// prepareMigrationDB opens a migration connection and waits for the database
// to accept queries. The wait matters on boot: an embedded Postgres or a
// container started alongside this process may not be listening yet.
func prepareMigrationDB(ctx context.Context, dsn string, logger *slog.Logger) (*sql.DB, error) {
	// SQLite will not create missing parent directories, and the directory is
	// absent both in a fresh checkout and in the container image, where it is
	// excluded from the build context.
	if dir := filepath.Dir(dsn); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("store: create database directory %s: %w", dir, err)
		}
	}

	db, err := sql.Open("sqlite", DSN(dsn))
	if err != nil {
		return nil, fmt.Errorf("store: open database for migrations: %w", err)
	}

	var lastErr error
	for attempt := 1; attempt <= pingAttempts; attempt++ {
		if lastErr = db.PingContext(ctx); lastErr == nil {
			return db, nil
		}
		if ctx.Err() != nil {
			db.Close()
			return nil, fmt.Errorf("store: waiting for database: %w", ctx.Err())
		}
		if logger != nil {
			logger.Debug(
				"waiting for database",
				"attempt", attempt,
				"of", pingAttempts,
				"err", lastErr,
			)
		}
		time.Sleep(pingInterval)
	}

	db.Close()
	return nil, fmt.Errorf("store: database unreachable after %d attempts: %w", pingAttempts, lastErr)
}
