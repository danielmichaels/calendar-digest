package cmd

import (
	"fmt"

	"github.com/danielmichaels/calendar-digest/internal/jobs"
	"github.com/danielmichaels/calendar-digest/internal/server"
)

type ServeCmd struct {
}

func (s *ServeCmd) Run() error {
	app, err := NewApp()
	if err != nil {
		return err
	}
	defer app.Close()

	deps := server.Deps{
		Conf: app.Config,
		Log:  app.Logger,
		Db:   app.Store,
	}

	jobClient, err := jobs.NewClient(app.Ctx, app.Config.Db.DbName, app.Config, app.Logger)
	if err != nil {
		return fmt.Errorf("create job client: %w", err)
	}
	deps.Jobs = jobClient
	srv := server.New(deps)

	if err := srv.Start(app.Ctx); err != nil {
		return fmt.Errorf("start background workers: %w", err)
	}
	defer func() {
		if err := srv.Stop(app.Ctx); err != nil {
			app.Logger.Error("stopping background workers", "error", err)
		}
	}()

	if err := srv.Serve(app.Ctx); err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	return nil
}
