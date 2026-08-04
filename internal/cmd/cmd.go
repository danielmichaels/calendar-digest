package cmd

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/danielmichaels/calendar-digest/internal/config"
	"github.com/danielmichaels/calendar-digest/internal/logging"
	"github.com/danielmichaels/calendar-digest/internal/store"
)

type Globals struct {
}

type App struct {
	Config *config.Conf
	Logger *slog.Logger
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

// startDatabase brings up Postgres if this process owns it, migrates, then
// opens the pool. Migrations run before the pool so a schema change is in
// place before anything queries through it.
func (a *App) startDatabase() error {
	dsn := a.Config.Db.DbName
	if err := store.MigrateUp(a.Ctx, dsn, a.Logger); err != nil {
		return err
	}

	db, err := store.NewDatabasePool(a.Ctx, a.Config)
	if err != nil {
		return fmt.Errorf("open database pool: %w", err)
	}
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
func (a *App) release() {
	a.Cancel()
}
