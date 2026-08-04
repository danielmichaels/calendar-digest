package cmd

import (
	"fmt"

	"github.com/danielmichaels/calendar-digest/internal/calendar"
	"github.com/danielmichaels/calendar-digest/internal/jobs"
	"github.com/danielmichaels/calendar-digest/internal/notify"
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

	jobDeps := &jobs.Deps{Notifiers: map[string]jobs.Notifier{}}
	if credential := app.Config.AppConf.GoogleServiceAccountJSON; credential != "" {
		cal, err := calendar.NewGoogleClient(app.Ctx, credential)
		if err != nil {
			return fmt.Errorf("build calendar client: %w", err)
		}
		jobDeps.Calendar = cal
	} else {
		app.Logger.Warn("GOOGLE_SERVICE_ACCOUNT_JSON is unset: no digest can be captured")
	}

	// Wired before the client starts, so the boot-time calendar check has
	// somewhere to shout. Missing configuration is warned about rather than
	// fatal: a running app that logs its alerts beats one that will not start.
	switch {
	case app.Config.AppConf.TelegramBotToken == "":
		app.Logger.Warn("TELEGRAM_BOT_TOKEN is unset: alerts will only reach the log")
	case app.Config.AppConf.AlertTelegramChatID == "":
		app.Logger.Warn("ALERT_TELEGRAM_CHAT_ID is unset: alerts will only reach the log")
	default:
		jobDeps.Alerter = &notify.TelegramAlerter{
			Bot:    &notify.Telegram{Token: app.Config.AppConf.TelegramBotToken},
			ChatID: app.Config.AppConf.AlertTelegramChatID,
		}
	}

	jobClient, err := jobs.NewClient(app.Ctx, app.DB, app.Config, app.Logger, jobDeps)
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
