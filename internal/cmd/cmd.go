package cmd

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"

	"github.com/danielmichaels/calendar-digest/internal/config"
	"github.com/danielmichaels/calendar-digest/internal/logging"
	"github.com/danielmichaels/calendar-digest/internal/store"
)

type Globals struct {
}

// App is the process-wide wiring. DB is exposed alongside Store because the
// job client and the transaction helpers need the handle itself, not just the
// generated queries bound to it.
type App struct {
	Config *config.Conf
	Logger *slog.Logger
	DB     *sql.DB
	Store  *store.Queries
	Ctx    context.Context
	Cancel context.CancelFunc
}

// NewApp loads configuration, brings up the database, and applies migrations.
// Everything it starts is released by Close, including on the error paths
// here.
func NewApp() (*App, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	logger := logging.SetupLogger(cfg)
	ctx, cancel := context.WithCancel(context.Background())

	a := &App{
		Config: cfg,
		Logger: logger,
		Ctx:    ctx,
		Cancel: cancel,
	}

	if err := a.startDatabase(); err != nil {
		a.release()
		return nil, err
	}
	return a, nil
}

// startDatabase migrates, then opens the shared handle. Migrations run first
// so a schema change is in place before anything queries through it, and on
// its own short-lived connection so the shared handle never sees a
// half-applied schema.
func (a *App) startDatabase() error {
	dsn := a.Config.Db.DbName
	if err := store.MigrateUp(a.Ctx, dsn, a.Logger); err != nil {
		return err
	}

	db, err := store.NewDatabasePool(a.Ctx, a.Config)
	if err != nil {
		return fmt.Errorf("open database pool: %w", err)
	}
	a.DB = db
	a.Store = store.New(db)
	return nil
}

func (a *App) Close() {
	a.Logger.Info("shutting down")
	a.release()
	a.Logger.Info("shutdown complete")
}

// release tears down whatever was started, in reverse order, and tolerates
// partially-built state so NewApp can use it on its error paths.
//
// The handle closes after the cancel so anything still draining on the context
// finishes against a live database rather than a closed one.
func (a *App) release() {
	a.Cancel()
	if a.DB != nil {
		if err := a.DB.Close(); err != nil {
			a.Logger.Error("closing database", "error", err)
		}
	}
}
